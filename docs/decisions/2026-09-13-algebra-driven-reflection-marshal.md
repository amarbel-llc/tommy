---
status: proposed
date: 2026-09-13
supersedes: n/a (extends 2026-06-01-compositional-codegen and 2026-06-08-representability-in-the-type-ir to pkg/marshal)
---

# ADR: Drive the reflection marshal from the TypeExpr algebra

## Context

tommy has two encoders/decoders for Go structs:

- **Codegen** (`generate/`): classifies each field into the compositional
  TypeExpr algebra (`typeexpr.go`: Scalar/Ptr/Slice/Map/Struct/Delegated), folds
  over it into IR (`comp_build.go`), and renders encode/decode. Position — root,
  nested table, array-table entry, map sub-table — is carried once, by
  `compPos.scoped` and the container node `cv`, and table placement goes through
  shared `cst` helpers (`EnsureChildTable`, `AppendArrayTableEntryAfter`,
  `ChildScope`). The representability fold (`representability.go`) and its
  conformance harness pin value-space behavior against generated code.
- **Reflection marshal** (`pkg/marshal`): dispatches on `reflect.Kind`
  separately in each position (`decodeFieldValue`, `encodeField`,
  `encodeStructSliceField`), with no shared shape model.

In September 2026, moving clown's `profiles.toml` onto `pkg/marshal`
(clown#238) exposed a run of reflection-only bugs, all silent (wrong bytes, no
error), none of which codegen had:

- maps unsupported at all (tommy#141, 934349e);
- primitive slices dropped inside `[[array]]` entries;
- map sub-tables placed after the next `[[array]]` entry on append, and left
  orphaned on shrink (b508df4, via `document.AppendArrayTableEntry` /
  `RemoveArrayTableEntry`);
- `omitempty` ignored (51161f9).

Each was a missing `reflect.Kind` arm in one position. The shape × position
matrix is re-implemented by hand in `pkg/marshal`, so it drifts from codegen
one cell at a time — the enumeration problem the 2026-06-01 ADR removed from
codegen.

## Decision (proposed)

Rebuild `pkg/marshal`'s encode and decode as folds over the same TypeExpr
algebra codegen uses:

1. **Runtime classifier**: `reflect.Type → spkType`, mirroring
   `classifyTypeExpr` in `analyze.go` (which works on `go/types`). Delegation
   does not apply at runtime: a cross-package struct is just `spkStruct`.
   Unsupported shapes return the same errors codegen reports.
2. **One encode fold and one decode fold** over `spkType` × container node,
   carrying position the way `compPos` does. Encode uses the same `cst` helpers
   as the generated code; decode keeps reading the `cst.Decompose` value model
   (already true today).
3. **Representability**: `pkg/marshal` behavior is defined by `reprOf`; no
   separate nil/empty/`omitempty` rules.
4. **Differential test** (the real safety net): for the round-trip fuzzers'
   generated shapes and values, assert that `pkg/marshal` and the generated code
   produce the same decoded values and equivalent TOML from the same input
   document.

## Consequences

- (+) A shape or position fixed in codegen's algebra is fixed for the
  reflection path too; the bug class above cannot recur independently.
- (+) `pkg/marshal` gains every shape codegen supports (pointers, nested
  arrays, TextMarshaler, sized ints) instead of its current subset.
- (−) Scope: replaces most of `marshal.go` (~350 of ~520 lines) plus the
  differential test.
- (−) Behavior changes for current callers:
  - shapes that are silently skipped today become errors or start encoding;
  - codegen rewrites a string map's table (`DeleteAllValues`), while
    `pkg/marshal` updates keys in place and keeps their comments. The ADR must
    pick one behavior for both paths (or keep the difference deliberately and
    pin it).
- (−) TextMarshaler/`TOMLMarshaler` codecs and pointer/nil semantics must be
  re-expressed through reflection and checked against the conformance cells.

## Open questions

- Should `pkg/marshal` share `generate`'s classifier and folds directly (moving
  them out of `generate/` into an internal package), or mirror them?
- Is the differential test run per merge (like `go-generate`) or only in the
  fuzz sweep?
- Which of today's `pkg/marshal` callers depend on silently-skipped shapes?

## Status

Proposed. No implementation until approved.
