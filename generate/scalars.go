package generate

// scalarType is one row of the canonical primitive-scalar registry: the single
// source of truth for which Go scalar types the codegen supports and how each
// maps onto a cst extractor.
//
// Historically the scalar set was hardcoded in four parallel per-type switches
// that had to be kept in sync by hand — cstExtract and cstSliceExtractFunc here,
// cst.EncodeValue in pkg/cst, and the round-trip fuzzer's scalarType/
// sliceScalarType/scalarValue — and they drifted (the fuzzer generated the
// narrowest set, hiding the []bool/[]float64 (#98) and sized-int (#96) gaps).
// This registry is the fix: the two decode helpers below and the fuzzer's type
// universe all derive from it, so adding a scalar is one row that propagates to
// scalar decode, slice decode, and fuzz coverage.
//
// Encode is the one consumer that can't derive from this list: cst.EncodeValue
// dispatches on the runtime Go type (a `switch value.(type)`), not a type-name
// string, so it stays an explicit switch — validated against this registry by the
// round-trip fuzzer (which now generates every registered scalar) rather than
// generated from it.
type scalarType struct {
	goName    string // Go type name as written on a field, e.g. "int8"
	extractFn string // cst scalar extractor, e.g. "ExtractInt64"
	sliceFn   string // cst slice extractor, e.g. "ExtractInt64Slice"
	// cast is the conversion applied to the extractor's result for a sized type
	// whose extractor returns the widest variant (e.g. int8 decodes via
	// ExtractInt64 then int8(...)). Empty when the extractor already returns the
	// field's exact type.
	cast string
}

// scalarTypes is the canonical primitive set. extractFn/sliceFn name functions
// in pkg/cst (see accessors.go). The base (cast-free) extractors return the
// field's exact type; sized variants share a base extractor + a cast.
var scalarTypes = []scalarType{
	{goName: "string", extractFn: "ExtractString", sliceFn: "ExtractStringSlice"},
	{goName: "bool", extractFn: "ExtractBool", sliceFn: "ExtractBoolSlice"},
	{goName: "int", extractFn: "ExtractInt", sliceFn: "ExtractIntSlice"},
	{goName: "int8", extractFn: "ExtractInt64", sliceFn: "ExtractInt64Slice", cast: "int8"},
	{goName: "int16", extractFn: "ExtractInt64", sliceFn: "ExtractInt64Slice", cast: "int16"},
	{goName: "int32", extractFn: "ExtractInt64", sliceFn: "ExtractInt64Slice", cast: "int32"},
	{goName: "int64", extractFn: "ExtractInt64", sliceFn: "ExtractInt64Slice"},
	{goName: "uint", extractFn: "ExtractUint64", sliceFn: "ExtractUint64Slice", cast: "uint"},
	{goName: "uint8", extractFn: "ExtractUint64", sliceFn: "ExtractUint64Slice", cast: "uint8"},
	{goName: "uint16", extractFn: "ExtractUint64", sliceFn: "ExtractUint64Slice", cast: "uint16"},
	{goName: "uint32", extractFn: "ExtractUint64", sliceFn: "ExtractUint64Slice", cast: "uint32"},
	{goName: "uint64", extractFn: "ExtractUint64", sliceFn: "ExtractUint64Slice"},
	{goName: "float32", extractFn: "ExtractFloat64", sliceFn: "ExtractFloat64Slice", cast: "float32"},
	{goName: "float64", extractFn: "ExtractFloat64", sliceFn: "ExtractFloat64Slice"},
}

// lookupScalar returns the registry row for a Go scalar type name.
func lookupScalar(goName string) (scalarType, bool) {
	for _, s := range scalarTypes {
		if s.goName == goName {
			return s, true
		}
	}
	return scalarType{}, false
}
