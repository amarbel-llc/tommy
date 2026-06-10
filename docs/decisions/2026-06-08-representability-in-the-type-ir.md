---
status: proposed
date: 2026-06-08
supersedes: n/a (complements 2026-06-07-decode-normalization)
---

> **Draft for review (2026-06-08; examined and partially realized 2026-06-09).**
> The motivating evidence (#1, #94, #121, #122, #123) is already fixed/filed;
> this ADR records the *structural* direction those point at so it is decided
> deliberately rather than rediscovered one cell at a time. Realized so far:
> the differential-decode test (commit e470a8b), the representability fold
> (`generate/representability.go`) — rewritten 2026-06-09 to model the actual
> encoder after an empirical probe falsified parts of the first prototype —
> the cell-by-cell **conformance harness**
> (`representability_conformance_test.go`) gating model against generated-code
> reality, and the fuzzers' nil/empty generation policy now **derived from the
> fold**. Still proposed: deriving the encoder/decoder themselves, closing the
> encode-side empty witnesses, and the document-state axis. See Examination
> findings (2026-06-09) below.

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
  *(2026-06-09: empirically disproven — see below. The encoder's table header
  witnesses; the AND-over-fields rule was reasoning from an imagined encoder.)*
- **The fold is type-level (worst-case "CAN be silent"); the fuzzer additionally
  needs a value-level check** ("does THIS value witness?"), or must restrict
  generation to the always-witnessing region. So the model yields a *predicate*
  for the encoder/decoder/exclusions and a *generation obligation* for the fuzzer.
- **The one load-bearing assumption is the encoder's zero-value policy** — see
  the sharpened open question below. Everything else fell out cleanly.

## Examination findings (2026-06-09): model the encoder we have, gate it empirically

A second pass replaced reasoning-from-assumptions with an **empirical probe of
every boundary cell against compiled generated code**, now committed as the
standing conformance harness
(`generate/representability_conformance_test.go`). The probe falsified parts
of the first prototype and sharpened the theory. The fold
(`representability.go`) was rewritten to model the renderer as it actually is.

### The sharpened law

Per field shape `T`, let `absent(T)` be what decode produces for a missing key
(the Go zero: zero scalar, nil pointer/slice/map, zero struct) and `silent(T)`
the set of values the encoder emits zero tokens for. Then:

> every value of `T` round-trips  **iff**  `silent(T) ⊆ {absent(T)}`
> and present-empty is witnessed where it is distinct.

`MayBeSilent` alone was the wrong axis: silence is *harmless* when the silent
value IS the absent-default (a zero scalar), and lossy only when it isn't (a
non-nil pointer, a present-empty collection). The fold now carries
`SilentFaithful` for exactly this distinction, plus a per-direction split of
the old `EmptyDistinct` (below), and a composed `FullyFaithful` predicate.

### What the probe established (each pinned by a conformance cell)

1. **Zero scalars are silent but faithful.** A non-omitempty zero scalar emits
   nothing into an empty document (`compSetPrimitive` gates on
   `zero || HasValue`) and decodes back to zero. The prototype's "a
   non-omitempty scalar always emits" assumption was wrong, yet harmless —
   which is why no fuzzer ever caught it. Pointer scalars DO always emit
   (`pb = false`). The zero-value policy open question below is therefore
   answered: the generated encoder's policy is *suppress-unless-present*, and
   it is faithful; what remains open is only marshal parity (#123).
2. **The struct table header is the presence witness.** `&Wrapper{S: nil}`
   emits `[w]` and round-trips — the AND-over-fields silence rule is false.
   The ONLY silent struct is the #89 shape: every field a root-relative
   array-of-tables, where the header is skipped (`compEncodeAllArrayTables`).
   **#122 is precisely the #89 cosmetic rule applied behind a pointer**, where
   silence stops being faithful (`absent = nil ≠ &AllTables{}`). The cell is
   *positional*: the same shape inside a `[[list]]` entry or map sub-table
   keeps its header and is faithful — so the fold threads a `scoped`
   parameter, mirroring the position parameter the codegen folds thread.
3. **The empty-collection asymmetry is encode-only.** The DECODERS already
   read every present-empty form (`xs = []` → non-nil empty `[]struct` via
   `compModelEmptyArrayLeaf`; a bare `[ms]` → non-nil empty
   `map[string]struct`). Only the ENCODERS are entry-driven (`len > 0` gates
   in `compEncMapStruct`/`compEncMapMap`/the array-table loops) and never emit
   those forms. So the #1/#94 "unrepresentable" cells are not TOML
   limitations — they are one-sided encoder suppression, and closing them is
   an encoder-only change the decoders are already prepared for. `EmptyDistinct`
   accordingly split into `EncodeWitnessesEmpty` / `DecodeReadsEmpty`.
4. **`omitempty` is a leaf-only option.** `comp_build` threads `OmitEmpty`
   into `ceLeaf` only; an omitempty scalar map still witnesses present-empty.
5. **Nil container elements were a live panic, and the two container kinds
   disagree.** Encoding `[]*Struct{nil, …}` dereferenced nil and panicked
   (fixed 2026-06-09: skipped, matching `compSlicePrimSet`/`compEncMapStruct`
   policy). But the map path creates the `[msp.k]` sub-table BEFORE its nil
   check, so a nil map entry round-trips as a non-nil empty struct instead of
   dropping — pinned in the harness as a (questionable) current policy.

### Bugs the probe surfaced beyond the value-space axis

- **Handle-type collision** (fixed 2026-06-09): two top-level `[]X` fields
  sharing an element struct type generated two `xHandle` declarations — a
  compile failure. Handle types are now named after the field.
- **Stale array-table entries on shrink** (OPEN): decode a document with three
  `[[servers]]` entries, truncate the slice to one, encode — all three entries
  survive, because encode reuses found entries per index and never deletes the
  tail. This is an instance of a whole untested axis: **encode output is a
  function of (value, prior document)** — the `HasValue` zero-scalar rule and
  entry reuse both read the prior document — but every fuzzer starts from an
  empty document, so the second parameter is generatively unexercised.
  Closing it needs a decode→mutate→encode harness (and an array-table
  truncation fix).

### Consumers wired so far

- **The conformance harness** is the empirical gate: each cell records its
  TypeExpr shape, which model axis decides it, and the verified outcome; the
  test fails on either model drift (`predict(reprOf(shape)) != faithful`) or
  renderer drift (generated-code behavior changes). Lossy cells are pinned, so
  closing one later consciously flips the cell and the model together.
- **The round-trip fuzzers' generation policy now DERIVES from the fold**
  (`tdToSpk` + `reprOf` in `roundtrip_gen_test.go`), retiring the hand-coded
  exclusion switch — which was a fifth hand-synced copy of the rules, written
  in the fuzzer's own parallel `td` mini-algebra. Nil/empty variants are
  generated exactly where the fold says they round-trip, which also *widened*
  coverage (nil array-tables were previously never generated anywhere; now
  they are everywhere except the guarded #122 position).

### Remaining steps (in order of leverage)

1. **Close the encode-side empty witnesses** (the #1/#94 cells): emit `= []`
   for present-empty array-of-tables and a bare `[table]` for present-empty
   struct-/named-map-valued maps. Decoders already read both; the conformance
   cells and fold flip with the change. This shrinks the genuinely
   unrepresentable residue to: nil container elements (TOML has no null) and
   the deliberate omitempty collapse.
2. **Re-examine the #89 header skip behind pointers**: emitting the parent
   table header when a non-nil pointer struct's body is otherwise silent would
   close #122 (the decoder already materializes `&S{}` from a bare header).
   Cosmetic cost: a bare `[at]` section when entries exist is avoidable by
   emitting it only when all array-table fields are empty.
3. **The document-state axis**: a decode→mutate→encode generative harness, and
   the array-table truncation fix it will immediately find.
4. **Marshal parity (#123)** and differential fuzzing over the common subset,
   as already proposed above.

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
