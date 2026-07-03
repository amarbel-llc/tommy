package generate

// TypeExpr is the compositional algebra classifyTypeExpr (analyze.go) classifies
// struct fields into: a small, fixed set of constructors that compose, and which
// the folds (comp_build.go) recurse over directly. Custom/TextMarshaler are
// Scalar *codecs* (not constructors); Delegated is an opaque cross-package leaf.
// A pointer is a structural Ptr node (e.g. []*Struct = Slice(Ptr(Struct)),
// map[string]NamedMap = Map(Map(Scalar))) rather than a side-channel bool. Each
// constructor carries the type-derived payload at the node that owns it.

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
type (
	spkPtr   struct{ Elem spkType }
	spkSlice struct {
		Elem       spkType
		TypeName   string // named slice-alias wrapper ("pkg.IntSlice"); "" if anonymous
		ImportPath string
	}
)
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
