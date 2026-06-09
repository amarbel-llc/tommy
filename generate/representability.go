package generate

import "fmt"

// Representability fold (ADR 2026-06-08). A fold over the TypeExpr IR that makes
// explicit which Go value shapes have a faithful TOML round-trip. It collapses
// the value-space axis the way Decompose collapsed the spelling axis: the
// boundary "which shapes round-trip" is computed once from the type algebra
// instead of being rediscovered one bug at a time.
//
// First consumer (2026-06-09): the round-trip fuzzer (roundtrip_gen_test.go)
// DERIVES its empty-variant generation policy from EmptyDistinct via the td →
// spkType bridge, instead of hand-coding "does this kind have a present-empty
// form?" in genValue. That makes the fold load-bearing — exercised on every fuzz
// sweep — so it can no longer drift from the generator the way the four
// hand-synced sites in the ADR did. The encoder / both decoders are the next
// consumers, gated on pinning the encoder's zero-value policy (ADR open Q#1).
//
// The four bug cells it must predict (plus the mapmap cell the prototype missed):
//
//   #1   empty `xs = []` into []struct collapses to nil (no present-empty form)
//   #121 empty `xs = []` into []scalar round-trips as a non-nil empty slice
//   #94  the fuzzer must know which empty/nil variants are representable
//   #122 a non-nil pointer to an all-silent struct body round-trips to nil
//   --   map[string]NamedMap (nested map) encodes as sub-tables → no present-
//        empty form, so empty↔nil — NOT distinct (the prototype mis-modeled this)

// repr is the representability descriptor for a field's TypeExpr.
type repr struct {
	// MayBeSilent reports whether SOME value of this field encodes to zero TOML
	// tokens — indistinguishable from absence on decode. A non-nil pointer to a
	// MayBeSilent body has values that do not round-trip (#122); a faithful
	// generator must not emit those silent values.
	MayBeSilent bool
	// EmptyDistinct reports whether a present-but-empty value has a TOML encoding
	// distinct from absence, so empty round-trips as non-nil empty rather than
	// collapsing to nil. True only for []scalar / map[string]scalar (`= []`, an
	// empty `[table]`); false for array-of-tables, map[string]struct, AND nested
	// maps (map[string]NamedMap) — all of which realize their entries as
	// sub-tables with no empty-container form (#1/#94, and the mapmap cell).
	//
	// EmptyDistinct is deliberately STRUCTURAL (omitempty-independent): it answers
	// "is `= []` a distinct value from absence?", which is a property of the input
	// syntax the decoder must honor regardless of the field's tag. The
	// round-trip/generation question ("will a present-empty value survive?") folds
	// in omitempty — see emptyRoundTrips.
	EmptyDistinct bool
}

// reprOf folds the representability model over a field's TypeExpr. omitEmpty is
// the field's `,omitempty` tag, which widens the silent set (a zero/empty value
// is dropped entirely) and so feeds MayBeSilent — but NOT EmptyDistinct, which
// is structural (see repr.EmptyDistinct).
//
// PROTOTYPE ASSUMPTION: a non-omitempty scalar always emits its `key = value`
// (even the zero value), which is what the round-trip fuzzer relies on — it
// fills scalars with non-zero samples. The reflection encoder's additional
// "skip a zero value that isn't already in the document" rule is deliberately
// NOT modelled here; reconciling the generated and reflection encoders'
// zero-value policy is exactly the hidden contract this fold is meant to force
// into the open (ADR open question #1).
//
// The switch is exhaustive over the six TypeExpr constructors and panics on an
// unhandled one: adding a constructor to the algebra (typeexpr.go) must force a
// deliberate representability decision here rather than silently inheriting a
// worst-case default that hides the next value-space bug. reprOf is called only
// from tests/the fuzzer today, so the panic can only ever fire under test.
func reprOf(t spkType, omitEmpty bool) repr {
	switch s := t.(type) {
	case spkScalar:
		// A non-omitempty scalar always witnesses its key; an omitempty one drops
		// its zero value (silent). A scalar has no empty-container form.
		return repr{MayBeSilent: omitEmpty, EmptyDistinct: false}
	case spkPtr:
		// A nil pointer is silent; the field is therefore always potentially silent.
		return repr{MayBeSilent: true, EmptyDistinct: false}
	case spkSlice:
		// A nil slice is silent (omitted). A []scalar / []*scalar has a present-empty
		// form (`= []`) distinct from nil; an array-of-tables ([]struct / []*struct)
		// does not.
		return repr{MayBeSilent: true, EmptyDistinct: elemIsScalarLeaf(s.Elem)}
	case spkMap:
		// A nil map is silent. map[string]scalar has a present-empty form (an empty
		// `[table]`); map[string]struct and map[string]<map> (nested) realize their
		// entries as `[key]` / `[outer.key]` sub-tables with no empty-container form.
		return repr{MayBeSilent: true, EmptyDistinct: elemIsScalarLeaf(s.Elem)}
	case spkStruct:
		return repr{MayBeSilent: structMayBeSilent(s.InnerInfo), EmptyDistinct: false}
	case spkDelegated:
		return repr{MayBeSilent: structMayBeSilent(s.InnerInfo), EmptyDistinct: false}
	}
	panic(fmt.Sprintf("reprOf: unhandled TypeExpr constructor %T (add a representability decision in representability.go)", t))
}

// emptyRoundTrips reports whether a present-but-empty value of this field decodes
// back to a non-nil empty (rather than collapsing to nil) — the predicate a
// faithful generator uses to decide whether to emit the empty-collection variant.
// It is EmptyDistinct narrowed by omitempty: omitempty drops the empty value, so
// it re-encodes identically to absence and round-trips to nil, not empty.
func emptyRoundTrips(t spkType, omitEmpty bool) bool {
	return !omitEmpty && reprOf(t, omitEmpty).EmptyDistinct
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

// elemIsScalarLeaf reports whether a slice/map element encodes as a scalar leaf
// (so the enclosing container has a present-empty form: `= []` for a slice, an
// empty `[table]` for a map) rather than as a sub-table / array-of-tables (which
// has no empty-container form). Scalars and pointers-to-scalars are leaves;
// structs, delegated structs, and NESTED maps/slices are not — their entries are
// realized as their own sub-tables, so an empty outer container emits nothing and
// collapses to nil. (The prototype's negated elemIsStructish missed the nested-map
// case, mis-flagging map[string]NamedMap as having a present-empty form.)
func elemIsScalarLeaf(t spkType) bool {
	switch s := t.(type) {
	case spkScalar:
		return true
	case spkPtr:
		return elemIsScalarLeaf(s.Elem)
	case spkSlice, spkMap, spkStruct, spkDelegated:
		return false
	}
	panic(fmt.Sprintf("elemIsScalarLeaf: unhandled TypeExpr constructor %T", t))
}

// pointerStructRoundTrips reports whether EVERY non-nil value of *struct(si)
// round-trips. False means some non-nil pointer — one whose struct body is in an
// all-silent state — collapses to nil on decode (#122): the encoder cannot
// witness its presence and a faithful generator must not produce it.
func pointerStructRoundTrips(si *StructInfo) bool {
	return !structMayBeSilent(si)
}
