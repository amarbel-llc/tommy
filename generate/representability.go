package generate

// Representability fold (ADR 2026-06-08; sharpened 2026-06-09). A fold over the
// TypeExpr IR that makes explicit which Go value shapes have a faithful TOML
// round-trip under the GENERATED encoder/decoder pair. Unlike the first
// prototype — which reasoned from assumed encoder behavior — this version models
// the renderer as it actually is, and is held to that empirically by the
// conformance harness (representability_conformance_test.go): every claim below
// is asserted against compiled generated code, so the model cannot silently
// drift from the implementation.
//
// The round-trip law it makes checkable, per field shape T:
//
//	decode(encode(v)) == v   for all v of T   iff   silent(T) ⊆ {absent(T)}
//	                                                and empty witnesses where distinct
//
// where absent(T) is the value decode produces when the key is missing (the Go
// zero value: zero scalar, nil pointer/slice/map, zero struct), and silent(T) is
// the set of values the encoder emits zero tokens for. Silence is harmless
// exactly when the silent value IS the absent-default (a zero scalar); it is a
// lossy cell when it isn't (a non-nil pointer, a present-empty collection).
//
// Encoder facts the fold encodes (each pinned by a conformance cell):
//
//   - A non-omitempty zero SCALAR is suppressed unless the key already exists in
//     the document (compSetPrimitive) — silent, but faithful: absent decodes to
//     the same zero. (This also makes encode output depend on the PRIOR document
//     state — see the ADR's document-state axis.)
//   - A non-nil POINTER SCALAR always emits, zero or not.
//   - A struct field (value or pointer) witnesses its presence with its [table]
//     header — EXCEPT when every child is a root-relative array-table, where the
//     #89 no-spurious-bare-section rule skips the header (compEncTable /
//     compEncNilGuard / compEncodeAllArrayTables). Behind a pointer that
//     exception is the #122 lossy cell: an all-silent body collapses to nil.
//     Delegated structs always emit their header (compEncDelStruct).
//   - []scalar and map[string]scalar witness present-empty (`= []`, a bare
//     `[table]`); []struct, map[string]struct and map[string]NamedMap do NOT
//     (their encoders are entry-driven: zero entries, zero tokens) — even though
//     the DECODERS read those present-empty forms (compModelEmptyArrayLeaf, the
//     VTable map readers). That encode/decode asymmetry is the #1/#94 cell.
//   - Nil container ELEMENTS are skipped on encode (TOML has no null):
//     []*scalar, []*struct, map[string]*struct all drop nil entries.
//
// Position matters: the #89 header skip applies only to fields reached through
// root-relative table nesting. Inside an array-table entry or a map sub-table
// the entry/sub-table header always witnesses, so the same struct shape can be
// lossy at the document root and faithful inside a [[list]] entry. The fold
// threads that as the `scoped` parameter.

// repr is the representability descriptor for a field's TypeExpr.
type repr struct {
	// MayBeSilent reports whether SOME value of this field encodes to zero TOML
	// tokens into an empty document — indistinguishable from absence on decode.
	MayBeSilent bool
	// SilentFaithful reports whether every silent value equals the decode-absent
	// default, i.e. silence never loses information. A zero scalar is silent and
	// faithful; a non-nil *AllArrayTables body is silent and NOT faithful (#122);
	// a present-empty []struct is silent and NOT faithful (#1).
	SilentFaithful bool
	// EncodeWitnessesEmpty reports whether the ENCODER emits a present-empty
	// form for an empty (non-nil) collection: `= []` / a bare `[table]`. True
	// for []scalar and map[string]scalar; false for array-of-tables and
	// struct-/named-map-valued maps, whose encoders are entry-driven.
	EncodeWitnessesEmpty bool
	// DecodeReadsEmpty reports whether the DECODER maps an explicit present-empty
	// form to a non-nil empty collection. True for every collection kind — the
	// decoders gained the empty forms in #1/#94 — which is what makes
	// EncodeWitnessesEmpty=false an asymmetry rather than a shared limitation.
	DecodeReadsEmpty bool
	// FullyFaithful reports whether EVERY value of this shape round-trips
	// (decode∘encode == id from an empty document). This is the predicate the
	// fuzzer's generation policy derives from: a fully-faithful shape needs no
	// value exclusions at all. One exemption from the quantifier: values holding
	// nil container ELEMENTS ([]*scalar{nil} as much as []*struct{nil}) — TOML
	// has no null, encode skips them, and no faithful generator produces them.
	FullyFaithful bool
}

// reprOf folds the representability model over a field's TypeExpr. omitEmpty is
// the field's `,omitempty` tag, which widens the silent set (a zero/empty value
// is dropped entirely — a deliberate, user-requested collapse, so it shows up
// here as lost faithfulness at empty). scoped reports whether the field lives
// inside an array-table entry or map sub-table (whose header always witnesses)
// rather than root-relative table nesting (where #89 can skip the header).
func reprOf(t spkType, omitEmpty bool, scoped bool) repr {
	switch s := t.(type) {
	case spkScalar:
		// Zero suppression makes the zero value silent even without omitempty,
		// but absent decodes to the same zero: silent yet faithful either way.
		return repr{MayBeSilent: true, SilentFaithful: true, FullyFaithful: true}
	case spkPtr:
		return reprPtr(s, scoped)
	case spkSlice:
		if elemIsStructish(s.Elem) {
			// Array-of-tables: entry-driven encode → empty is silent and collapses
			// to nil, though decode reads an explicit `= []` (#1/#94). A pointer
			// element can additionally be nil, which encode skips (lossy).
			return repr{MayBeSilent: true, SilentFaithful: false,
				EncodeWitnessesEmpty: false, DecodeReadsEmpty: true, FullyFaithful: false}
		}
		// []scalar: nil omits (faithful), empty emits `= []` — unless omitempty,
		// which collapses empty to absent (decodes nil ≠ empty: lossy at empty).
		return repr{MayBeSilent: true, SilentFaithful: !omitEmpty,
			EncodeWitnessesEmpty: !omitEmpty, DecodeReadsEmpty: true, FullyFaithful: !omitEmpty}
	case spkMap:
		_, elemIsMap := s.Elem.(spkMap) // map[string]NamedMap: entry-driven too
		if elemIsStructish(s.Elem) || elemIsMap {
			// map[string]struct / map[string]NamedMap: entry-driven encode
			// (compEncMapStruct / compEncMapMap gate on len > 0) → empty silent,
			// though decode materializes a non-nil map from a bare [table].
			return repr{MayBeSilent: true, SilentFaithful: false,
				EncodeWitnessesEmpty: false, DecodeReadsEmpty: true, FullyFaithful: false}
		}
		// map[string]scalar: nil omits, non-nil (incl. empty) emits its [table]
		// header. compSetMapScalar has no omitempty branch, so the tag does not
		// widen the silent set here.
		return repr{MayBeSilent: true, SilentFaithful: true,
			EncodeWitnessesEmpty: true, DecodeReadsEmpty: true, FullyFaithful: true}
	case spkStruct:
		// A VALUE struct field is always faithful in itself: when its header is
		// emitted it witnesses; when #89 skips the header the silent value is an
		// all-absent-default body, which is exactly what decode-absent produces.
		// Lossiness can only come from its children.
		return repr{
			MayBeSilent:    structHeaderSkipped(s.InnerInfo, scoped),
			SilentFaithful: true,
			FullyFaithful:  structFullyFaithful(s.InnerInfo, scoped),
		}
	case spkDelegated:
		// Delegated structs always emit their table header on encode.
		return repr{MayBeSilent: false, SilentFaithful: true,
			FullyFaithful: structFullyFaithful(s.InnerInfo, true)}
	}
	return repr{MayBeSilent: true}
}

// reprPtr models pointer fields. Nil is silent and faithful (absent decodes to
// nil). A NON-nil pointer witnesses via its scalar value or its struct table
// header — except a root-relative same-package struct whose children are all
// array-tables (#89 skips the header): values with an all-silent body then
// collapse to nil on decode, the #122 cell.
func reprPtr(p spkPtr, scoped bool) repr {
	switch e := p.Elem.(type) {
	case spkStruct:
		if structHeaderSkipped(e.InnerInfo, scoped) {
			return repr{MayBeSilent: true, SilentFaithful: false, FullyFaithful: false}
		}
		return repr{MayBeSilent: true, SilentFaithful: true,
			FullyFaithful: structFullyFaithful(e.InnerInfo, scoped)}
	case spkDelegated:
		return repr{MayBeSilent: true, SilentFaithful: true,
			FullyFaithful: structFullyFaithful(e.InnerInfo, true)}
	default:
		// *scalar: a non-nil pointer always emits `key = value`, zero included
		// (compSetPrimitive's pointer path has no zero suppression).
		return repr{MayBeSilent: true, SilentFaithful: true, FullyFaithful: true}
	}
}

// structHeaderSkipped mirrors compEncodeAllArrayTables: a root-relative struct
// whose every field is an array-of-tables (same-package or delegated, possibly
// of pointer elements) gets no parent [table] header on encode — the [[a.b]]
// headers imply it when entries exist, and nothing witnesses when they don't.
// Scoped structs (inside an array-table entry or map sub-table) always keep
// their header. nil InnerInfo (an unresolved skeleton) assumes the worst case.
//
// This skip is also the ONLY way a struct field can be silent: with its header
// emitted a struct always witnesses regardless of its body. (The conformance
// harness disproved the first prototype's AND-over-fields silence rule — a
// struct holding only a nil *Server still witnesses via its own [table].)
func structHeaderSkipped(si *StructInfo, scoped bool) bool {
	if scoped {
		return false
	}
	if si == nil {
		return true
	}
	if len(si.Fields) == 0 {
		return false
	}
	for _, f := range si.Fields {
		if !isSliceOfStruct(f.Type) {
			return false
		}
	}
	return true
}

// structFullyFaithful reports whether every value of the struct round-trips:
// the conjunction of its fields' faithfulness. Fields of a value/pointer struct
// keep their parent's position (table nesting extends the path); the entry
// bodies of array-tables and struct-maps are scoped by their own reprOf cases.
func structFullyFaithful(si *StructInfo, scoped bool) bool {
	if si == nil {
		return false // unresolved skeleton: cannot vouch for the body
	}
	for _, f := range si.Fields {
		if !reprOf(f.Type, f.OmitEmpty, scoped).FullyFaithful {
			return false
		}
	}
	return true
}

// elemIsStructish reports whether a slice/map element decodes as an array-of-
// tables / sub-table (which the encoder gives no present-empty form) rather
// than a scalar leaf.
func elemIsStructish(t spkType) bool {
	switch s := t.(type) {
	case spkStruct, spkDelegated:
		return true
	case spkPtr:
		return elemIsStructish(s.Elem)
	default:
		return false
	}
}
