package generate

import "testing"

// Unit cells for the representability fold. Every expectation here is the
// empirically verified behavior of the generated encoder/decoder pair — the
// conformance harness (representability_conformance_test.go) asserts the same
// predictions against compiled generated code, so these stay honest.

func scalar() spkType { return spkScalar{Codec: codecPrim, TypeName: "string"} }

func structOf(fields ...FieldInfo) spkStruct {
	return spkStruct{TypeName: "S", InnerInfo: &StructInfo{Name: "S", Fields: fields}}
}

func TestReprScalar(t *testing.T) {
	r := reprOf(scalar(), false, false)
	if !r.MayBeSilent {
		t.Error("a non-omitempty zero scalar is suppressed when absent from the document — silent")
	}
	if !r.SilentFaithful || !r.FullyFaithful {
		t.Error("the silent value IS the decode-absent zero — silence is faithful")
	}
	if !reprOf(scalar(), true, false).FullyFaithful {
		t.Error("an omitempty scalar omits only its zero, which decodes back to zero — faithful")
	}
}

func TestReprPtrScalar(t *testing.T) {
	r := reprOf(spkPtr{Elem: scalar()}, false, false)
	if !r.MayBeSilent {
		t.Error("nil pointer is silent")
	}
	if !r.SilentFaithful || !r.FullyFaithful {
		t.Error("a non-nil *scalar always emits, zero included (`pb = false`) — fully faithful")
	}
}

// #121 vs #1: a []scalar witnesses present-empty (`= []`); an array-of-tables
// does not — its encode is entry-driven, so empty collapses to nil even though
// the DECODER reads an explicit `= []` into a non-nil empty slice.
func TestReprSliceEmptyForm(t *testing.T) {
	r := reprOf(spkSlice{Elem: scalar()}, false, false)
	if !r.EncodeWitnessesEmpty || !r.FullyFaithful {
		t.Error("[]scalar: empty `= []` must be witnessed on encode (#121)")
	}
	if reprOf(spkSlice{Elem: scalar()}, true, false).FullyFaithful {
		t.Error("[]scalar omitempty: empty collapses to absent → decodes nil ≠ empty — lossy at empty")
	}
	for _, c := range []struct {
		name string
		t    spkType
	}{
		{"[]struct", spkSlice{Elem: structOf(FieldInfo{Type: scalar()})}},
		{"[]*struct", spkSlice{Elem: spkPtr{Elem: structOf(FieldInfo{Type: scalar()})}}},
	} {
		r := reprOf(c.t, false, false)
		if r.EncodeWitnessesEmpty {
			t.Errorf("%s: encoder never emits a present-empty array-of-tables (#1)", c.name)
		}
		if !r.DecodeReadsEmpty {
			t.Errorf("%s: decoder DOES read `= []` to a non-nil empty slice — the asymmetry", c.name)
		}
		if r.FullyFaithful {
			t.Errorf("%s: empty collapses to nil — not fully faithful", c.name)
		}
	}
}

func TestReprMapEmptyForm(t *testing.T) {
	if r := reprOf(spkMap{Elem: scalar()}, false, false); !r.EncodeWitnessesEmpty || !r.FullyFaithful {
		t.Error("map[string]scalar: empty emits its bare [table] header — fully faithful")
	}
	for _, c := range []struct {
		name string
		t    spkType
	}{
		{"map[string]struct", spkMap{Elem: structOf(FieldInfo{Type: scalar()})}},
		{"map[string]NamedMap", spkMap{Elem: spkMap{Elem: scalar()}}},
	} {
		r := reprOf(c.t, false, false)
		if r.EncodeWitnessesEmpty || r.FullyFaithful {
			t.Errorf("%s: entry-driven encode gives empty no witness — lossy at empty", c.name)
		}
		if !r.DecodeReadsEmpty {
			t.Errorf("%s: decoder materializes a non-nil map from a bare [table]", c.name)
		}
	}
}

// The struct witness rule. The first prototype computed silence as AND over
// fields; the conformance harness disproved that: a struct witnesses via its
// own [table] header regardless of its body — EXCEPT when every field is an
// array-of-tables, where #89 skips the header (the #122 cell).
func TestReprStructWitness(t *testing.T) {
	server := &StructInfo{Name: "Server", Fields: []FieldInfo{
		{GoName: "Host", TomlKey: "host", Type: scalar()},
	}}

	// Wrapper{S *Server}: nil-able body, but the [w] header always witnesses —
	// &Wrapper{nil} round-trips (verified by the wrapper-nil-inner cell).
	wrapper := &StructInfo{Name: "Wrapper", Fields: []FieldInfo{
		{GoName: "S", TomlKey: "s", Type: spkPtr{Elem: spkStruct{TypeName: "Server", InnerInfo: server}}},
	}}
	if structHeaderSkipped(wrapper, false) {
		t.Error("Wrapper{*Server}: not all-array-tables — header emitted")
	}
	if r := reprOf(spkPtr{Elem: spkStruct{TypeName: "Wrapper", InnerInfo: wrapper}}, false, false); !r.FullyFaithful {
		t.Error("&Wrapper{nil} witnesses via [w] and round-trips — fully faithful")
	}

	// AllTables{Xs []Server} — the #122 shape: all fields are array-tables, the
	// header is skipped, and a non-nil pointer with a nil/empty body is silent.
	allTables := &StructInfo{Name: "AllTables", Fields: []FieldInfo{
		{GoName: "Xs", TomlKey: "xs", Type: spkSlice{Elem: spkStruct{TypeName: "Server", InnerInfo: server}}},
	}}
	if !structHeaderSkipped(allTables, false) {
		t.Error("AllTables: every field an array-of-tables — #89 skips the header")
	}
	r := reprOf(spkPtr{Elem: spkStruct{TypeName: "AllTables", InnerInfo: allTables}}, false, false)
	if !r.MayBeSilent || r.SilentFaithful || r.FullyFaithful {
		t.Error("&AllTables{} is silent and ≠ nil — the #122 lossy cell")
	}

	// The same shape inside an array-table entry is scoped: the entry header
	// witnesses, so the table header is kept and the cell disappears.
	if structHeaderSkipped(allTables, true) {
		t.Error("scoped AllTables keeps its header — the #122 cell is positional")
	}

	// A value-struct AllTables field is silent-able but FAITHFUL: the silent
	// body is all absent-defaults, exactly what decode-absent produces. Its
	// unfaithfulness comes only from the child cells (empty []struct).
	rv := reprOf(spkStruct{TypeName: "AllTables", InnerInfo: allTables}, false, false)
	if !rv.MayBeSilent || !rv.SilentFaithful {
		t.Error("value AllTables: silent body == zero value — faithful in itself")
	}
	if rv.FullyFaithful {
		t.Error("value AllTables: still not FULLY faithful — its []struct child loses empty")
	}
}

// Faithfulness composes through nesting: one lossy child poisons the struct.
func TestReprStructComposition(t *testing.T) {
	server := &StructInfo{Name: "Server", Fields: []FieldInfo{
		{GoName: "Host", TomlKey: "host", Type: scalar()},
	}}
	clean := &StructInfo{Name: "Clean", Fields: []FieldInfo{
		{GoName: "A", TomlKey: "a", Type: scalar()},
		{GoName: "B", TomlKey: "b", Type: spkSlice{Elem: scalar()}},
		{GoName: "C", TomlKey: "c", Type: spkPtr{Elem: spkStruct{TypeName: "Server", InnerInfo: server}}},
		{GoName: "D", TomlKey: "d", Type: spkMap{Elem: scalar()}},
	}}
	if !structFullyFaithful(clean, false) {
		t.Error("scalars, []scalar, *struct-with-header, map[string]scalar: all faithful")
	}

	dirty := &StructInfo{Name: "Dirty", Fields: []FieldInfo{
		{GoName: "A", TomlKey: "a", Type: scalar()},
		{GoName: "Xs", TomlKey: "xs", Type: spkSlice{Elem: spkStruct{TypeName: "Server", InnerInfo: server}}},
	}}
	if structFullyFaithful(dirty, false) {
		t.Error("one []struct field (lossy at empty) must poison the struct")
	}
}
