---
promotion-criteria: per-field validation demand from real usage
status: proposed
supersedes: n/a
---

# ADR: Struct-Level Validation Before Per-Field

## Context

Tommy's code generator needs to support user-defined validation. The question is
where validation checks are injected in generated code.

## Decision

Implement struct-level validation first (Approach C), with per-field validation
(Approach B) as the planned evolution.

## Approaches Considered

### A: New FieldKind (FieldValidatable)

Add a new `FieldKind` for types implementing `Validate() error`.

- (+) Consistent with existing FieldCustom/FieldTextMarshaler patterns
- (-) Validation is orthogonal to serialization kind --- a `FieldPrimitive` and
  a `FieldStruct` can both be validatable. A new FieldKind creates false
  exclusivity.

**Rejected:** wrong abstraction level.

### B: Orthogonal Flag on FieldInfo

Add `Validatable bool` to `FieldInfo`. Set independently of `FieldKind`. Emit
calls `Validate()` on each field after decode.

- (+) Correctly models validation as orthogonal to serialization
- (+) Works for any field kind
- (-) More changes than needed for the initial scope

**Deferred:** right design, but premature for the first cut.

### C: Struct-Level Only (chosen)

Add `Validatable bool` to `StructInfo`. Call `Validate()` after all fields are
decoded / before encode begins. No FieldInfo changes.

- (+) Smallest change that delivers the feature
- (+) Matches the proof-of-concept scope
- (-) Doesn't cover per-field newtypes (e.g., `type Port int` used as a field)

**Chosen:** delivers value with minimal risk. Promotes to B when real usage
demands per-field validation.

## Consequences

- Users can validate structs by adding `Validate() error` --- no new tags, no
  interface imports
- Per-field newtype validation (the original motivating use case) requires
  Approach B, which is a follow-up
- The struct-level cut still enables the newtype pattern when the newtype is
  itself a struct (e.g., wrapping a primitive in a single-field struct), though
  this is less ergonomic than direct per-field validation
