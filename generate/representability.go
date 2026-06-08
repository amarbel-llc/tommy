package generate

// Representability fold (PROTOTYPE — ADR 2026-06-08). A fold over the TypeExpr
// IR that makes explicit which Go value shapes have a faithful TOML round-trip.
// It is not yet wired into the encoder / decoders / fuzzer; it exists to validate
// that the model is a clean static computation and to surface its hidden
// assumptions (see the ADR's open questions). The four bug cells it must predict:
//
//   #1   empty `xs = []` into []struct collapses to nil (no present-empty form)
//   #121 empty `xs = []` into []scalar round-trips as a non-nil empty slice
//   #94  the fuzzer must know which empty/nil variants are representable
//   #122 a non-nil pointer to an all-silent struct body round-trips to nil
//
// The eventual goal (ADR): the encoder's suppression, the decoders' nil/empty
// normalization, and the fuzzer's generation policy all DERIVE from this fold,
// instead of being hand-maintained in four places that drift apart.

// repr is the representability descriptor for a field's TypeExpr.
type repr struct {
	// MayBeSilent reports whether SOME value of this field encodes to zero TOML
	// tokens — indistinguishable from absence on decode. A non-nil pointer to a
	// MayBeSilent body has values that do not round-trip (#122); a faithful
	// generator must not emit those silent values.
	MayBeSilent bool
	// EmptyDistinct reports whether a present-but-empty value has a TOML encoding
	// distinct from absence, so empty round-trips as non-nil empty rather than
	// collapsing to nil. True for []scalar / map[string]scalar (`= []`, empty
	// `[table]`); false for array-of-tables and map[string]struct (#1/#94).
	EmptyDistinct bool
}

// reprOf folds the representability model over a field's TypeExpr. omitEmpty is
// the field's `,omitempty` tag, which widens the silent set (a zero/empty value
// is dropped entirely).
//
// PROTOTYPE ASSUMPTION: a non-omitempty scalar always emits its `key = value`
// (even the zero value), which is what the round-trip fuzzer relies on — it
// fills scalars with non-zero samples. The reflection encoder's additional
// "skip a zero value that isn't already in the document" rule is deliberately
// NOT modelled here; reconciling the generated and reflection encoders'
// zero-value policy is exactly the hidden contract this fold is meant to force
// into the open (ADR open question #1).
func reprOf(t spkType, omitEmpty bool) repr {
	switch s := t.(type) {
	case spkScalar:
		return repr{MayBeSilent: omitEmpty, EmptyDistinct: false}
	case spkPtr:
		// A nil pointer is silent; the field is therefore always potentially silent.
		return repr{MayBeSilent: true, EmptyDistinct: false}
	case spkSlice:
		// A nil slice is silent (omitted). A []scalar has a present-empty form
		// (`= []`) distinct from nil; an array-of-tables ([]struct / []*struct)
		// does not.
		return repr{MayBeSilent: true, EmptyDistinct: !elemIsStructish(s.Elem)}
	case spkMap:
		// A nil map is silent. map[string]scalar has a present-empty form (an empty
		// `[table]`); map[string]struct does not.
		return repr{MayBeSilent: true, EmptyDistinct: !elemIsStructish(s.Elem)}
	case spkStruct:
		return repr{MayBeSilent: structMayBeSilent(s.InnerInfo), EmptyDistinct: false}
	case spkDelegated:
		return repr{MayBeSilent: structMayBeSilent(s.InnerInfo), EmptyDistinct: false}
	}
	return repr{MayBeSilent: true}
}

// structMayBeSilent reports whether a struct can encode to zero tokens — true
// iff EVERY field can simultaneously be silent. A single always-emitting field
// (e.g. a non-omitempty scalar) makes the struct always witness its presence, so
// a non-nil pointer to it always round-trips. This is the recursive crux: the
// property is bottom-up over the struct's fields.
func structMayBeSilent(si *StructInfo) bool {
	if si == nil {
		return true // unresolved skeleton (fieldType path): assume the worst case
	}
	for _, f := range si.Fields {
		if !reprOf(f.Type, f.OmitEmpty).MayBeSilent {
			return false
		}
	}
	return true
}

// elemIsStructish reports whether a slice/map element decodes as an array-of-
// tables / sub-table (which has no present-empty form) rather than a scalar leaf.
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

// pointerStructRoundTrips reports whether EVERY non-nil value of *struct(si)
// round-trips. False means some non-nil pointer — one whose struct body is in an
// all-silent state — collapses to nil on decode (#122): the encoder cannot
// witness its presence and a faithful generator must not produce it.
func pointerStructRoundTrips(si *StructInfo) bool {
	return !structMayBeSilent(si)
}
