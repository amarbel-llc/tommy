package generate

import "testing"

// Validates the representability fold (ADR 2026-06-08 prototype) against the four
// value-space bug cells it must predict. If the static fold matches the bugs we
// found empirically, the model is a viable single source of truth.

func scalar() spkType { return spkScalar{Codec: codecPrim, TypeName: "string"} }

func structOf(fields ...FieldInfo) spkStruct {
	return spkStruct{TypeName: "S", InnerInfo: &StructInfo{Name: "S", Fields: fields}}
}

func TestReprScalar(t *testing.T) {
	if reprOf(scalar(), false).MayBeSilent {
		t.Error("a non-omitempty scalar always emits — must not be silent")
	}
	if !reprOf(scalar(), true).MayBeSilent {
		t.Error("an omitempty scalar omits its zero value — must be silent")
	}
}

// #121 vs #1: a []scalar has a present-empty form (`= []`) so empty is distinct
// from nil; an array-of-tables ([]struct) has none, so empty collapses to nil.
func TestReprSliceEmptyForm(t *testing.T) {
	if !reprOf(spkSlice{Elem: scalar()}, false).EmptyDistinct {
		t.Error("[]scalar: empty `= []` must be distinct from nil (#121)")
	}
	if reprOf(spkSlice{Elem: structOf(FieldInfo{Type: scalar()})}, false).EmptyDistinct {
		t.Error("[]struct: empty array-of-tables has no present form (#1)")
	}
	if reprOf(spkSlice{Elem: spkPtr{Elem: structOf(FieldInfo{Type: scalar()})}}, false).EmptyDistinct {
		t.Error("[]*struct: still an array-of-tables — no present-empty form")
	}
}

// #122: a struct whose only field is an array-of-tables can be all-silent, so a
// non-nil pointer to it does not round-trip. A struct with a required scalar
// always witnesses, so a non-nil pointer to it always round-trips.
func TestReprPointerStructRoundTrips(t *testing.T) {
	// Sub7 { F0 []*Sub8 } — the exact #122 shape.
	sub7 := &StructInfo{Name: "Sub7", Fields: []FieldInfo{
		{GoName: "F0", TomlKey: "f0", Type: spkSlice{Elem: spkPtr{Elem: structOf(FieldInfo{Type: scalar()})}}},
	}}
	if pointerStructRoundTrips(sub7) {
		t.Error("*Sub7{F0:nil} is silent → must be flagged as NOT round-tripping (#122)")
	}

	// Server { Host string } — a required scalar always witnesses.
	server := &StructInfo{Name: "Server", Fields: []FieldInfo{
		{GoName: "Host", TomlKey: "host", Type: scalar()},
	}}
	if !pointerStructRoundTrips(server) {
		t.Error("*Server always witnesses via Host → must round-trip")
	}

	// A struct of only optional/collection fields is silent-able.
	loose := &StructInfo{Name: "Loose", Fields: []FieldInfo{
		{GoName: "A", TomlKey: "a", Type: scalar(), OmitEmpty: true},
		{GoName: "B", TomlKey: "b", Type: spkSlice{Elem: scalar()}},
		{GoName: "C", TomlKey: "c", Type: spkPtr{Elem: scalar()}},
	}}
	if pointerStructRoundTrips(loose) {
		t.Error("a struct of only omitempty/slice/pointer fields can be all-silent")
	}

	// Nesting: a struct whose only field is a *Server (silent when nil) is itself
	// silent-able, even though Server alone always witnesses.
	wrapper := &StructInfo{Name: "Wrapper", Fields: []FieldInfo{
		{GoName: "S", TomlKey: "s", Type: spkPtr{Elem: spkStruct{TypeName: "Server", InnerInfo: server}}},
	}}
	if pointerStructRoundTrips(wrapper) {
		t.Error("Wrapper{*Server} is silent when S is nil → must not be claimed round-tripping")
	}
}
