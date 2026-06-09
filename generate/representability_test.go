package generate

import "testing"

// Validates the representability fold (ADR 2026-06-08 prototype) against the four
// value-space bug cells it must predict. If the static fold matches the bugs we
// found empirically, the model is a viable single source of truth.

func scalar() spkType { return spkScalar{Codec: codecPrim, TypeName: "string"} }

// TestFuzzerBridgeEmptyForm locks the round-trip fuzzer's empty-variant policy to
// the representability fold through the td→spkType bridge: every shape that has a
// present-empty TOML form (so genValue may emit `{}` / `= []`) must be exactly the
// one reprOf flags EmptyDistinct. If the two ever diverge the fuzzer would emit an
// unrepresentable value (a red sweep) or silently lose coverage (#94); this guard
// catches the divergence at unit speed instead of seed-deep in a sweep. The mapmap
// row (want=false) is the cell the prototype fold mis-modeled.
func TestFuzzerBridgeEmptyForm(t *testing.T) {
	str := &td{kind: "struct", stName: "S", fields: []tdField{
		{name: "F0", tomlKey: "f0", t: &td{kind: "scalar", scalar: "string"}},
	}}
	scal := &td{kind: "scalar", scalar: "int"}
	cases := []struct {
		name string
		t    *td
		want bool // has a present-empty TOML form
	}{
		{"[]scalar", &td{kind: "slice", elem: scal}, true},
		{"[]*scalar", &td{kind: "slice", elem: &td{kind: "ptr", elem: scal}}, true},
		{"[]struct", &td{kind: "slice", elem: str}, false},
		{"[]*struct", &td{kind: "slice", elem: &td{kind: "ptr", elem: str}}, false},
		{"map[string]scalar", &td{kind: "map", elem: &td{kind: "scalar", scalar: "string"}}, true},
		{"map[string]struct", &td{kind: "map", elem: str}, false},
		{"map[string]*struct", &td{kind: "map", elem: &td{kind: "ptr", elem: str}}, false},
		{"mapmap", &td{kind: "mapmap", stName: "M"}, false},
		{"namedmapstruct", &td{kind: "namedmapstruct", stName: "NM", elem: str}, false},
		{"scalar", scal, false},
		{"ptr scalar", &td{kind: "ptr", elem: scal}, false},
	}
	for _, c := range cases {
		if got := reprOf(c.t.toSpkType(), false).EmptyDistinct; got != c.want {
			t.Errorf("%s: EmptyDistinct = %v, want %v", c.name, got, c.want)
		}
	}
}

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

// The mapmap cell the prototype missed: map[string]NamedMap (a nested map)
// encodes its entries as `[outer.key]` sub-tables, so an empty outer map emits
// nothing and round-trips to nil — it has NO present-empty form, exactly like
// map[string]struct and unlike map[string]scalar. The prototype's negated
// elemIsStructish returned EmptyDistinct=true here (a latent regression had the
// fold been wired in as-is); elemIsScalarLeaf returns false.
func TestReprNestedMapNoEmptyForm(t *testing.T) {
	mapmap := spkMap{Elem: spkMap{Elem: scalar()}} // map[string]map[string]string
	if reprOf(mapmap, false).EmptyDistinct {
		t.Error("map[string]<map>: nested entries are sub-tables — no present-empty form")
	}
	if !reprOf(spkMap{Elem: scalar()}, false).EmptyDistinct {
		t.Error("map[string]scalar: an empty [table] is a distinct present form")
	}
	if reprOf(spkMap{Elem: structOf(FieldInfo{Type: scalar()})}, false).EmptyDistinct {
		t.Error("map[string]struct: sub-tables, no present-empty form")
	}
}

// EmptyDistinct is structural (omitempty-independent); emptyRoundTrips folds in
// omitempty, which drops an empty value so it re-encodes as absence and decodes
// to nil rather than empty.
func TestReprEmptyRoundTripsOmitempty(t *testing.T) {
	sl := spkSlice{Elem: scalar()} // []scalar
	if !reprOf(sl, true).EmptyDistinct {
		t.Error("EmptyDistinct must stay structural even under omitempty")
	}
	if emptyRoundTrips(sl, true) {
		t.Error("an omitempty []scalar drops its empty value → empty does NOT round-trip")
	}
	if !emptyRoundTrips(sl, false) {
		t.Error("a non-omitempty []scalar: empty `= []` round-trips as non-nil empty (#121)")
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
