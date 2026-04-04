package generate

// DecodeOp represents a single decode operation in the IR.
// The builder constructs a tree of DecodeOps with all scoping decisions resolved.
// The renderer walks the tree and emits Go source with minimal context.
type DecodeOp interface{ decodeOp() }

// --- Leaf decode operations ---

// GetPrimitive decodes a primitive value via GetFromContainer[T].
type GetPrimitive struct {
	Target     string // Go expression for assignment (e.g. "d.data.Name")
	Key        string // TOML key relative to container
	Tgt        TargetPath
	TKey       TOMLKey
	TypeName   string // Go type for generic param
	ElemType   string // type alias conversion (empty if none)
	ImportPath string // import path for ElemType if cross-package
	Pointer    bool   // wrap in &v
}

// GetCustom decodes a custom unmarshaler via GetRawFromContainer + UnmarshalTOML.
type GetCustom struct {
	Target     string
	Key        string
	Tgt        TargetPath
	TKey       TOMLKey
	TypeName   string
	ImportPath string
}

// GetTextMarshaler decodes a TextUnmarshaler via GetFromContainer[string] + UnmarshalText.
type GetTextMarshaler struct {
	Target     string
	Key        string
	Tgt        TargetPath
	TKey       TOMLKey
	TypeName   string
	ImportPath string
}

// GetSlicePrimitive decodes a primitive slice via GetFromContainer[[]T].
type GetSlicePrimitive struct {
	Target       string
	Key          string
	Tgt          TargetPath
	TKey         TOMLKey
	ElemType     string // element type for the generic param
	TypeName     string // named type conversion (empty if none)
	ImportPath   string // import path for TypeName if cross-package
	SlicePointer bool   // []*T
}

// GetSliceTextMarshaler decodes a slice of TextUnmarshalers via GetFromContainer[[]string].
type GetSliceTextMarshaler struct {
	Target     string
	Key        string
	Tgt        TargetPath
	TKey       TOMLKey
	TypeName   string
	ImportPath string
}

// GetMapStringString decodes map[string]string via FindTable + GetStringMapFromTable.
type GetMapStringString struct {
	Target     string
	Key        string
	Tgt        TargetPath
	TKey       TOMLKey
	UseRootAPI bool // FindTable vs FindTableInContainer
}

// GetMapStringMapStringString decodes map[string]map[string]string via FindSubTables.
type GetMapStringMapStringString struct {
	Target   string
	Key      string
	Tgt      TargetPath
	TKey     TOMLKey
	TypeName string // named type for inner map (empty for plain map[string]string)
}

// --- Container decode operations ---

// InTable finds a [table] and decodes inner fields within it.
type InTable struct {
	Key        string
	TKey       TOMLKey
	UseRootAPI bool // FindTable vs FindTableInContainer
	Fields     []DecodeOp
}

// InPointerTable finds a [table], allocates *Struct, decodes fields;
// else tries flat-key fallback for implicit parent tables.
type InPointerTable struct {
	Key         string
	TKey        TOMLKey
	TypeName    string
	Target      string // Go expression for pointer assignment
	Tgt         TargetPath
	TableFields []DecodeOp // fields decoded when explicit table found
	FlatFields  []DecodeOp // fields decoded at parent container (flat-key fallback)
}

// ForArrayTable iterates array table nodes and decodes inner fields per entry.
type ForArrayTable struct {
	Key          string // relative TOML key
	DottedKey    string // full dotted key for FindArrayTableNodes
	TKey         TOMLKey
	TDottedKey   TOMLKey
	TypeName     string // element type
	ImportPath   string // import path if cross-package
	Target       string // slice target
	Tgt          TargetPath
	SlicePointer bool
	TrackHandles bool // emit handle code (top-level receiver only)
	Fields       []DecodeOp
}

// ForMapStringStruct iterates sub-tables and decodes inner fields per map entry.
type ForMapStringStruct struct {
	Key          string
	TKey         TOMLKey
	TypeName     string
	Target       string
	Tgt          TargetPath
	SlicePointer bool
	UseRootAPI   bool // FindSubTables vs FindSubTablesInContainer
	Fields       []DecodeOp
}

// --- Delegation operations (cross-package) ---

// DelegateStruct delegates decoding to another package's DecodeXInto.
type DelegateStruct struct {
	Target     string
	Key        string
	Tgt        TargetPath
	TKey       TOMLKey
	TypeName   string // "pkg.Type"
	ImportPath string // full import path for the package
	Pointer    bool   // allocate *Type before delegating
	UseRootAPI bool   // FindTable vs FindTableInContainer
}

// DelegateSlice delegates decoding of []cross-package struct per array table entry.
type DelegateSlice struct {
	Target       string
	Key          string
	DottedKey    string // full dotted key for FindArrayTableNodes
	Tgt          TargetPath
	TKey         TOMLKey
	TDottedKey   TOMLKey
	TypeName     string // "pkg.Type"
	ImportPath   string // full import path for the package
	SlicePointer bool
}

// DelegateMap delegates decoding of map[string]cross-package struct per sub-table.
type DelegateMap struct {
	Target     string
	Key        string
	Tgt        TargetPath
	TKey       TOMLKey
	ElemType   string // "pkg.Type"
	ImportPath string // full import path for the package
	UseRootAPI bool   // FindSubTables vs FindSubTablesInContainer
}

// --- Validation ---

// Validate calls data.Validate() if the struct implements the Validate interface.
type Validate struct {
	DataExpr   string // "d.data" or "data"
	DataTarget TargetPath
}

// --- FoundVar wrapper ---

// WithFoundVar wraps an op to emit "foundVar = true" alongside it.
// Used inside InPointerTable flat-key fallback.
type WithFoundVar struct {
	Inner    DecodeOp
	FoundVar string
}

// --- Interface satisfaction (decode) ---

func (GetPrimitive) decodeOp()                {}
func (GetCustom) decodeOp()                   {}
func (GetTextMarshaler) decodeOp()            {}
func (GetSlicePrimitive) decodeOp()           {}
func (GetSliceTextMarshaler) decodeOp()       {}
func (GetMapStringString) decodeOp()          {}
func (GetMapStringMapStringString) decodeOp() {}
func (InTable) decodeOp()                     {}
func (InPointerTable) decodeOp()              {}
func (ForArrayTable) decodeOp()               {}
func (ForMapStringStruct) decodeOp()          {}
func (DelegateStruct) decodeOp()              {}
func (DelegateSlice) decodeOp()               {}
func (DelegateMap) decodeOp()                 {}
func (Validate) decodeOp()                    {}
func (WithFoundVar) decodeOp()                {}

// ==========================================================================
// Encode IR
// ==========================================================================

// EncodeOp represents a single encode operation in the IR.
type EncodeOp interface{ encodeOp() }

// --- Leaf encode operations ---

// SetPrimitive writes a primitive value with zero-value/omitempty logic.
type SetPrimitive struct {
	Tgt        TargetPath
	TKey       TOMLKey
	TypeName   string // Go type (string, int, bool, float64, etc.)
	ElemType   string // type alias underlying type (empty if none)
	ImportPath string
	OmitEmpty  bool
	Multiline  bool // use """ multiline string syntax
}

// SetPointerPrimitive writes *T if non-nil.
type SetPointerPrimitive struct {
	Tgt      TargetPath
	TKey     TOMLKey
	TypeName string // underlying type (string, int, etc.)
}

// SetCustom calls MarshalTOML() then SetInContainer.
type SetCustom struct {
	Tgt  TargetPath
	TKey TOMLKey
}

// SetTextMarshaler calls MarshalText() then SetInContainer.
type SetTextMarshaler struct {
	Tgt       TargetPath
	TKey      TOMLKey
	OmitEmpty bool
}

// SetSlicePrimitive writes a primitive slice.
type SetSlicePrimitive struct {
	Tgt          TargetPath
	TKey         TOMLKey
	ElemType     string // element type for slice
	TypeName     string // named type conversion
	ImportPath   string
	SlicePointer bool // []*T
	OmitEmpty    bool
}

// SetSliceTextMarshaler marshals each element then writes []string.
type SetSliceTextMarshaler struct {
	Tgt        TargetPath
	TKey       TOMLKey
	TypeName   string
	ImportPath string
}

// SetMapStringString writes map[string]string via EnsureTable + DeleteAll + loop.
type SetMapStringString struct {
	Tgt        TargetPath
	TKey       TOMLKey
	UseRootAPI bool
}

// --- Container encode operations ---

// InEncodeTable creates/ensures a table then recurses inner fields.
type InEncodeTable struct {
	TKey       TOMLKey
	UseRootAPI bool
	Fields     []EncodeOp
}

// InEncodePointerTable checks nil, creates table, recurses inner fields.
type InEncodePointerTable struct {
	Tgt      TargetPath
	TKey     TOMLKey
	TypeName string
	Fields   []EncodeOp
}

// ForEncodeArrayTable loops over a slice, reusing handles or appending entries.
type ForEncodeArrayTable struct {
	Tgt          TargetPath
	TKey         TOMLKey // bare key for AppendArrayTableEntry
	TDottedKey   TOMLKey // full dotted key for FindArrayTableNodes
	TypeName     string
	ImportPath   string
	SlicePointer bool
	TrackHandles bool // use decode handles in receiver context
	Fields       []EncodeOp
}

// ForEncodeMapStringStruct loops over map entries, EnsureSubTable per entry.
type ForEncodeMapStringStruct struct {
	Tgt          TargetPath
	TKey         TOMLKey
	TypeName     string
	SlicePointer bool
	UseRootAPI   bool
	Fields       []EncodeOp
}

// ForEncodeMapStringMapStringString loops outer map, EnsureSubTable, DeleteAll, loop inner.
type ForEncodeMapStringMapStringString struct {
	Tgt      TargetPath
	TKey     TOMLKey
	TypeName string // named type for inner map (empty for plain)
}

// --- Delegation encode operations ---

// EncodeDelegateStruct creates table then calls pkg.EncodeXFrom.
type EncodeDelegateStruct struct {
	Tgt        TargetPath
	TKey       TOMLKey
	TypeName   string // "pkg.Type"
	ImportPath string
	Pointer    bool
	UseRootAPI bool
}

// EncodeDelegateSlice loops, get/create container, calls pkg.EncodeXFrom.
type EncodeDelegateSlice struct {
	Tgt          TargetPath
	TKey         TOMLKey // bare key
	TDottedKey   TOMLKey // full dotted key
	TypeName     string
	ImportPath   string
	SlicePointer bool
}

// EncodeDelegateMap loops, EnsureSubTable, calls pkg.EncodeXFrom.
type EncodeDelegateMap struct {
	Tgt        TargetPath
	TKey       TOMLKey
	ElemType   string // "pkg.Type"
	ImportPath string
	UseRootAPI bool
}

// EncodeValidate calls data.Validate() before encoding.
type EncodeValidate struct {
	DataTarget TargetPath
}

// --- Interface satisfaction (encode) ---

func (SetPrimitive) encodeOp()                      {}
func (SetPointerPrimitive) encodeOp()               {}
func (SetCustom) encodeOp()                         {}
func (SetTextMarshaler) encodeOp()                  {}
func (SetSlicePrimitive) encodeOp()                 {}
func (SetSliceTextMarshaler) encodeOp()             {}
func (SetMapStringString) encodeOp()                {}
func (InEncodeTable) encodeOp()                     {}
func (InEncodePointerTable) encodeOp()              {}
func (ForEncodeArrayTable) encodeOp()               {}
func (ForEncodeMapStringStruct) encodeOp()          {}
func (ForEncodeMapStringMapStringString) encodeOp() {}
func (EncodeDelegateStruct) encodeOp()              {}
func (EncodeDelegateSlice) encodeOp()               {}
func (EncodeDelegateMap) encodeOp()                 {}
func (EncodeValidate) encodeOp()                    {}
