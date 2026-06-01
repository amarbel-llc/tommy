package generate

// Compositional IR (#84, ADR docs/decisions/2026-06-01-compositional-codegen.md
// option B). These node families replace the enumerated DecodeOp/EncodeOp set:
// one recursive renderer per direction walks them, with TOML container position
// threaded as a fold parameter rather than baked into per-op helpers.
//
// The shrink vs. the enumerated IR: the five leaf decode ops collapse into a
// single cdLeaf (tagged by cdLeafKind), Ptr(Struct) becomes one cdNilGuard
// wrapping a single child body (no TableFields/FlatFields duplication beyond the
// #55 fallback list), and the positional jenP* family folds into one compPosOp
// dispatch reused recursively.
//
// Phase 1 keeps the analyze.go classifier; the folds (comp_build.go) consume the
// existing FieldInfo via fieldType (typeexpr.go).

// --- Decode nodes ---

type cdNode interface{ isCDNode() }

// cdLeafKind tags the codec/shape a cdLeaf decodes. Each maps to one of the
// former Get* leaf ops.
type cdLeafKind int

const (
	cdLeafPrim      cdLeafKind = iota // GetPrimitive (incl. *prim, alias)
	cdLeafCustom                      // GetCustom
	cdLeafText                        // GetTextMarshaler
	cdLeafSlicePrim                   // GetSlicePrimitive (incl. []*prim, named)
	cdLeafSliceText                   // GetSliceTextMarshaler
)

// cdLeaf is a key-value scanned in the current container. TKey is the full
// prefixed/dotted key (consumed marks use it; the switch case label is its
// BareKey).
type cdLeaf struct {
	Kind         cdLeafKind
	Tgt          TargetPath
	TKey         TOMLKey
	TypeName     string // prim: Go type for cstExtract; sliceText/slicePrim: named-type conversion
	ElemType     string // prim: alias underlying conversion; slicePrim: generic elem type
	ImportPath   string
	Pointer      bool // *prim
	SlicePointer bool // []*prim
}

// cdInTable finds [TKey] from the document root and decodes children scoped to
// the found node.
type cdInTable struct {
	TKey     TOMLKey
	Children []cdNode
}

// cdNilGuard is Ptr(Struct): find [TKey], allocate *TypeName, decode Children;
// else (#55) decode FlatChildren at the parent container with a found-guard.
type cdNilGuard struct {
	Tgt          TargetPath
	TypeName     string
	TKey         TOMLKey
	Children     []cdNode // decoded inside the explicit [table]
	FlatChildren []cdNode // flat-key fallback at the parent container
}

// cdArrayTable iterates [[TDottedKey]] entries. In receiver context for a
// same-package element, TrackHandles stores the *cst.Node per entry on the
// Document for round-trip-stable encode.
type cdArrayTable struct {
	Tgt          TargetPath
	TypeName     string
	ImportPath   string
	TKey         TOMLKey // bare key
	TDottedKey   TOMLKey // full dotted key
	SlicePtr     bool
	TrackHandles bool
	Children     []cdNode
}

// cdMapScalar is map[string]string: find [TKey], ExtractStringMap.
type cdMapScalar struct {
	Tgt  TargetPath
	TKey TOMLKey
}

// cdMapMap is map[string]map[string]string: iterate [TKey.*] sub-tables.
type cdMapMap struct {
	Tgt        TargetPath
	TKey       TOMLKey
	TypeName   string // named inner-map type ("" for plain map[string]string)
	ImportPath string
}

// cdMapStruct is map[string]Struct / map[string]*Struct: iterate [TKey.<key>]
// sub-tables. Children are folded with the runtime _mk spliced into their TKey.
type cdMapStruct struct {
	Tgt      TargetPath
	TKey     TOMLKey
	TypeName string
	SlicePtr bool
	Children []cdNode
}

// cdDelStruct delegates to ImportPath.Decode<TypeName>Into.
type cdDelStruct struct {
	Tgt        TargetPath
	TKey       TOMLKey
	ImportPath string
	TypeName   string // short type name (post-delegateParts)
	Ptr        bool
}

// cdDelSlice delegates per array-table entry.
type cdDelSlice struct {
	Tgt        TargetPath
	TKey       TOMLKey // bare key (for error messages)
	TDottedKey TOMLKey // full dotted key
	ImportPath string
	TypeName   string // short type name
	SlicePtr   bool
}

// cdDelMap delegates per [TKey.<key>] sub-table.
type cdDelMap struct {
	Tgt        TargetPath
	TKey       TOMLKey
	ImportPath string
	ElemType   string // short type name
}

func (cdLeaf) isCDNode()       {}
func (cdInTable) isCDNode()    {}
func (cdNilGuard) isCDNode()   {}
func (cdArrayTable) isCDNode() {}
func (cdMapScalar) isCDNode()  {}
func (cdMapMap) isCDNode()     {}
func (cdMapStruct) isCDNode()  {}
func (cdDelStruct) isCDNode()  {}
func (cdDelSlice) isCDNode()   {}
func (cdDelMap) isCDNode()     {}

// --- Encode nodes ---

type ceNode interface{ isCENode() }

type ceLeafKind int

const (
	ceLeafPrim      ceLeafKind = iota // SetPrimitive
	ceLeafPtrPrim                     // SetPointerPrimitive
	ceLeafCustom                      // SetCustom
	ceLeafText                        // SetTextMarshaler
	ceLeafSlicePrim                   // SetSlicePrimitive
	ceLeafSliceText                   // SetSliceTextMarshaler
)

type ceLeaf struct {
	Kind         ceLeafKind
	Tgt          TargetPath
	TKey         TOMLKey
	TypeName     string
	ElemType     string
	ImportPath   string
	OmitEmpty    bool
	Multiline    bool
	SlicePointer bool
}

type ceTable struct {
	TKey     TOMLKey
	Children []ceNode
}

type ceNilGuard struct {
	Tgt      TargetPath
	TKey     TOMLKey
	TypeName string
	Children []ceNode
}

type ceArrayTable struct {
	Tgt          TargetPath
	TKey         TOMLKey // bare key
	TDottedKey   TOMLKey // full dotted key
	TypeName     string
	ImportPath   string
	SlicePtr     bool
	TrackHandles bool
	Children     []ceNode
}

type ceMapScalar struct {
	Tgt  TargetPath
	TKey TOMLKey
}

type ceMapMap struct {
	Tgt      TargetPath
	TKey     TOMLKey
	TypeName string
}

type ceMapStruct struct {
	Tgt      TargetPath
	TKey     TOMLKey
	TypeName string
	SlicePtr bool
	Children []ceNode
}

type ceDelStruct struct {
	Tgt        TargetPath
	TKey       TOMLKey
	ImportPath string
	TypeName   string // short type name
	Ptr        bool
}

type ceDelSlice struct {
	Tgt        TargetPath
	TKey       TOMLKey // bare key
	TDottedKey TOMLKey // full dotted key
	ImportPath string
	TypeName   string // short type name
	SlicePtr   bool
}

type ceDelMap struct {
	Tgt        TargetPath
	TKey       TOMLKey
	ImportPath string
	ElemType   string // short type name
}

func (ceLeaf) isCENode()       {}
func (ceTable) isCENode()      {}
func (ceNilGuard) isCENode()   {}
func (ceArrayTable) isCENode() {}
func (ceMapScalar) isCENode()  {}
func (ceMapMap) isCENode()     {}
func (ceMapStruct) isCENode()  {}
func (ceDelStruct) isCENode()  {}
func (ceDelSlice) isCENode()   {}
func (ceDelMap) isCENode()     {}
