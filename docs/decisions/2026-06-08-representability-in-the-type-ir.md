---
status: proposed
date: 2026-06-08
supersedes: n/a (complements 2026-06-07-decode-normalization)
---

> **Draft for review (2026-06-08).** Proposed, not implemented as production.
> The motivating evidence (#1, #94, #121, #122, #123) is already fixed/filed;
> this ADR records the *structural* direction those point at so it is decided
> deliberately rather than rediscovered one cell at a time. Two concrete steps
> already exist: the differential-decode test (commit e470a8b) is the empirical
> gate, and a **prototype representability fold** (`generate/representability.go`
> + test) validates that the model is a clean static computation — see Prototype
> findings below.

# ADR: Model representability in the type IR (collapse the value-space axis)

## Context

The 2026-06-07 ADR collapsed the **spelling axis** (`Decompose` → `Value`): one
semantic value has many TOML textual encodings, so normalize them once instead
of matching each per kind. That worked.

A *third* axis of churn remains, orthogonal to both kinds and spelling: the
**value-space / representability axis**. Go's value space is strictly richer
than TOML's, and the mapping Go → TOML → Go is lossy in specific, predictable
cells:

| Go shape | Go values | faithfully representable in TOML? |
|---|---|---|
| `*T` | nil, `&v` | yes — *iff* `v` itself emits something (else `&empty` → nil) |
| `[]scalar` | nil, empty, `[v…]` | yes — absent↔nil, `= []`↔empty, `= [v…]` |
| `[]struct` | nil, empty, `[v…]` | **no** — array-of-tables has no empty form; empty↔nil |
| `map[string]struct` | nil, empty, `{…}` | **no** — no distinct empty form; empty↔nil |
| any container *element* | …, nil | **no** — TOML has no null |

Every value-space bug this cycle lives in the right-hand column:

- **#1** — empty `xs = []` into `[]struct`: the two decoders disagreed (marshal
  errored; codegen left nil + leaked an undecoded key).
- **#121** — empty `[]string`: reflection returned nil, codegen non-nil.
- **#94** — the fuzzer never generated empty/nil collections (blind spot).
- **#122** — when widened, the fuzzer *over*-generated an unrepresentable value
  (`&Struct{}` with an all-empty body → encodes to nothing → round-trips nil).
- **#123** — the two decoders don't even implement the same type set.

The representability boundary — *which Go shapes round-trip* — is currently
encoded **implicitly and redundantly** in at least four hand-synced places:

1. the **encoder**'s suppression rules (`omitempty`, the #89 no-spurious-bare-
   section choice, zero-value skipping),
2. the **decoder**'s normalization (#21 present-empty↔non-nil, absent↔nil),
3. the **fuzzer generator**'s exclusions (don't emit empty `[]struct`, don't nil
   a struct's sole field, …),
4. the **capability gap** between the generated decoder and reflection `marshal`
   (#123).

Closing the cross-product one cell at a time is the same cat-and-mouse the
spelling ADR diagnosed — on a different axis. The findings are *converging*
(each is more obscure than the last), which says the design is sound but
**under-modeled**: representability is emergent, not first-class.

## Decision (proposed)

Make representability a **first-class property of the type IR**. Each `TypeExpr`
node (`typeexpr.go`) gains — directly or via a derived fold — a description of:

- its **value-space cardinality** (does it distinguish nil / empty / populated /
  absent?), and
- the **TOML-representable subset** of that space, plus the canonical
  `absent ↔ value` mapping.

That single annotation becomes the one source of truth the four sites above
*derive* from instead of hand-coding:

```
TypeExpr ─► representability fold ─┬─► encoder: emit a presence-witness or omit?
                                   ├─► decoder: absent → nil or empty?
                                   ├─► fuzzer:  which {nil, empty, populated} variants are valid to emit?
                                   └─► predicate roundTrippable(shape)   (#122)
```

The round-trip law becomes explicit and *checkable*:

> `decode(encode(v)) == v`  **iff**  `v ∈ representable(shapeOf(v))`,
> where `representable` is computed from the IR.

The fuzzer then generates exactly the representable subset (no rediscovered
exclusions); the encoder and both decoders agree by construction because they
read the same annotation.

## What we keep

- The **`TypeExpr` structural algebra** (`Scalar/Ptr/Slice/Map/Struct/Delegated`)
  — sound; this *enriches* its nodes, it does not replace them.
- **`Decompose` / `Value`** (the spelling-axis normalization) — orthogonal and
  complementary: spelling is about input syntax, representability about value
  space. They compose.
- Lexer / CST / document / **encoder** (canonical single-spelling emit).

## What changes

- Add the representability annotation/fold over `TypeExpr`.
- Re-express the encoder's suppression, the decoder's nil/empty normalization,
  and the fuzzer's generation policy as *consumers* of that annotation rather
  than independent hand-written rules.

## Empirical gate

- **Differential decode** (commit e470a8b, prototype): decode the same TOML via
  the generated decoder *and* reflection `marshal`, assert agreement. It catches
  the #1/#121 "two paths disagree" class directly — the class round-trip fuzzing
  structurally cannot see (it only decodes encoder output; cf. #107).
- Graduate it to **differential fuzzing**: the two decoders as mutual oracles
  over the common subset — an oracle-free attack on #107 — and, with the explicit
  representability model, a generator that emits the *full* representable subset.

## Prototype findings (2026-06-08)

`generate/representability.go` + `representability_test.go` implement the fold
and validate it against all four cells. What it confirmed and surfaced:

- **It is a clean static recursion** (~50 lines). Representability is not one
  property but **two orthogonal axes**, and the fold computes both:
  - `MayBeSilent` — *some* value encodes to zero tokens (collapses to absent).
    Drives the #122 pointer case and the #1 array-of-tables collapse.
  - `EmptyDistinct` — present-empty has a TOML form distinct from absent.
    True for `[]scalar` / `map[string]scalar`, false for array-of-tables /
    `map[string]struct`. Drives #121 vs #1.
- **The recursive crux is the struct node:** `structMayBeSilent = AND over fields`
  (a struct can be silent iff every field can be). It is bottom-up and composes
  correctly through nesting and pointers — the prototype's `Wrapper{*Server}`
  case is silent-able even though `Server` alone always witnesses. From it,
  `pointerStructRoundTrips(S) = !structMayBeSilent(S)` predicts #122 exactly
  (`*Sub7{F0 []*Sub8}` → does not round-trip; `*Server{Host string}` → does).
- **The fold is type-level (worst-case "CAN be silent"); the fuzzer additionally
  needs a value-level check** ("does THIS value witness?"), or must restrict
  generation to the always-witnessing region. So the model yields a *predicate*
  for the encoder/decoder/exclusions and a *generation obligation* for the fuzzer.
- **The one load-bearing assumption is the encoder's zero-value policy** — see
  the sharpened open question below. Everything else fell out cleanly.

## Risks / open questions

- **Decoder capability gap (#123).** Differential testing only works on the
  subset both decoders implement. Either bring reflection `marshal` to parity
  (reading the same `Value` model) or scope representability to the generated
  decoder and define `marshal` as an explicit subset. This ADR assumes the
  former is the eventual target.
- **Encoder zero-value policy is THE load-bearing contract (sharpened by the
  prototype).** `MayBeSilent(scalar)` hinges entirely on whether a non-omitempty
  scalar always emits. The reflection encoder skips a zero-value field that isn't
  already in the document (`encodeField`); the generated encoder may differ; the
  round-trip fuzzer sidesteps the question by only generating non-zero scalars.
  The prototype *assumes* "non-omitempty scalar always emits" to match fuzzer
  reality. The model can only be the single source of truth if both encoders
  commit to ONE zero-value policy — so **step 0 of the redesign is pinning that
  policy** (it also feeds the #123 marshal-parity decision). Until then, the fold
  is correct relative to its stated assumption, not to a unified encoder.
- **Is representability otherwise statically decidable?** Yes — the prototype
  confirms it: a function of kind composition + `omitempty`, with the struct case
  computed bottom-up (`AND` over fields). No value-level information is needed for
  the *type-level* predicate; only the fuzzer's generation obligation is
  value-level.
- **Cost vs. payoff.** The annotation adds machinery; the payoff is retiring the
  four-way hand-sync and the recurring value-space bug class. Worth prototyping
  the fold against the known cells (#1/#94/#121/#122) before committing.
- **Encode stays canonical**, so representability is purely about value space and
  does not reintroduce spelling concerns — the two ADRs remain orthogonal.
