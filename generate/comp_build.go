package generate

import (
	"fmt"
	"strconv"
	"strings"
)

// Compositional folds (#84). foldCompDecode/foldCompEncode build the cd*/ce*
// node trees from a StructInfo by recursing over fieldType (typeexpr.go) and
// threading the TOML position (compPos) plus the emitHandles flag. These replace
// the FieldKind switches in ir_build.go.
//
// compPos.tkey is the full prefixed/dotted key for this position; child()
// extends it. Consumed marks and table-header matching both derive from it, so
// the map-entry _mk splice (cdMapStruct) lands in inner leaf keys without the
// post-hoc injectMapKey rewrite the enumerated renderer needed.
//
// arrayDepth is the number of enclosing array-table levels; it names the loop
// index/entry vars (arrayIdxVar/arrayEntryVar) so nested arrays never collide on
// `i`/`_node`. It only increments when descending through an array (#87).

type compPos struct {
	tkey       TOMLKey
	tgt        TargetPath
	arrayDepth int
	scoped     bool // inside an array-table or map entry: nested arrays' headers carry a runtime key segment, so encode must find/append within the parent container, not document-wide
	seq        *int // monotonic counter for unique decode locals (nil on encode)
}

func (p compPos) child(tomlKey, goName string) compPos {
	return compPos{tkey: p.tkey.Dot(tomlKey), tgt: p.tgt.Dot(goName), arrayDepth: p.arrayDepth, scoped: p.scoped, seq: p.seq}
}

// nextLocal returns a process-unique local variable name within one generated
// decode function. Used for nil-guard pointer-struct locals so nested structs
// with same-named fields never collide (key-derived names fail under the #55
// flat fallback, which strips the path).
func (p compPos) nextLocal(prefix string) string {
	if p.seq == nil {
		return prefix + "Val"
	}
	id := *p.seq
	*p.seq++
	return fmt.Sprintf("%s%d", prefix, id)
}

func arrayIdxVar(depth int) string {
	if depth == 0 {
		return "i"
	}
	return "i" + strconv.Itoa(depth)
}

func arrayEntryVar(depth int) string {
	if depth == 0 {
		return "_node"
	}
	return "_node" + strconv.Itoa(depth)
}

// --- Decode fold ---

func foldCompDecode(si *StructInfo, pos compPos, emitHandles bool) []cdNode {
	var out []cdNode
	for _, fi := range si.Fields {
		if n := foldCompDecodeField(fi, pos, emitHandles); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func foldCompDecodeField(fi FieldInfo, pos compPos, emitHandles bool) cdNode {
	c := pos.child(fi.TomlKey, fi.GoName)

	switch te := fieldType(fi).(type) {
	case spkScalar:
		switch te.Codec {
		case codecPrim:
			return cdLeaf{Kind: cdLeafPrim, Tgt: c.tgt, TKey: c.tkey, TypeName: fi.TypeName, ElemType: fi.ElemType, ImportPath: fi.ImportPath}
		case codecCustom:
			return cdLeaf{Kind: cdLeafCustom, Tgt: c.tgt, TKey: c.tkey}
		case codecText:
			return cdLeaf{Kind: cdLeafText, Tgt: c.tgt, TKey: c.tkey}
		}

	case spkPtr:
		switch te.Elem.(type) {
		case spkScalar:
			return cdLeaf{Kind: cdLeafPrim, Tgt: c.tgt, TKey: c.tkey, TypeName: fi.TypeName, Pointer: true}
		case spkStruct:
			return compDecodeNilGuard(fi, c)
		case spkDelegated:
			return cdDelStruct{Tgt: c.tgt, TKey: c.tkey, ImportPath: fi.ImportPath, TypeName: fi.TypeName, Ptr: true}
		}

	case spkSlice:
		switch elem := te.Elem.(type) {
		case spkScalar:
			if elem.Codec == codecText {
				return cdLeaf{Kind: cdLeafSliceText, Tgt: c.tgt, TKey: c.tkey, TypeName: fi.TypeName, ImportPath: fi.ImportPath}
			}
			return cdLeaf{Kind: cdLeafSlicePrim, Tgt: c.tgt, TKey: c.tkey, ElemType: fi.ElemType, TypeName: fi.TypeName, ImportPath: fi.ImportPath, SlicePointer: fi.SlicePointer}
		case spkStruct:
			return compDecodeArrayTable(fi, c, false, emitHandles)
		case spkDelegated:
			return cdDelSlice{Tgt: c.tgt, TKey: StaticKey(fi.TomlKey), TDottedKey: c.tkey, ImportPath: fi.ImportPath, TypeName: fi.TypeName, SlicePtr: false, IdxVar: arrayIdxVar(c.arrayDepth)}
		case spkPtr:
			switch elem.Elem.(type) {
			case spkScalar:
				// []*prim: FieldSlicePrimitive carries the pointer as fieldType's
				// Slice(Ptr(Scalar)), so it surfaces here, not under spkScalar.
				return cdLeaf{Kind: cdLeafSlicePrim, Tgt: c.tgt, TKey: c.tkey, ElemType: fi.ElemType, TypeName: fi.TypeName, ImportPath: fi.ImportPath, SlicePointer: true}
			case spkStruct:
				return compDecodeArrayTable(fi, c, true, emitHandles)
			case spkDelegated:
				return cdDelSlice{Tgt: c.tgt, TKey: StaticKey(fi.TomlKey), TDottedKey: c.tkey, ImportPath: fi.ImportPath, TypeName: fi.TypeName, SlicePtr: true, IdxVar: arrayIdxVar(c.arrayDepth)}
			}
		}

	case spkMap:
		switch elem := te.Elem.(type) {
		case spkScalar:
			return cdMapScalar{Tgt: c.tgt, TKey: c.tkey}
		case spkMap:
			return cdMapMap{Tgt: c.tgt, TKey: c.tkey, TypeName: fi.TypeName, ImportPath: fi.ImportPath}
		case spkStruct:
			return compDecodeMapStruct(fi, c, false)
		case spkDelegated:
			return cdDelMap{Tgt: c.tgt, TKey: c.tkey, ImportPath: fi.ImportPath, ElemType: fi.ElemType}
		case spkPtr:
			if _, ok := elem.Elem.(spkStruct); ok {
				return compDecodeMapStruct(fi, c, true)
			}
		}

	case spkStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		return cdInTable{TKey: c.tkey, Children: foldCompDecode(fi.InnerInfo, c, false)}

	case spkDelegated:
		return cdDelStruct{Tgt: c.tgt, TKey: c.tkey, ImportPath: fi.ImportPath, TypeName: fi.TypeName, Ptr: false}
	}
	return nil
}

func compDecodeNilGuard(fi FieldInfo, c compPos) cdNode {
	if fi.InnerInfo == nil {
		return nil
	}
	// A process-unique local (not key-derived): nested pointer-structs whose
	// fields share a GoName must get distinct locals, and the #55 flat fallback
	// strips the key path, so key-based names collide. See the fuzzer regression.
	localVar := c.nextLocal(toLowerFirst(fi.GoName) + "Val")
	localTgt := LocalTarget(localVar)
	// Children: all inner fields decoded inside the explicit [table].
	children := foldCompDecode(fi.InnerInfo, compPos{tkey: c.tkey, tgt: localTgt, arrayDepth: c.arrayDepth, seq: c.seq}, false)
	// FlatChildren (#55): inner fields decoded at the parent container. Array-table
	// sub-fields keep their dotted keys (matched from the document root); other
	// fields use bare keys at the current container.
	var flat []cdNode
	for _, inner := range fi.InnerInfo.Fields {
		var n cdNode
		if inner.Kind == FieldSliceStruct || inner.Kind == FieldSliceDelegatedStruct {
			n = foldCompDecodeField(inner, compPos{tkey: c.tkey, tgt: localTgt, arrayDepth: c.arrayDepth, seq: c.seq}, false)
		} else {
			n = foldCompDecodeField(inner, compPos{tkey: TOMLKey{}, tgt: localTgt, arrayDepth: c.arrayDepth, seq: c.seq}, false)
		}
		if n != nil {
			flat = append(flat, n)
		}
	}
	return cdNilGuard{Tgt: c.tgt, TypeName: fi.TypeName, TKey: c.tkey, LocalVar: localVar, Children: children, FlatChildren: flat}
}

func compDecodeArrayTable(fi FieldInfo, c compPos, slicePtr, emitHandles bool) cdNode {
	if fi.InnerInfo == nil {
		return nil
	}
	crossPkg := strings.Contains(fi.TypeName, ".")
	iv := arrayIdxVar(c.arrayDepth)
	return cdArrayTable{
		Tgt:          c.tgt,
		TypeName:     fi.TypeName,
		ImportPath:   fi.ImportPath,
		TKey:         StaticKey(fi.TomlKey),
		TDottedKey:   c.tkey,
		SlicePtr:     slicePtr,
		TrackHandles: emitHandles && !crossPkg,
		IdxVar:       iv,
		EntryVar:     arrayEntryVar(c.arrayDepth),
		Children:     foldCompDecode(fi.InnerInfo, compPos{tkey: c.tkey, tgt: c.tgt.Index(iv), arrayDepth: c.arrayDepth + 1, seq: c.seq}, false),
	}
}

func compDecodeMapStruct(fi FieldInfo, c compPos, slicePtr bool) cdNode {
	if fi.InnerInfo == nil {
		return nil
	}
	// Seed inner positions with the runtime map-key variable spliced into the
	// dotted path, so inner leaf consumed keys build "<field>.<mapVar>.<inner>"
	// directly. mapVar is unique per nesting level so a nested map-struct doesn't
	// shadow the outer key its consumed marks still reference through TKey.
	mapVar := c.nextLocal("_mk")
	entryPos := compPos{tkey: c.tkey.Lit(".").Var(mapVar), tgt: LocalTarget("entry"), arrayDepth: c.arrayDepth, seq: c.seq}
	return cdMapStruct{
		Tgt:      c.tgt,
		TKey:     c.tkey,
		TypeName: fi.TypeName,
		SlicePtr: slicePtr,
		MapVar:   mapVar,
		Children: foldCompDecode(fi.InnerInfo, entryPos, false),
	}
}

// --- Encode fold ---

func foldCompEncode(si *StructInfo, pos compPos, emitHandles bool) []ceNode {
	var out []ceNode
	for _, fi := range si.Fields {
		if n := foldCompEncodeField(fi, pos, emitHandles); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func foldCompEncodeField(fi FieldInfo, pos compPos, emitHandles bool) ceNode {
	c := pos.child(fi.TomlKey, fi.GoName)

	switch te := fieldType(fi).(type) {
	case spkScalar:
		switch te.Codec {
		case codecPrim:
			return ceLeaf{Kind: ceLeafPrim, Tgt: c.tgt, TKey: c.tkey, TypeName: fi.TypeName, ElemType: fi.ElemType, ImportPath: fi.ImportPath, OmitEmpty: fi.OmitEmpty, Multiline: fi.Multiline}
		case codecCustom:
			return ceLeaf{Kind: ceLeafCustom, Tgt: c.tgt, TKey: c.tkey}
		case codecText:
			return ceLeaf{Kind: ceLeafText, Tgt: c.tgt, TKey: c.tkey, OmitEmpty: fi.OmitEmpty}
		}

	case spkPtr:
		switch te.Elem.(type) {
		case spkScalar:
			return ceLeaf{Kind: ceLeafPtrPrim, Tgt: c.tgt, TKey: c.tkey, TypeName: fi.TypeName}
		case spkStruct:
			if fi.InnerInfo == nil {
				return nil
			}
			return ceNilGuard{Tgt: c.tgt, TKey: c.tkey, TypeName: fi.TypeName, Children: foldCompEncode(fi.InnerInfo, c, false)}
		case spkDelegated:
			return ceDelStruct{Tgt: c.tgt, TKey: c.tkey, ImportPath: fi.ImportPath, TypeName: fi.TypeName, Ptr: true}
		}

	case spkSlice:
		switch elem := te.Elem.(type) {
		case spkScalar:
			if elem.Codec == codecText {
				return ceLeaf{Kind: ceLeafSliceText, Tgt: c.tgt, TKey: c.tkey, TypeName: fi.TypeName, ImportPath: fi.ImportPath, OmitEmpty: fi.OmitEmpty}
			}
			return ceLeaf{Kind: ceLeafSlicePrim, Tgt: c.tgt, TKey: c.tkey, ElemType: fi.ElemType, TypeName: fi.TypeName, ImportPath: fi.ImportPath, SlicePointer: fi.SlicePointer, OmitEmpty: fi.OmitEmpty}
		case spkStruct:
			return compEncodeArrayTable(fi, c, false, emitHandles)
		case spkDelegated:
			return ceDelSlice{Tgt: c.tgt, TKey: StaticKey(fi.TomlKey), TDottedKey: c.tkey, ImportPath: fi.ImportPath, TypeName: fi.TypeName, SlicePtr: false, IdxVar: arrayIdxVar(c.arrayDepth), Scoped: c.scoped}
		case spkPtr:
			switch elem.Elem.(type) {
			case spkScalar:
				// []*prim — see the decode fold's matching note.
				return ceLeaf{Kind: ceLeafSlicePrim, Tgt: c.tgt, TKey: c.tkey, ElemType: fi.ElemType, TypeName: fi.TypeName, ImportPath: fi.ImportPath, SlicePointer: true, OmitEmpty: fi.OmitEmpty}
			case spkStruct:
				return compEncodeArrayTable(fi, c, true, emitHandles)
			case spkDelegated:
				return ceDelSlice{Tgt: c.tgt, TKey: StaticKey(fi.TomlKey), TDottedKey: c.tkey, ImportPath: fi.ImportPath, TypeName: fi.TypeName, SlicePtr: true, IdxVar: arrayIdxVar(c.arrayDepth), Scoped: c.scoped}
			}
		}

	case spkMap:
		switch elem := te.Elem.(type) {
		case spkScalar:
			return ceMapScalar{Tgt: c.tgt, TKey: c.tkey}
		case spkMap:
			return ceMapMap{Tgt: c.tgt, TKey: c.tkey, TypeName: fi.TypeName}
		case spkStruct:
			return compEncodeMapStruct(fi, c, false)
		case spkDelegated:
			return ceDelMap{Tgt: c.tgt, TKey: c.tkey, ImportPath: fi.ImportPath, ElemType: fi.ElemType}
		case spkPtr:
			if _, ok := elem.Elem.(spkStruct); ok {
				return compEncodeMapStruct(fi, c, true)
			}
		}

	case spkStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		return ceTable{TKey: c.tkey, Children: foldCompEncode(fi.InnerInfo, c, false)}

	case spkDelegated:
		return ceDelStruct{Tgt: c.tgt, TKey: c.tkey, ImportPath: fi.ImportPath, TypeName: fi.TypeName, Ptr: false}
	}
	return nil
}

func compEncodeArrayTable(fi FieldInfo, c compPos, slicePtr, emitHandles bool) ceNode {
	if fi.InnerInfo == nil {
		return nil
	}
	crossPkg := strings.Contains(fi.TypeName, ".")
	iv := arrayIdxVar(c.arrayDepth)
	return ceArrayTable{
		Tgt:          c.tgt,
		TKey:         StaticKey(fi.TomlKey),
		TDottedKey:   c.tkey,
		TypeName:     fi.TypeName,
		ImportPath:   fi.ImportPath,
		SlicePtr:     slicePtr,
		TrackHandles: emitHandles && !crossPkg,
		IdxVar:       iv,
		Scoped:       c.scoped,
		Children:     foldCompEncode(fi.InnerInfo, compPos{tkey: c.tkey, tgt: c.tgt.Index(iv), arrayDepth: c.arrayDepth + 1, scoped: true}, false),
	}
}

func compEncodeMapStruct(fi FieldInfo, c compPos, slicePtr bool) ceNode {
	if fi.InnerInfo == nil {
		return nil
	}
	entryTgt := LocalTarget("mapVal")
	if slicePtr {
		entryTgt = LocalTarget("(*mapVal)")
	}
	return ceMapStruct{
		Tgt:      c.tgt,
		TKey:     c.tkey,
		TypeName: fi.TypeName,
		SlicePtr: slicePtr,
		Children: foldCompEncode(fi.InnerInfo, compPos{tkey: c.tkey, tgt: entryTgt, arrayDepth: c.arrayDepth, scoped: true}, false),
	}
}
