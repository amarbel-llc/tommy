package generate

// TypeExpr is the compositional algebra the code generator classifies struct
// fields into: a small, fixed set of constructors that compose, replacing the
// flat FieldKind enumeration at the IR-build boundary. Custom/TextMarshaler are
// Scalar *codecs* (not constructors); Delegated is an opaque cross-package leaf.
//
// Every FieldKind maps to a composition (see fieldType), e.g. []*Struct =
// Slice(Ptr(Struct)) and map[string]NamedMap = Map(Map(Scalar)) — the
// SlicePointer flag becomes a structural Ptr rather than a side-channel bool.

type spkType interface{ isSpkType() }

type spkCodec int

const (
	codecPrim spkCodec = iota
	codecText
	codecCustom
)

// The constructors carry the type-derived payload at the node that owns it
// (#85 Phase 2b): a scalar names its Go type and any primitive-wrapper/codec
// import; a named slice/map wrapper (type IntSlice []int, type Labels
// map[string]string) annotates its Slice/Map node; struct/delegated carry their
// qualified name, import, and resolved inner StructInfo. adaptToFieldInfo
// (analyze.go) flattens this back to the legacy FieldInfo while the folds still
// consume FieldInfo; fieldType leaves the payload fields zero (it builds only the
// structural skeleton from a flat FieldInfo).
type spkScalar struct {
	Codec      spkCodec
	TypeName   string // prim: Go type name; text/custom: "pkg.Type"
	ElemType   string // prim wrapper: "pkg.Type" underlying-name conversion
	ImportPath string // prim wrapper / cross-pkg codec
}
type spkPtr struct{ Elem spkType }
type spkSlice struct {
	Elem       spkType
	TypeName   string // named slice-alias wrapper ("pkg.IntSlice"); "" if anonymous
	ImportPath string
}
type spkMap struct {
	Elem       spkType
	TypeName   string // named map-alias wrapper ("pkg.Labels"); "" if anonymous
	ImportPath string
}
type spkStruct struct {
	TypeName   string
	ImportPath string // facade path for an inline cross-pkg struct reached via an unexported-target alias; "" same-pkg
	InnerInfo  *StructInfo
}
type spkDelegated struct {
	TypeName   string
	ImportPath string
	InnerInfo  *StructInfo // resolved for parity/validation; unused by the fold (see analyze.go)
}

func (spkScalar) isSpkType()    {}
func (spkPtr) isSpkType()       {}
func (spkSlice) isSpkType()     {}
func (spkMap) isSpkType()       {}
func (spkStruct) isSpkType()    {}
func (spkDelegated) isSpkType() {}

// fieldType maps a classified FieldInfo to its compositional TypeExpr.
func fieldType(fi FieldInfo) spkType {
	ptrIf := func(p bool, t spkType) spkType {
		if p {
			return spkPtr{Elem: t}
		}
		return t
	}
	switch fi.Kind {
	case FieldPrimitive:
		return spkScalar{Codec: codecPrim}
	case FieldCustom:
		return spkScalar{Codec: codecCustom}
	case FieldTextMarshaler:
		return spkScalar{Codec: codecText}
	case FieldPointerPrimitive:
		return spkPtr{Elem: spkScalar{Codec: codecPrim}}
	case FieldSlicePrimitive:
		return spkSlice{Elem: ptrIf(fi.SlicePointer, spkScalar{Codec: codecPrim})}
	case FieldSliceTextMarshaler:
		return spkSlice{Elem: spkScalar{Codec: codecText}}
	case FieldStruct:
		return spkStruct{}
	case FieldPointerStruct:
		return spkPtr{Elem: spkStruct{}}
	case FieldSliceStruct:
		return spkSlice{Elem: ptrIf(fi.SlicePointer, spkStruct{})}
	case FieldMapStringString:
		return spkMap{Elem: spkScalar{Codec: codecPrim}}
	case FieldMapStringMapStringString:
		return spkMap{Elem: spkMap{Elem: spkScalar{Codec: codecPrim}}}
	case FieldMapStringStruct:
		return spkMap{Elem: ptrIf(fi.SlicePointer, spkStruct{})}
	case FieldDelegatedStruct:
		return spkDelegated{}
	case FieldPointerDelegatedStruct:
		return spkPtr{Elem: spkDelegated{}}
	case FieldSliceDelegatedStruct:
		return spkSlice{Elem: ptrIf(fi.SlicePointer, spkDelegated{})}
	case FieldMapStringDelegatedStruct:
		return spkMap{Elem: spkDelegated{}}
	}
	panic("fieldType: unknown FieldKind")
}
