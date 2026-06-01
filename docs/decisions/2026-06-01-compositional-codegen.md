---
status: proposed
date: 2026-06-01
promotion-criteria: a decode-fold spike reproduces current renderer output on existing fixtures (byte-equivalent-or-compile-equivalent) for Scalar/Ptr/Slice/Struct
supersedes: n/a
---

# ADR: Compositional TypeExpr Codegen over Enumerated FieldKinds

## Context

Tommy's code generator has been the project's churn epicenter. Of 177 commits,
67 touch `generate/` --- ~2.4x the next-busiest directory. Of 83 filed issues,
~46 are codegen, and the bugs cluster into a handful of recurring families
rather than scattering randomly:

- **New Go type shapes the classifier didn't handle** (#22, #28, #32, #33, #34,
  #36, #47, #53, #81) --- the largest cluster. Each is the same failure
  ("unsupported type ...") for a *different* shape: a type alias, a `[]*Struct`,
  a `map[string]NamedMap`, a cross-package selector, an `internal/` re-export.
- **Container-scoping bugs** (#50, #51, #55, #62) --- codegen that assumed
  document-root scope broke when the collection was nested inside a table.
- **Dual-classifier divergence** (#32 -> #36) --- a fix in the direct-field path
  (`go/ast`) didn't cover the parallel slice-element path (`go/types`).
- **Wire-format emission bugs invisible to round-trip tests** (#65, #82).

The root cause (also diagnosed in #83) is structural, not incidental. The
generator models a field's type as a **flat enumeration** at three levels:

1. **Classification** --- `FieldInfo` (`generate/analyze.go:43`) carries a single
   `Kind FieldKind` (17 variants, `analyze.go:16-33`) plus a flag bag
   (`SlicePointer bool`, `ElemType`, `TypeName`, `InnerInfo`, ...). Composite
   shapes are folded into enum-variant + flags: `[]*Struct` is
   `Kind=FieldSliceStruct` **+** `SlicePointer=true`, never `Slice(Ptr(Struct))`.
   Classification runs through *two* parallel functions (`classifyField` over
   `go/ast`, `classifyFromType` over `go/types`) that must agree.
2. **IR** --- `generate/ir.go` defines ~16 `DecodeOp` + ~16 `EncodeOp` concrete
   types whose names bake the element type in: `GetMapStringMapStringString` is
   `Map(Map(Scalar))` as one op; `DelegateSlice` is `Slice(Delegated)` as one op.
3. **Code paths** --- each `FieldKind` must be handled consistently across
   classify-AST, classify-types, `buildDecodeOp`, `buildEncodeOp`,
   decode-render, encode-render (~6 sites).

Because the handled space is defined by *enumeration* rather than a recursive
rule, every new type *combination* a downstream consumer introduces is a new
case to hand-add in ~6 places --- and combinations nobody enumerated
(`map[string][]T`, `[][]T`, `*map[...]`, `[]map[string]X`) are hard errors today
(`analyze.go:429`, `:533`, `:937`). The driver has been real downstream
adoption: dodder's and madder's config structs keep surfacing the next
unhandled shape.

## Decision

Pivot the generator core to a **compositional design**: model a field's type as
a recursive `TypeExpr` algebra built from ~6 constructors, and define
encode/decode as a **recursive fold** over that tree, threading the TOML
container position as an explicit fold parameter.

Y-statement: *Chosen option: a recursive TypeExpr algebra + fold, because it
makes well-formed type combinations compose for free and makes nested scoping
correct by construction, accepting a load-bearing rewrite of the four core
generator files and a temporary dual-renderer migration scaffold.*

This is a refactor of internals only. The CLI surface (`tommy generate`), the
struct-tag options, the generated method signatures (`Decode<Name>`/`Encode`,
`DecodeInto`/`EncodeFrom`), and the `go/packages` loading are unchanged.

### The algebra

```go
type TypeExpr interface{ isTypeExpr() }

type Scalar    struct{ Codec ScalarCodec; GoType string; Alias *AliasInfo } // primitive | text-marshaler | custom | alias
type Ptr       struct{ Elem TypeExpr }
type Slice     struct{ Elem TypeExpr }
type Map       struct{ Elem TypeExpr }               // key is always string --- checked once, at construction
type Struct    struct{ Name string; Fields []Field } // same-package, inlinable
type Delegated struct{ PkgType, ImportPath string }  // cross-package --- opaque boundary
```

Every current `FieldKind` becomes a composition; the currently-unsupported
combinations fall out for free:

| Today (enumerated)                          | Compositional        |
|---------------------------------------------|----------------------|
| `FieldSlicePrimitive`                       | `Slice(Scalar)`      |
| `FieldSliceStruct` + `SlicePointer`         | `Slice(Ptr(Struct))` |
| `FieldMapStringStruct`                      | `Map(Struct)`        |
| `FieldMapStringMapStringString` (bespoke op)| `Map(Map(Scalar))`   |
| `FieldMapStringDelegatedStruct`             | `Map(Delegated)`     |
| `map[string][]Bar` --- *error today*        | `Map(Slice(...))`    |
| `[][]T`, `*[]T` --- *error today*           | `Slice(Slice(...))`, `Ptr(Slice(...))` |

Classification collapses to **one** recursive function over `go/types` (the
`go/ast` classifier is dropped; it exists only for historical reasons and is the
source of the #32->#36 divergence). `types.Unalias` is applied **once** at the
top of the recursion rather than per-shape.

### The fold

Decode/encode become recursive folds over `TypeExpr`. `Ptr` collapses to one
rule that wraps a nil-guard around whatever its element emits --- replacing
`FieldPointerStruct`, `FieldPointerPrimitive`, `FieldPointerDelegatedStruct`, the
`SlicePointer` flag on 6 ops, and the `InPointerTable`/`SetPointerPrimitive`
special ops. `Slice` and `Map` branch on a single predicate --- does the element
introduce a TOML table header? --- instead of one hand-written op per element
kind. `Struct` extends the container path; because the path is a fold parameter
*extended on every descent*, nested scoping (the #50/#51/#55/#62 class) is
correct by construction.

## Decision Drivers

- **Collapse the recurring "unsupported shape" bug family** by replacing
  enumeration with a recursive rule.
- **Eliminate dual-classifier divergence** by classifying off `go/types` only.
- **Make nested-container scoping correct by construction**, not per-op.
- **Enable property-based testing** --- a small generative grammar can be fuzzed
  (`decode . encode == id`, golden wire-format); a flat enum + ad-hoc fixtures
  cannot. This directly attacks the #82 round-trip-blind class.
- **Bounded risk** --- the existing 184 compile-and-run integration tests +
  wire-format bats lane are a strong safety net for a behavior-preserving
  rewrite.

## Approaches Considered

### A: Status quo --- keep enumerating (rejected)

Continue adding a `FieldKind` + IR op + 6 code-path edits per new shape.

- (+) No rewrite; each increment is small and locally testable.
- (-) The bug family is structural and recurs with every new downstream shape.
- (-) Cross-cutting concerns (pointer, scope, imports) stay smeared across every
  op, so the same fix gets duplicated inconsistently (#81 found ~8 sites).

**Rejected:** treats the symptom (each missing shape) forever, never the cause.

### B: Compositional TypeExpr + fold (chosen)

Recursive algebra + fold as described above.

- (+) Well-formed combinations compose for free; many "unsupported" walls vanish.
- (+) Single classifier; orthogonal `Ptr`; scope-by-construction.
- (+) Makes property testing natural.
- (-) Load-bearing rewrite of the highest-churn code.
- (-) TOML's value/table duality must be encoded *once, correctly* in the fold;
  a mistake there has wider blast radius than a single-op bug (but is caught
  immediately by the suite).

**Chosen:** addresses the root cause and unlocks the testing strategy that would
have caught the bugs that shipped.

### C: Compositional classifier, keep the enumerated IR (rejected)

Recursive `TypeExpr` classifier, but lower it back into today's ~32 op types.

- (+) Smaller renderer change.
- (-) The IR *is* the enumeration; `map[string]map[string]string` still needs a
  bespoke op. Keeps levels 2 and 3 of the problem.

**Rejected:** the IR enumeration is where half the duplication lives; leaving it
keeps most of the cost without the payoff.

## Consequences

### Good

- The "unsupported type shape" issue family (Cluster A) largely evaporates:
  combinations compose instead of needing enumeration.
- Nested-collection scoping bugs (Cluster C) become structurally impossible ---
  the path is threaded by the recursion.
- One classifier instead of two removes the divergence class (#32->#36).
- The renderer shrinks from ~32 op cases to a handful of node kinds.
- Property-based tests become feasible, closing the round-trip-blindness gap
  (#65, #82).

### Bad / accepted costs

- A rewrite of `analyze.go` classification, `ir.go`, `ir_build.go`,
  `ir_render.go` --- essentially the generator core minus loading and the CLI.
- A migration window with two renderers running in parallel under a
  forcing-function equivalence assertion (used as a scaffold and then deleted ---
  not left to rot as the removed multi-backend setup was).
- Risk of a fold that emits valid-but-byte-different TOML that passes round-trip
  while changing output --- must be gated by the wire-format bats lane.

### Not fixed by this decision

- **Import-path resolution for re-exported aliases (#81)** is orthogonal --- a
  `go/types` correctness problem, not an enumeration problem. Composition does
  not dissolve it, but it centralizes the decision into the single classifier
  recursion instead of ~8 sites.
- **The leaf codec set stays enumerated** (string/int/bool/float/text/custom/
  alias). This is bounded and is not where churn came from.
- **TOML's value/table duality is encoded, not eliminated** --- the
  `(constructor x position)` rule set is smaller than 32 ops but is the genuinely
  tricky part.

## Confirmation

- A throwaway spike builds `TypeExpr` + the decode fold for
  `Scalar`/`Ptr`/`Slice`/`Struct` and reproduces the current `RenderFile` output
  on a sample of existing fixtures (byte-equivalent, or compile-and-behave
  equivalent under the integration harness). This is the promotion criterion for
  moving this ADR from `proposed` to `accepted`.
- Full cutover is confirmed when the entire `generate/` integration suite and
  the `encode_wire_format.bats` lane pass against the fold-based renderer with
  the legacy renderer removed.

## More Information

- Root-cause issue: #83 (generator wire-format regressions slip through CI).
- Representative shape-handling issues: #22, #32, #36, #81.
- Scoping issues: #50, #51, #55, #62.
- Wire-format issues: #65, #82.
- Prior generator design docs: `docs/plans/2026-03-22-codegen-b-design.md`,
  `docs/plans/2026-03-25-cross-package-delegation.md`.
- Related decision: `docs/decisions/2026-03-25-validation-approach.md` (the
  `Validate()` hook is a fold parameter in the new design, not a `FieldKind`).
