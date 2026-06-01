package generate

// Compositional-fold equivalence harness
// (ADR docs/decisions/2026-06-01-compositional-codegen.md).
//
// Pressure-tests "composition over enumeration": a recursive TypeExpr folded
// into the EXISTING DecodeOp/EncodeOp trees, driving dispatch by the type's
// recursive structure instead of the flat FieldKind switch in
// buildDecodeOp/buildEncodeOp.
//
// Algebra (the ADR's ~6 constructors): Scalar{codec}, Ptr, Slice, Map, Struct,
// Delegated. Custom/TextMarshaler are Scalar CODECS, not constructors;
// Delegated is an opaque cross-package leaf. All 16 FieldKinds are covered as
// compositions, e.g.:
//
//   []*Struct          = Slice(Ptr(Struct))      SlicePointer DERIVED, not read
//   *T                 = Ptr(Scalar)             pointer is a wrapper property
//   map[string]*S      = Map(Ptr(Struct))
//   map[string]NamedM  = Map(Map(Scalar))
//   []Delegated        = Slice(Delegated)
//
// Equivalence is asserted two ways for BOTH directions: reflect.DeepEqual on
// the op trees AND a rendered-Go comparison through the real
// jenDecodeOps/jenEncodeOps.
//
// SCOPE / HONESTY: this spike reproduces the *current* ops, so it validates the
// classify+build front-end ONLY across the full type surface. It does NOT yet
// demonstrate the IR shrink (ADR option B) — that changes the ops and defeats a
// byte-diff. Cross-package fixtures use fake package paths; output is
// string-compared, NOT compiled (compile-correctness for the current ops is
// already covered by the integration suite, which the fold inherits via
// fold == builder). Kept as the ADR's promotion gate and the equivalence net
// guarding the option-B IR migration (#2); it should pass unchanged until the
// builder it mirrors is replaced.

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	jen "github.com/dave/jennifer/jen"
)

// The compositional algebra (spkType/spkScalar/.../fieldType) now lives in the
// production file typeexpr.go; this harness consumes it.

// =========================================================================
// Decode fold
// =========================================================================

func foldDecodeOps(si StructInfo, dataPath, keyPrefix string, tgt TargetPath, tkey TOMLKey, isRoot, emitHandles bool) []DecodeOp {
	var ops []DecodeOp
	for _, fi := range si.Fields {
		ops = append(ops, foldDecodeField(fi, dataPath, keyPrefix, tgt, tkey, isRoot, emitHandles))
	}
	return ops
}

func foldDecodeField(fi FieldInfo, dataPath, keyPrefix string, tgt TargetPath, tkey TOMLKey, isRoot, emitHandles bool) DecodeOp {
	te := fieldType(fi)
	target := dataPath + "." + fi.GoName
	fieldTgt := tgt.Dot(fi.GoName)
	fieldKey := tkey.Dot(fi.TomlKey)
	key := keyPrefix + fi.TomlKey

	switch t := te.(type) {
	case spkScalar:
		switch t.Codec {
		case codecPrim:
			return GetPrimitive{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ElemType: fi.ElemType, ImportPath: fi.ImportPath}
		case codecCustom:
			return GetCustom{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath}
		case codecText:
			return GetTextMarshaler{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath}
		}

	case spkPtr:
		switch t.Elem.(type) {
		case spkScalar:
			return GetPrimitive{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, Pointer: true}
		case spkStruct:
			return foldDecodePointerStruct(fi, dataPath, keyPrefix, tgt, tkey)
		case spkDelegated:
			return DelegateStruct{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath, Pointer: true, UseRootAPI: isRoot}
		}

	case spkSlice:
		switch elem := t.Elem.(type) {
		case spkScalar:
			if elem.Codec == codecText {
				return GetSliceTextMarshaler{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath}
			}
			return foldDecodeSlicePrimitive(fi, target, key, fieldTgt, fieldKey, false)
		case spkPtr:
			switch elem.Elem.(type) {
			case spkScalar:
				return foldDecodeSlicePrimitive(fi, target, key, fieldTgt, fieldKey, true)
			case spkStruct:
				return foldDecodeArrayTable(fi, dataPath, keyPrefix, tgt, tkey, emitHandles, true)
			case spkDelegated:
				return foldDecodeDelegateSlice(fi, target, keyPrefix, fieldTgt, fieldKey, true)
			}
		case spkStruct:
			return foldDecodeArrayTable(fi, dataPath, keyPrefix, tgt, tkey, emitHandles, false)
		case spkDelegated:
			return foldDecodeDelegateSlice(fi, target, keyPrefix, fieldTgt, fieldKey, false)
		}

	case spkMap:
		switch t.Elem.(type) {
		case spkScalar:
			return GetMapStringString{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, UseRootAPI: isRoot}
		case spkMap:
			return GetMapStringMapStringString{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath}
		case spkStruct, spkPtr:
			return foldDecodeMapStruct(fi, dataPath, keyPrefix, tgt, tkey, isRoot)
		case spkDelegated:
			return DelegateMap{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, ElemType: fi.ElemType, ImportPath: fi.ImportPath, UseRootAPI: isRoot}
		}

	case spkStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		return InTable{
			Key: key, TKey: fieldKey, UseRootAPI: isRoot,
			Fields: foldDecodeOps(*fi.InnerInfo, target, keyPrefix+fi.TomlKey+".", fieldTgt, fieldKey.Lit("."), false, false),
		}

	case spkDelegated:
		return DelegateStruct{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath, UseRootAPI: isRoot}
	}
	panic("spike: unreachable decode combination")
}

func foldDecodeSlicePrimitive(fi FieldInfo, target, key string, fieldTgt TargetPath, fieldKey TOMLKey, slicePtr bool) DecodeOp {
	return GetSlicePrimitive{Target: target, Key: key, Tgt: fieldTgt, TKey: fieldKey, ElemType: fi.ElemType, TypeName: fi.TypeName, ImportPath: fi.ImportPath, SlicePointer: slicePtr}
}

func foldDecodeDelegateSlice(fi FieldInfo, target, keyPrefix string, fieldTgt TargetPath, fieldKey TOMLKey, slicePtr bool) DecodeOp {
	return DelegateSlice{Target: target, Key: fi.TomlKey, DottedKey: keyPrefix + fi.TomlKey, Tgt: fieldTgt, TKey: StaticKey(fi.TomlKey), TDottedKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath, SlicePointer: slicePtr}
}

func foldDecodeArrayTable(fi FieldInfo, dataPath, keyPrefix string, tgt TargetPath, tkey TOMLKey, emitHandles, slicePtr bool) DecodeOp {
	if fi.InnerInfo == nil {
		return nil
	}
	target := dataPath + "." + fi.GoName
	fieldTgt := tgt.Dot(fi.GoName)
	fieldKey := tkey.Dot(fi.TomlKey)
	crossPkg := strings.Contains(fi.TypeName, ".")
	return ForArrayTable{
		Key: fi.TomlKey, DottedKey: keyPrefix + fi.TomlKey, TKey: StaticKey(fi.TomlKey), TDottedKey: fieldKey,
		TypeName: fi.TypeName, ImportPath: fi.ImportPath, Target: target, Tgt: fieldTgt,
		SlicePointer: slicePtr, TrackHandles: emitHandles && !crossPkg,
		Fields: foldDecodeOps(*fi.InnerInfo, target+"[i]", keyPrefix+fi.TomlKey+".", fieldTgt.Index("i"), fieldKey.Lit("."), false, false),
	}
}

func foldDecodeMapStruct(fi FieldInfo, dataPath, keyPrefix string, tgt TargetPath, tkey TOMLKey, isRoot bool) DecodeOp {
	if fi.InnerInfo == nil {
		return nil
	}
	target := dataPath + "." + fi.GoName
	fieldTgt := tgt.Dot(fi.GoName)
	fieldKey := tkey.Dot(fi.TomlKey)
	return ForMapStringStruct{
		Key: keyPrefix + fi.TomlKey, TKey: fieldKey, TypeName: fi.TypeName, Target: target, Tgt: fieldTgt,
		SlicePointer: fi.SlicePointer, UseRootAPI: isRoot,
		Fields: foldDecodeOps(*fi.InnerInfo, "entry", keyPrefix+fi.TomlKey+".", LocalTarget("entry"), fieldKey.Lit("."), false, false),
	}
}

func foldDecodePointerStruct(fi FieldInfo, dataPath, keyPrefix string, tgt TargetPath, tkey TOMLKey) DecodeOp {
	if fi.InnerInfo == nil {
		return nil
	}
	target := dataPath + "." + fi.GoName
	fieldTgt := tgt.Dot(fi.GoName)
	fieldKey := tkey.Dot(fi.TomlKey)
	innerPrefix := keyPrefix + fi.TomlKey + "."
	innerKey := fieldKey.Lit(".")
	localVar := toLowerFirst(fi.GoName) + "Val"
	localTgt := LocalTarget(localVar)
	tableFields := foldDecodeOps(*fi.InnerInfo, localVar, innerPrefix, localTgt, innerKey, false, false)
	var flatFields []DecodeOp
	for _, inner := range fi.InnerInfo.Fields {
		if isSliceOfStruct(fieldType(inner)) {
			flatFields = append(flatFields, foldDecodeField(inner, localVar, innerPrefix, localTgt, innerKey, false, false))
		} else {
			flatFields = append(flatFields, foldDecodeField(inner, localVar, "", localTgt, TOMLKey{}, false, false))
		}
	}
	return InPointerTable{Key: keyPrefix + fi.TomlKey, TKey: fieldKey, TypeName: fi.TypeName, Target: target, Tgt: fieldTgt, TableFields: tableFields, FlatFields: flatFields}
}

// isSliceOfStruct matches the buildDecodeOp predicate
// (inner.Kind == FieldSliceStruct || FieldSliceDelegatedStruct) structurally.
func isSliceOfStruct(te spkType) bool {
	s, ok := te.(spkSlice)
	if !ok {
		return false
	}
	switch e := s.Elem.(type) {
	case spkStruct, spkDelegated:
		return true
	case spkPtr:
		switch e.Elem.(type) {
		case spkStruct, spkDelegated:
			return true
		}
	}
	return false
}

// =========================================================================
// Encode fold
// =========================================================================

func foldEncodeOps(si StructInfo, tgt TargetPath, tkey TOMLKey, isRoot, emitHandles bool) []EncodeOp {
	var ops []EncodeOp
	for _, fi := range si.Fields {
		if op := foldEncodeField(fi, tgt, tkey, isRoot, emitHandles); op != nil {
			ops = append(ops, op)
		}
	}
	return ops
}

func foldEncodeField(fi FieldInfo, tgt TargetPath, tkey TOMLKey, isRoot, emitHandles bool) EncodeOp {
	te := fieldType(fi)
	fieldTgt := tgt.Dot(fi.GoName)
	fieldKey := tkey.Dot(fi.TomlKey)

	switch t := te.(type) {
	case spkScalar:
		switch t.Codec {
		case codecPrim:
			return SetPrimitive{Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ElemType: fi.ElemType, ImportPath: fi.ImportPath, OmitEmpty: fi.OmitEmpty, Multiline: fi.Multiline}
		case codecCustom:
			return SetCustom{Tgt: fieldTgt, TKey: fieldKey}
		case codecText:
			return SetTextMarshaler{Tgt: fieldTgt, TKey: fieldKey, OmitEmpty: fi.OmitEmpty}
		}

	case spkPtr:
		switch t.Elem.(type) {
		case spkScalar:
			return SetPointerPrimitive{Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName}
		case spkStruct:
			if fi.InnerInfo == nil {
				return nil
			}
			return InEncodePointerTable{Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, Fields: foldEncodeOps(*fi.InnerInfo, fieldTgt, fieldKey.Lit("."), false, false)}
		case spkDelegated:
			return EncodeDelegateStruct{Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath, Pointer: true, UseRootAPI: isRoot}
		}

	case spkSlice:
		switch elem := t.Elem.(type) {
		case spkScalar:
			if elem.Codec == codecText {
				return SetSliceTextMarshaler{Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath, OmitEmpty: fi.OmitEmpty}
			}
			return SetSlicePrimitive{Tgt: fieldTgt, TKey: fieldKey, ElemType: fi.ElemType, TypeName: fi.TypeName, ImportPath: fi.ImportPath, SlicePointer: false, OmitEmpty: fi.OmitEmpty}
		case spkPtr:
			switch elem.Elem.(type) {
			case spkScalar:
				return SetSlicePrimitive{Tgt: fieldTgt, TKey: fieldKey, ElemType: fi.ElemType, TypeName: fi.TypeName, ImportPath: fi.ImportPath, SlicePointer: true, OmitEmpty: fi.OmitEmpty}
			case spkStruct:
				return foldEncodeArrayTable(fi, fieldTgt, fieldKey, emitHandles, true)
			case spkDelegated:
				return EncodeDelegateSlice{Tgt: fieldTgt, TKey: StaticKey(fi.TomlKey), TDottedKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath, SlicePointer: true}
			}
		case spkStruct:
			return foldEncodeArrayTable(fi, fieldTgt, fieldKey, emitHandles, false)
		case spkDelegated:
			return EncodeDelegateSlice{Tgt: fieldTgt, TKey: StaticKey(fi.TomlKey), TDottedKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath, SlicePointer: false}
		}

	case spkMap:
		switch t.Elem.(type) {
		case spkScalar:
			return SetMapStringString{Tgt: fieldTgt, TKey: fieldKey, UseRootAPI: isRoot}
		case spkMap:
			return ForEncodeMapStringMapStringString{Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName}
		case spkStruct:
			return foldEncodeMapStruct(fi, fieldTgt, fieldKey, isRoot, false)
		case spkPtr:
			return foldEncodeMapStruct(fi, fieldTgt, fieldKey, isRoot, true)
		case spkDelegated:
			return EncodeDelegateMap{Tgt: fieldTgt, TKey: fieldKey, ElemType: fi.ElemType, ImportPath: fi.ImportPath, UseRootAPI: isRoot}
		}

	case spkStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		return InEncodeTable{TKey: fieldKey, UseRootAPI: isRoot, Fields: foldEncodeOps(*fi.InnerInfo, fieldTgt, fieldKey.Lit("."), false, false)}

	case spkDelegated:
		return EncodeDelegateStruct{Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath, UseRootAPI: isRoot}
	}
	panic("spike: unreachable encode combination")
}

func foldEncodeArrayTable(fi FieldInfo, fieldTgt TargetPath, fieldKey TOMLKey, emitHandles, slicePtr bool) EncodeOp {
	if fi.InnerInfo == nil {
		return nil
	}
	crossPkg := strings.Contains(fi.TypeName, ".")
	return ForEncodeArrayTable{
		Tgt: fieldTgt, TKey: StaticKey(fi.TomlKey), TDottedKey: fieldKey, TypeName: fi.TypeName, ImportPath: fi.ImportPath,
		SlicePointer: slicePtr, TrackHandles: emitHandles && !crossPkg,
		Fields: foldEncodeOps(*fi.InnerInfo, fieldTgt.Index("i"), fieldKey.Lit("."), false, false),
	}
}

func foldEncodeMapStruct(fi FieldInfo, fieldTgt TargetPath, fieldKey TOMLKey, isRoot, slicePtr bool) EncodeOp {
	if fi.InnerInfo == nil {
		return nil
	}
	entryTgt := LocalTarget("mapVal")
	if slicePtr {
		entryTgt = LocalTarget("(*mapVal)")
	}
	return ForEncodeMapStringStruct{
		Tgt: fieldTgt, TKey: fieldKey, TypeName: fi.TypeName, SlicePointer: slicePtr, UseRootAPI: isRoot,
		Fields: foldEncodeOps(*fi.InnerInfo, entryTgt, fieldKey.Lit("."), false, false),
	}
}

// =========================================================================
// Rendering through the REAL renderers
// =========================================================================

func spikeRenderDecode(t *testing.T, ops []DecodeOp) string {
	t.Helper()
	ctx := receiverJenCtx()
	cv := jen.Id("d").Dot("cstDoc").Dot("Root").Call()
	f := jen.NewFile("spike")
	f.Func().Id("decode").Params().BlockFunc(func(g *jen.Group) {
		for _, s := range jenDecodeOps(ctx, ops, cv, "") {
			g.Add(s)
		}
	})
	var b strings.Builder
	if err := f.Render(&b); err != nil {
		t.Fatalf("jen render (decode): %v", err)
	}
	return b.String()
}

func spikeRenderEncode(t *testing.T, ops []EncodeOp) string {
	t.Helper()
	ctx := receiverEncCtx()
	cv := jen.Id("d").Dot("cstDoc").Dot("Root").Call()
	f := jen.NewFile("spike")
	f.Func().Id("encode").Params().BlockFunc(func(g *jen.Group) {
		for _, s := range jenEncodeOps(ctx, ops, cv) {
			g.Add(s)
		}
	})
	var b strings.Builder
	if err := f.Render(&b); err != nil {
		t.Fatalf("jen render (encode): %v", err)
	}
	return b.String()
}

// =========================================================================
// Fixtures — covering all 16 FieldKinds as compositions
// =========================================================================

func spikeFixtures() []StructInfo {
	auth := &StructInfo{Name: "Auth", Fields: []FieldInfo{
		{GoName: "User", TomlKey: "user", Kind: FieldPrimitive, TypeName: "string"},
		{GoName: "Token", TomlKey: "token", Kind: FieldPrimitive, TypeName: "string"},
	}}
	deep := &StructInfo{Name: "Deep", Fields: []FieldInfo{
		{GoName: "Inner", TomlKey: "inner", Kind: FieldStruct, TypeName: "Auth", InnerInfo: auth},
	}}
	route := &StructInfo{Name: "Route", Fields: []FieldInfo{
		{GoName: "Path", TomlKey: "path", Kind: FieldPrimitive, TypeName: "string"},
		{GoName: "Methods", TomlKey: "methods", Kind: FieldSlicePrimitive, ElemType: "string"},
	}}
	tls := &StructInfo{Name: "TLS", Fields: []FieldInfo{
		{GoName: "Cert", TomlKey: "cert", Kind: FieldPrimitive, TypeName: "string"},
		{GoName: "Key", TomlKey: "key", Kind: FieldPrimitive, TypeName: "string"},
		// nested array-of-tables to exercise the InPointerTable flat-key branch
		{GoName: "Sans", TomlKey: "sans", Kind: FieldSliceStruct, TypeName: "Route", InnerInfo: route},
	}}
	svc := &StructInfo{Name: "Svc", Fields: []FieldInfo{
		{GoName: "Image", TomlKey: "image", Kind: FieldPrimitive, TypeName: "string"},
	}}

	return []StructInfo{
		// A: primitives + *int + []string + omitempty/multiline
		{Name: "Config", Fields: []FieldInfo{
			{GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
			{GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
			{GoName: "Debug", TomlKey: "debug", Kind: FieldPrimitive, TypeName: "bool", OmitEmpty: true},
			{GoName: "Doc", TomlKey: "doc", Kind: FieldPrimitive, TypeName: "string", Multiline: true},
			{GoName: "Retries", TomlKey: "retries", Kind: FieldPointerPrimitive, TypeName: "int"},
			{GoName: "Tags", TomlKey: "tags", Kind: FieldSlicePrimitive, ElemType: "string", OmitEmpty: true},
			{GoName: "PtrTags", TomlKey: "ptr_tags", Kind: FieldSlicePrimitive, ElemType: "string", SlicePointer: true},
		}},
		// B: codecs — custom, text, []text
		{Name: "Codecs", Fields: []FieldInfo{
			{GoName: "Raw", TomlKey: "raw", Kind: FieldCustom, TypeName: "RawVal"},
			{GoName: "When", TomlKey: "when", Kind: FieldTextMarshaler, TypeName: "time.Time", ImportPath: "time", OmitEmpty: true},
			{GoName: "Whens", TomlKey: "whens", Kind: FieldSliceTextMarshaler, TypeName: "time.Time", ImportPath: "time"},
		}},
		// C: maps of scalars — map[string]string, map[string]NamedMap
		{Name: "Maps", Fields: []FieldInfo{
			{GoName: "Env", TomlKey: "env", Kind: FieldMapStringString},
			{GoName: "Groups", TomlKey: "groups", Kind: FieldMapStringMapStringString, TypeName: "Labels", ImportPath: ""},
		}},
		// D: maps of structs — map[string]Svc, map[string]*Svc
		{Name: "MapStructs", Fields: []FieldInfo{
			{GoName: "Svcs", TomlKey: "svcs", Kind: FieldMapStringStruct, TypeName: "Svc", InnerInfo: svc},
			{GoName: "PtrSvcs", TomlKey: "ptr_svcs", Kind: FieldMapStringStruct, TypeName: "Svc", InnerInfo: svc, SlicePointer: true},
		}},
		// E: nested struct two levels (scoping)
		{Name: "Server", Fields: []FieldInfo{
			{GoName: "Host", TomlKey: "host", Kind: FieldPrimitive, TypeName: "string"},
			{GoName: "Cfg", TomlKey: "cfg", Kind: FieldStruct, TypeName: "Deep", InnerInfo: deep},
		}},
		// F: []Struct and []*Struct (composition showcase)
		{Name: "Router", Fields: []FieldInfo{
			{GoName: "Routes", TomlKey: "routes", Kind: FieldSliceStruct, TypeName: "Route", InnerInfo: route},
			{GoName: "PRoutes", TomlKey: "p_routes", Kind: FieldSliceStruct, TypeName: "Route", InnerInfo: route, SlicePointer: true},
		}},
		// G: *Struct with a nested array-of-tables (InPointerTable flat-key + scoping)
		{Name: "Listener", Fields: []FieldInfo{
			{GoName: "Addr", TomlKey: "addr", Kind: FieldPrimitive, TypeName: "string"},
			{GoName: "TLS", TomlKey: "tls", Kind: FieldPointerStruct, TypeName: "TLS", InnerInfo: tls},
		}},
		// H: cross-package delegation — struct, *struct, []struct, map[string]struct
		{Name: "Deleg", Fields: []FieldInfo{
			{GoName: "Settings", TomlKey: "settings", Kind: FieldDelegatedStruct, TypeName: "opts.Settings", ImportPath: "example.com/opts"},
			{GoName: "PSettings", TomlKey: "p_settings", Kind: FieldPointerDelegatedStruct, TypeName: "opts.Settings", ImportPath: "example.com/opts"},
			{GoName: "Many", TomlKey: "many", Kind: FieldSliceDelegatedStruct, TypeName: "opts.Settings", ImportPath: "example.com/opts"},
			{GoName: "PMany", TomlKey: "p_many", Kind: FieldSliceDelegatedStruct, TypeName: "opts.Settings", ImportPath: "example.com/opts", SlicePointer: true},
			{GoName: "Keyed", TomlKey: "keyed", Kind: FieldMapStringDelegatedStruct, ElemType: "opts.Settings", ImportPath: "example.com/opts"},
		}},
	}
}

// =========================================================================
// The spike
// =========================================================================

func TestSpikeCompositionalFold(t *testing.T) {
	const dataPath = "d.data"
	for _, si := range spikeFixtures() {
		si := si
		t.Run(si.Name, func(t *testing.T) {
			tgt := ReceiverTarget("d", "data")
			tkey := StaticKey("")

			// --- decode ---
			curDec := buildDecodeOps(si, dataPath, "", tgt, tkey, true, true)
			foldDec := foldDecodeOps(si, dataPath, "", tgt, tkey, true, true)
			if !reflect.DeepEqual(curDec, foldDec) {
				t.Errorf("decode op trees differ for %s", si.Name)
			}
			if a, b := spikeRenderDecode(t, curDec), spikeRenderDecode(t, foldDec); a != b {
				t.Errorf("rendered DECODE differs for %s\n--- builder ---\n%s\n--- fold ---\n%s", si.Name, a, b)
			}

			// --- encode ---
			curEnc := buildEncodeOps(si, tgt, tkey, true, true)
			foldEnc := foldEncodeOps(si, tgt, tkey, true, true)
			if !reflect.DeepEqual(curEnc, foldEnc) {
				t.Errorf("encode op trees differ for %s", si.Name)
			}
			if a, b := spikeRenderEncode(t, curEnc), spikeRenderEncode(t, foldEnc); a != b {
				t.Errorf("rendered ENCODE differs for %s\n--- builder ---\n%s\n--- fold ---\n%s", si.Name, a, b)
			}
		})
	}
}

// =========================================================================
// Property-based harness (step #3)
//
// The 16 FieldKinds are fixed, but the TREE of nested structs/slices/maps
// containing them is unbounded — and that's where scoping bugs (#50/#51/#55/
// #62) lived. This generates random bounded-depth StructInfo trees with random
// field kinds at every level and asserts fold == builder (DeepEqual + render)
// for BOTH directions across the whole generated space. The seed is fixed, so
// a failure reproduces exactly; a coverage assertion guarantees every kind was
// actually exercised.
// =========================================================================

var spikeLeafKinds = []FieldKind{
	FieldPrimitive, FieldPointerPrimitive, FieldSlicePrimitive,
	FieldCustom, FieldTextMarshaler, FieldSliceTextMarshaler,
	FieldMapStringString, FieldMapStringMapStringString,
	FieldDelegatedStruct, FieldPointerDelegatedStruct,
	FieldSliceDelegatedStruct, FieldMapStringDelegatedStruct,
}

var spikeContainerKinds = []FieldKind{
	FieldStruct, FieldPointerStruct, FieldSliceStruct, FieldMapStringStruct,
}

var spikePrims = []string{"string", "int", "int64", "uint64", "float64", "bool"}

const spikeMaxDepth = 3

type spikeGen struct {
	rng  *rand.Rand
	n    int
	hist map[FieldKind]int
}

func (g *spikeGen) uid() int     { g.n++; return g.n }
func (g *spikeGen) coin() bool   { return g.rng.Intn(2) == 0 }
func (g *spikeGen) prim() string { return spikePrims[g.rng.Intn(len(spikePrims))] }

func (g *spikeGen) genStruct(lvl int) *StructInfo {
	si := &StructInfo{Name: fmt.Sprintf("T%d", g.uid())}
	for i, nf := 0, 1+g.rng.Intn(4); i < nf; i++ {
		si.Fields = append(si.Fields, g.genField(lvl))
	}
	return si
}

func (g *spikeGen) genField(lvl int) FieldInfo {
	id := g.uid()
	fi := FieldInfo{GoName: fmt.Sprintf("F%d", id), TomlKey: fmt.Sprintf("k%d", id)}

	var kind FieldKind
	if lvl >= spikeMaxDepth {
		kind = spikeLeafKinds[g.rng.Intn(len(spikeLeafKinds))]
	} else {
		pool := append(append([]FieldKind{}, spikeLeafKinds...), spikeContainerKinds...)
		kind = pool[g.rng.Intn(len(pool))]
	}
	fi.Kind = kind
	g.hist[kind]++

	switch kind {
	case FieldPrimitive:
		fi.TypeName = g.prim()
		fi.OmitEmpty = g.coin()
		fi.Multiline = fi.TypeName == "string" && g.coin()
	case FieldPointerPrimitive:
		fi.TypeName = g.prim()
	case FieldSlicePrimitive:
		fi.ElemType = g.prim()
		fi.SlicePointer = g.coin()
		fi.OmitEmpty = g.coin()
	case FieldCustom:
		fi.TypeName = fmt.Sprintf("Custom%d", id)
	case FieldTextMarshaler:
		fi.TypeName = fmt.Sprintf("Text%d", id)
		fi.OmitEmpty = g.coin()
	case FieldSliceTextMarshaler:
		fi.TypeName = fmt.Sprintf("Text%d", id)
		fi.OmitEmpty = g.coin()
	case FieldMapStringString:
		// no extra fields
	case FieldMapStringMapStringString:
		if g.coin() {
			fi.TypeName = fmt.Sprintf("Labels%d", id)
		}
	case FieldDelegatedStruct, FieldPointerDelegatedStruct:
		fi.TypeName = fmt.Sprintf("p%d.T%d", id, id)
		fi.ImportPath = fmt.Sprintf("example.com/p%d", id)
	case FieldSliceDelegatedStruct:
		fi.TypeName = fmt.Sprintf("p%d.T%d", id, id)
		fi.ImportPath = fmt.Sprintf("example.com/p%d", id)
		fi.SlicePointer = g.coin()
	case FieldMapStringDelegatedStruct:
		fi.ElemType = fmt.Sprintf("p%d.T%d", id, id)
		fi.ImportPath = fmt.Sprintf("example.com/p%d", id)
	case FieldStruct:
		fi.InnerInfo = g.genStruct(lvl + 1)
		fi.TypeName = fi.InnerInfo.Name
	case FieldPointerStruct:
		fi.InnerInfo = g.genStruct(lvl + 1)
		fi.TypeName = fi.InnerInfo.Name
	case FieldSliceStruct:
		fi.InnerInfo = g.genStruct(lvl + 1)
		fi.TypeName = fi.InnerInfo.Name
		fi.SlicePointer = g.coin()
	case FieldMapStringStruct:
		fi.InnerInfo = g.genStruct(lvl + 1)
		fi.TypeName = fi.InnerInfo.Name
		fi.SlicePointer = g.coin()
	}
	return fi
}

func spikeKindName(k FieldKind) string {
	switch k {
	case FieldPrimitive:
		return "Primitive"
	case FieldPointerPrimitive:
		return "PointerPrimitive"
	case FieldSlicePrimitive:
		return "SlicePrimitive"
	case FieldCustom:
		return "Custom"
	case FieldTextMarshaler:
		return "TextMarshaler"
	case FieldSliceTextMarshaler:
		return "SliceTextMarshaler"
	case FieldMapStringString:
		return "MapStringString"
	case FieldMapStringMapStringString:
		return "MapStringMapStringString"
	case FieldDelegatedStruct:
		return "DelegatedStruct"
	case FieldPointerDelegatedStruct:
		return "PointerDelegatedStruct"
	case FieldSliceDelegatedStruct:
		return "SliceDelegatedStruct"
	case FieldMapStringDelegatedStruct:
		return "MapStringDelegatedStruct"
	case FieldStruct:
		return "Struct"
	case FieldPointerStruct:
		return "PointerStruct"
	case FieldSliceStruct:
		return "SliceStruct"
	case FieldMapStringStruct:
		return "MapStringStruct"
	}
	return fmt.Sprintf("FieldKind(%d)", int(k))
}

func TestSpikeCompositionalProperty(t *testing.T) {
	const trees = 500
	g := &spikeGen{rng: rand.New(rand.NewSource(0xC0FFEE)), hist: map[FieldKind]int{}}
	tgt := ReceiverTarget("d", "data")
	tkey := StaticKey("")

	for i := 0; i < trees; i++ {
		si := *g.genStruct(0)
		si.Name = fmt.Sprintf("Gen%d", i)

		curDec := buildDecodeOps(si, "d.data", "", tgt, tkey, true, true)
		foldDec := foldDecodeOps(si, "d.data", "", tgt, tkey, true, true)
		if !reflect.DeepEqual(curDec, foldDec) {
			t.Fatalf("tree %d (seed 0xC0FFEE): decode op trees differ\n%#v", i, si)
		}
		if a, b := spikeRenderDecode(t, curDec), spikeRenderDecode(t, foldDec); a != b {
			t.Fatalf("tree %d: rendered DECODE differs\n--- builder ---\n%s\n--- fold ---\n%s", i, a, b)
		}

		curEnc := buildEncodeOps(si, tgt, tkey, true, true)
		foldEnc := foldEncodeOps(si, tgt, tkey, true, true)
		if !reflect.DeepEqual(curEnc, foldEnc) {
			t.Fatalf("tree %d (seed 0xC0FFEE): encode op trees differ\n%#v", i, si)
		}
		if a, b := spikeRenderEncode(t, curEnc), spikeRenderEncode(t, foldEnc); a != b {
			t.Fatalf("tree %d: rendered ENCODE differs\n--- builder ---\n%s\n--- fold ---\n%s", i, a, b)
		}
	}

	// Coverage: every FieldKind must have been generated, else the run proved
	// nothing about that kind.
	allKinds := append(append([]FieldKind{}, spikeLeafKinds...), spikeContainerKinds...)
	for _, k := range allKinds {
		if g.hist[k] == 0 {
			t.Errorf("kind %s never generated — increase trees or reseed", spikeKindName(k))
		}
	}

	var b strings.Builder
	for _, k := range allKinds {
		fmt.Fprintf(&b, "\n  %-26s %d", spikeKindName(k), g.hist[k])
	}
	t.Logf("%d trees, %d total fields generated; per-kind coverage:%s", trees, g.n, b.String())
}
