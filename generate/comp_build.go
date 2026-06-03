package generate

import (
	"fmt"
	"strconv"
	"strings"
)

// Compositional folds (#84, #85). foldCompDecode/foldCompEncode build the cd*/ce*
// node trees from a StructInfo by recursing over each field's TypeExpr
// (FieldInfo.Type), reading the leaf payload (names, imports, inner StructInfo)
// directly off the spkType nodes, and threading the TOML position (compPos) plus
// the emitHandles flag.
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
	// siblingKeys are the TOML keys of this struct's own fields. A child's
	// flat-key fallback (#55) must not claim a key owned by one of these
	// siblings, or it would false-match and wrongly materialize the child (#100).
	siblingKeys := make(map[string]bool, len(si.Fields))
	for _, fi := range si.Fields {
		siblingKeys[fi.TomlKey] = true
	}
	var out []cdNode
	for _, fi := range si.Fields {
		if n := foldCompDecodeField(fi, pos, emitHandles, siblingKeys); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func foldCompDecodeField(fi FieldInfo, pos compPos, emitHandles bool, siblingKeys map[string]bool) cdNode {
	c := pos.child(fi.TomlKey, fi.GoName)

	switch te := fi.Type.(type) {
	case spkScalar:
		switch te.Codec {
		case codecPrim:
			return cdLeaf{Kind: cdLeafPrim, Tgt: c.tgt, TKey: c.tkey, TypeName: te.TypeName, ElemType: te.ElemType, ImportPath: te.ImportPath}
		case codecCustom:
			return cdLeaf{Kind: cdLeafCustom, Tgt: c.tgt, TKey: c.tkey}
		case codecText:
			return cdLeaf{Kind: cdLeafText, Tgt: c.tgt, TKey: c.tkey}
		}

	case spkPtr:
		switch inner := te.Elem.(type) {
		case spkScalar:
			return cdLeaf{Kind: cdLeafPrim, Tgt: c.tgt, TKey: c.tkey, TypeName: inner.TypeName, Pointer: true}
		case spkStruct:
			return compDecodeNilGuard(fi, inner, c, siblingKeys)
		case spkDelegated:
			return cdDelStruct{Tgt: c.tgt, TKey: c.tkey, ImportPath: inner.ImportPath, TypeName: inner.TypeName, Ptr: true}
		}

	case spkSlice:
		switch elem := te.Elem.(type) {
		case spkScalar:
			if elem.Codec == codecText {
				return cdLeaf{Kind: cdLeafSliceText, Tgt: c.tgt, TKey: c.tkey, TypeName: elem.TypeName, ImportPath: elem.ImportPath}
			}
			return cdLeaf{Kind: cdLeafSlicePrim, Tgt: c.tgt, TKey: c.tkey, ElemType: elem.TypeName, TypeName: te.TypeName, ImportPath: te.ImportPath, SlicePointer: false}
		case spkStruct:
			return compDecodeArrayTable(fi, elem, c, false, emitHandles)
		case spkDelegated:
			return cdDelSlice{Tgt: c.tgt, TKey: StaticKey(fi.TomlKey), TDottedKey: c.tkey, ImportPath: elem.ImportPath, TypeName: elem.TypeName, SlicePtr: false, IdxVar: arrayIdxVar(c.arrayDepth)}
		case spkPtr:
			switch pe := elem.Elem.(type) {
			case spkScalar:
				// []*prim: the pointer is the structural Slice(Ptr(Scalar)), so the
				// element type is on the inner scalar.
				return cdLeaf{Kind: cdLeafSlicePrim, Tgt: c.tgt, TKey: c.tkey, ElemType: pe.TypeName, TypeName: te.TypeName, ImportPath: te.ImportPath, SlicePointer: true}
			case spkStruct:
				return compDecodeArrayTable(fi, pe, c, true, emitHandles)
			case spkDelegated:
				return cdDelSlice{Tgt: c.tgt, TKey: StaticKey(fi.TomlKey), TDottedKey: c.tkey, ImportPath: pe.ImportPath, TypeName: pe.TypeName, SlicePtr: true, IdxVar: arrayIdxVar(c.arrayDepth)}
			}
		}

	case spkMap:
		switch elem := te.Elem.(type) {
		case spkScalar:
			return cdMapScalar{Tgt: c.tgt, TKey: c.tkey}
		case spkMap:
			return cdMapMap{Tgt: c.tgt, TKey: c.tkey, TypeName: te.TypeName, ImportPath: te.ImportPath}
		case spkStruct:
			return compDecodeMapStruct(fi, elem, c, false)
		case spkDelegated:
			importPath := te.ImportPath
			if importPath == "" {
				importPath = elem.ImportPath
			}
			return cdDelMap{Tgt: c.tgt, TKey: c.tkey, ImportPath: importPath, ElemType: elem.TypeName, MapVar: c.nextLocal("_mk"), EntryVar: c.nextLocal("entry")}
		case spkPtr:
			if ps, ok := elem.Elem.(spkStruct); ok {
				return compDecodeMapStruct(fi, ps, c, true)
			}
		}

	case spkStruct:
		if te.InnerInfo == nil {
			return nil
		}
		return cdInTable{
			TKey:         c.tkey,
			Children:     foldCompDecode(te.InnerInfo, c, false),
			FlatChildren: compDecodeFlatChildren(te.InnerInfo, c, c.tgt, siblingKeys),
		}

	case spkDelegated:
		return cdDelStruct{Tgt: c.tgt, TKey: c.tkey, ImportPath: te.ImportPath, TypeName: te.TypeName, Ptr: false}
	}
	return nil
}

// compDecodeFlatChildren builds the flat-key fallback decode for a struct's
// inner fields, decoded at the parent container when the struct's own [table]
// header is absent (#55, #89): array-table sub-fields keep their full dotted key
// (matched document-root-relative), every other field uses a bare key at the
// current container. tgt is where the decoded values land (a nil-guard local for
// pointer structs, the field itself for value structs).
//
// siblingKeys are the TOML keys of the *parent* struct's fields. An inner field
// whose bare key collides with one is omitted from the fallback: that key at the
// parent belongs to the sibling (TOML can't have it twice), so a flat scan must
// not claim it — doing so false-matches and wrongly materializes this struct
// (#100). Such a field is only decodable via the explicit [table].
func compDecodeFlatChildren(inner *StructInfo, c compPos, tgt TargetPath, siblingKeys map[string]bool) []cdNode {
	var flat []cdNode
	for _, f := range inner.Fields {
		if !flatDecodable(f.Type) {
			continue
		}
		if !isSliceOfStruct(f.Type) && siblingKeys[f.TomlKey] {
			continue
		}
		pos := compPos{tgt: tgt, arrayDepth: c.arrayDepth, seq: c.seq}
		if isSliceOfStruct(f.Type) {
			pos.tkey = c.tkey
		}
		if n := foldCompDecodeField(f, pos, false, nil); n != nil {
			flat = append(flat, n)
		}
	}
	return flat
}

// flatDecodable reports whether an inner field has a meaningful flat
// representation at the parent container when its struct's [table] header is
// absent (#55/#89). Only fields that read from the container directly qualify:
//
//   - scalars and *scalars — a bare `key = value` in the parent scope;
//   - any slice — a primitive slice is a bare `key = [...]` (sibling-guarded by
//     #100); an array-table keeps its specific dotted key (`parent.field`), which
//     cannot collide with an unrelated table.
//
// Fields that resolve via a document-root table scan are NOT flat-decodable:
// maps (`[field]` / `[field.k]`), nested value/pointer structs and delegated
// structs (`[field]`). In a flat fallback they scan the whole document and can
// false-match a root- or grandparent-level table, wrongly materializing a
// pointer that should be nil (#101). Such a field is only decodable via the
// struct's explicit [table], which is unambiguous.
func flatDecodable(te spkType) bool {
	switch t := te.(type) {
	case spkScalar, spkSlice:
		return true
	case spkPtr:
		_, scalar := t.Elem.(spkScalar)
		return scalar
	default:
		// spkMap, spkStruct, spkDelegated: document-root table scans.
		return false
	}
}

// isSliceOfStruct reports whether te is a slice whose element (possibly behind a
// pointer) is a struct or delegated struct — i.e. an array-table field. The #55
// flat fallback keeps such fields' dotted keys (matched from the document root)
// while bare-keying the rest.
func isSliceOfStruct(te spkType) bool {
	s, ok := te.(spkSlice)
	if !ok {
		return false
	}
	elem := s.Elem
	if p, ok := elem.(spkPtr); ok {
		elem = p.Elem
	}
	switch elem.(type) {
	case spkStruct, spkDelegated:
		return true
	}
	return false
}

func compDecodeNilGuard(fi FieldInfo, st spkStruct, c compPos, siblingKeys map[string]bool) cdNode {
	if st.InnerInfo == nil {
		return nil
	}
	// A process-unique local (not key-derived): nested pointer-structs whose
	// fields share a GoName must get distinct locals, and the #55 flat fallback
	// strips the key path, so key-based names collide. See the fuzzer regression.
	localVar := c.nextLocal(toLowerFirst(fi.GoName) + "Val")
	localTgt := LocalTarget(localVar)
	// Children: all inner fields decoded inside the explicit [table].
	children := foldCompDecode(st.InnerInfo, compPos{tkey: c.tkey, tgt: localTgt, arrayDepth: c.arrayDepth, seq: c.seq}, false)
	// FlatChildren (#55): inner fields decoded at the parent container when the
	// [table] header is absent; sibling-owned keys are excluded (#100).
	flat := compDecodeFlatChildren(st.InnerInfo, c, localTgt, siblingKeys)
	return cdNilGuard{Tgt: c.tgt, TypeName: st.TypeName, TKey: c.tkey, LocalVar: localVar, Children: children, FlatChildren: flat}
}

func compDecodeArrayTable(fi FieldInfo, st spkStruct, c compPos, slicePtr, emitHandles bool) cdNode {
	if st.InnerInfo == nil {
		return nil
	}
	crossPkg := strings.Contains(st.TypeName, ".")
	iv := arrayIdxVar(c.arrayDepth)
	return cdArrayTable{
		Tgt:          c.tgt,
		TypeName:     st.TypeName,
		ImportPath:   st.ImportPath,
		TKey:         StaticKey(fi.TomlKey),
		TDottedKey:   c.tkey,
		SlicePtr:     slicePtr,
		TrackHandles: emitHandles && !crossPkg,
		IdxVar:       iv,
		EntryVar:     arrayEntryVar(c.arrayDepth),
		Children:     foldCompDecode(st.InnerInfo, compPos{tkey: c.tkey, tgt: c.tgt.Index(iv), arrayDepth: c.arrayDepth + 1, seq: c.seq}, false),
	}
}

func compDecodeMapStruct(fi FieldInfo, st spkStruct, c compPos, slicePtr bool) cdNode {
	if st.InnerInfo == nil {
		return nil
	}
	// Seed inner positions with the runtime map-key variable spliced into the
	// dotted path, so inner leaf consumed keys build "<field>.<mapVar>.<inner>"
	// directly. mapVar is unique per nesting level so a nested map-struct doesn't
	// shadow the outer key its consumed marks still reference through TKey.
	mapVar := c.nextLocal("_mk")
	entryVar := c.nextLocal("entry")
	entryPos := compPos{tkey: c.tkey.Lit(".").Var(mapVar), tgt: LocalTarget(entryVar), arrayDepth: c.arrayDepth, seq: c.seq}
	return cdMapStruct{
		Tgt:      c.tgt,
		TKey:     c.tkey,
		TypeName: st.TypeName,
		SlicePtr: slicePtr,
		MapVar:   mapVar,
		EntryVar: entryVar,
		Children: foldCompDecode(st.InnerInfo, entryPos, false),
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

	switch te := fi.Type.(type) {
	case spkScalar:
		switch te.Codec {
		case codecPrim:
			return ceLeaf{Kind: ceLeafPrim, Tgt: c.tgt, TKey: c.tkey, TypeName: te.TypeName, ElemType: te.ElemType, ImportPath: te.ImportPath, OmitEmpty: fi.OmitEmpty, Multiline: fi.Multiline}
		case codecCustom:
			return ceLeaf{Kind: ceLeafCustom, Tgt: c.tgt, TKey: c.tkey}
		case codecText:
			return ceLeaf{Kind: ceLeafText, Tgt: c.tgt, TKey: c.tkey, OmitEmpty: fi.OmitEmpty}
		}

	case spkPtr:
		switch inner := te.Elem.(type) {
		case spkScalar:
			return ceLeaf{Kind: ceLeafPtrPrim, Tgt: c.tgt, TKey: c.tkey, TypeName: inner.TypeName}
		case spkStruct:
			if inner.InnerInfo == nil {
				return nil
			}
			return ceNilGuard{Tgt: c.tgt, TKey: c.tkey, TypeName: inner.TypeName, Children: foldCompEncode(inner.InnerInfo, c, false)}
		case spkDelegated:
			return ceDelStruct{Tgt: c.tgt, TKey: c.tkey, ImportPath: inner.ImportPath, TypeName: inner.TypeName, Ptr: true}
		}

	case spkSlice:
		switch elem := te.Elem.(type) {
		case spkScalar:
			if elem.Codec == codecText {
				return ceLeaf{Kind: ceLeafSliceText, Tgt: c.tgt, TKey: c.tkey, TypeName: elem.TypeName, ImportPath: elem.ImportPath, OmitEmpty: fi.OmitEmpty}
			}
			return ceLeaf{Kind: ceLeafSlicePrim, Tgt: c.tgt, TKey: c.tkey, ElemType: elem.TypeName, TypeName: te.TypeName, ImportPath: te.ImportPath, SlicePointer: false, OmitEmpty: fi.OmitEmpty}
		case spkStruct:
			return compEncodeArrayTable(fi, elem, c, false, emitHandles)
		case spkDelegated:
			return ceDelSlice{Tgt: c.tgt, TKey: StaticKey(fi.TomlKey), TDottedKey: c.tkey, ImportPath: elem.ImportPath, TypeName: elem.TypeName, SlicePtr: false, IdxVar: arrayIdxVar(c.arrayDepth), Scoped: c.scoped}
		case spkPtr:
			switch pe := elem.Elem.(type) {
			case spkScalar:
				// []*prim — see the decode fold's matching note.
				return ceLeaf{Kind: ceLeafSlicePrim, Tgt: c.tgt, TKey: c.tkey, ElemType: pe.TypeName, TypeName: te.TypeName, ImportPath: te.ImportPath, SlicePointer: true, OmitEmpty: fi.OmitEmpty}
			case spkStruct:
				return compEncodeArrayTable(fi, pe, c, true, emitHandles)
			case spkDelegated:
				return ceDelSlice{Tgt: c.tgt, TKey: StaticKey(fi.TomlKey), TDottedKey: c.tkey, ImportPath: pe.ImportPath, TypeName: pe.TypeName, SlicePtr: true, IdxVar: arrayIdxVar(c.arrayDepth), Scoped: c.scoped}
			}
		}

	case spkMap:
		switch elem := te.Elem.(type) {
		case spkScalar:
			return ceMapScalar{Tgt: c.tgt, TKey: c.tkey}
		case spkMap:
			return ceMapMap{Tgt: c.tgt, TKey: c.tkey, TypeName: te.TypeName}
		case spkStruct:
			return compEncodeMapStruct(fi, elem, c, false)
		case spkDelegated:
			importPath := te.ImportPath
			if importPath == "" {
				importPath = elem.ImportPath
			}
			return ceDelMap{Tgt: c.tgt, TKey: c.tkey, ImportPath: importPath, ElemType: elem.TypeName}
		case spkPtr:
			if ps, ok := elem.Elem.(spkStruct); ok {
				return compEncodeMapStruct(fi, ps, c, true)
			}
		}

	case spkStruct:
		if te.InnerInfo == nil {
			return nil
		}
		return ceTable{TKey: c.tkey, Children: foldCompEncode(te.InnerInfo, c, false)}

	case spkDelegated:
		return ceDelStruct{Tgt: c.tgt, TKey: c.tkey, ImportPath: te.ImportPath, TypeName: te.TypeName, Ptr: false}
	}
	return nil
}

func compEncodeArrayTable(fi FieldInfo, st spkStruct, c compPos, slicePtr, emitHandles bool) ceNode {
	if st.InnerInfo == nil {
		return nil
	}
	crossPkg := strings.Contains(st.TypeName, ".")
	iv := arrayIdxVar(c.arrayDepth)
	return ceArrayTable{
		Tgt:          c.tgt,
		TKey:         StaticKey(fi.TomlKey),
		TDottedKey:   c.tkey,
		TypeName:     st.TypeName,
		ImportPath:   st.ImportPath,
		SlicePtr:     slicePtr,
		TrackHandles: emitHandles && !crossPkg,
		IdxVar:       iv,
		Scoped:       c.scoped,
		Children:     foldCompEncode(st.InnerInfo, compPos{tkey: c.tkey, tgt: c.tgt.Index(iv), arrayDepth: c.arrayDepth + 1, scoped: true}, false),
	}
}

func compEncodeMapStruct(fi FieldInfo, st spkStruct, c compPos, slicePtr bool) ceNode {
	if st.InnerInfo == nil {
		return nil
	}
	entryTgt := LocalTarget("mapVal")
	if slicePtr {
		entryTgt = LocalTarget("(*mapVal)")
	}
	return ceMapStruct{
		Tgt:      c.tgt,
		TKey:     c.tkey,
		TypeName: st.TypeName,
		SlicePtr: slicePtr,
		Children: foldCompEncode(st.InnerInfo, compPos{tkey: c.tkey, tgt: entryTgt, arrayDepth: c.arrayDepth, scoped: true}, false),
	}
}
