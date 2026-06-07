---
status: proposed
date: 2026-06-07
promotion-criteria: the spelling fuzzer (TestRoundTripSpellingFuzz) gates ALL
  variants (canonical, inline-table-deep, dotted-key, inline-array-of-tables,
  implicit-parents) with zero xfails across the full fuzz-sweep seed set, and
  the bats + ./generate integration suites stay green, with the per-renderer
  spelling fallbacks deleted.
supersedes: n/a (complements 2026-06-01-compositional-codegen)
---

# ADR: Decode via a Normalized Value Model (collapse the spelling axis)

## Context

The 2026-06-01 ADR diagnosed the generator's churn as a **flat enumeration on
the type axis** and replaced it with a compositional `TypeExpr` algebra
(`Scalar/Ptr/Slice/Map/Struct/Delegated` → `comp_ir` → folds). That algebra is
sound and is *not* the problem here.

A second flat enumeration remains, on a different axis: **TOML surface
spelling.** TOML's value/table duality means one semantic value has several
legal textual encodings:

| value | spellings |
|---|---|
| nested struct | `[a]` header · `a = {…}` inline · `a.b = v` dotted-key · `[a.b]` with implicit `[a]` |
| map | `[m.k]` sub-table · `m = { k = {…} }` inline · `m.k = v` dotted-key |
| slice of struct | `[[xs]]` array-of-tables · `xs = [ {…} ]` inline-array |

The generated **decoder** inverts this many-to-one mapping by hand-coding, *per
type-kind*, one CST matcher per spelling: the header scan, then the #106 inline
fallback, the #89 flat-key fallback, the #113 implicit-parent branch, and (in
flight) dotted-key and inline-array. These fallbacks are duplicated across every
container renderer **and its scoped twin** (`compInTable`/`compNilGuard`/
`compMapScalar`/`compMapStruct`/`compDelStruct`/`compDelMap` × root/scoped).

So the real decode surface is not `kinds`; it is:

```
kinds  ×  spellings  ×  position (root / scoped / delegated)
```

and we have been closing that cross-product one cell at a time. The recurrence
is empirical, not hypothetical — in a single recent work session the *same*
conceptual bug ("the finder only matches the canonical header") was fixed as:

- **#115** — inline-table under an explicit parent header (×4 renderers),
- **#114** — implicit-parent decode for delegated struct/map fields (×3),
- **#117 frontier** — implicit-parent for same-package `map[string]struct`
  (`compMapStruct`, then its scoped twin `compScopedMapStruct`),

with `dotted-key` and `inline-array-of-tables` (the #108 deferred axes 2 & 3)
queued as the next rows of the very same table. Each fix teaches *one renderer*
*one spelling*. That is the cat-and-mouse.

## Decision

The spelling set is **finite and grammar-defined**, so it must be enumerated
**once** — but it does *not* need to be re-enumerated per kind per position in
generated code. Separate the two concerns with a **normalization layer between
parse and decode**, and rewrite the decode path fresh against it.

```
input ─► lexer ─► CST ─────────────────────────► kept verbatim   (encode, edits, comments)
                   │
                   └─► Decompose ─► Value model ─► type-algebra decoder  (one reader per kind)
```

- **`Decompose(root *cst.Node) (Value, error)`** — one grammar-driven pass that
  collapses *all* spelling duality into a single canonical **value model**:
  header/inline tables → `Table`; dotted keys → nested `Table`; array-of-tables
  / inline arrays → `Array`; implicit parents → materialized `Table`; scalars →
  typed/raw leaves. Duplicate-key detection (#90/#92/#102/#110) falls out for
  free — building a `Table` from two same-keyed entries is a collision → error,
  in *every* spelling at once.
- The **decoder** folds the existing `TypeExpr` algebra over the value model:
  exactly **one** reader per kind, **no** fallbacks, **no** root/scoped duality
  (the model is already a clean tree; position is just recursion). `Undecoded`
  becomes "model key-paths the struct did not consume" — computed on the model,
  not by re-walking the CST.

### The value model

A minimal, position-free tree (sketch; final home is a new `pkg/tomlval` or
`pkg/cst` sibling):

```go
type Value struct {
    Kind   ValueKind            // Scalar | Array | Table
    Raw    []byte               // Scalar: the leaf's verbatim bytes (sized ints,
                                 //   quoting, multiline survive — reuse Extract*)
    Items  []Value              // Array
    Table  []Field              // Table: ORDERED (preserves first-seen order)
    Path   string               // dotted key-path, for errors + Undecoded
}
type Field struct { Key string; Val Value }
```

Present-but-empty vs absent (#21) is preserved: an empty `Table`/`Array` is a
present `Value`; absence is a missing `Field`. Scalars keep `Raw`, so the
existing `Extract*`/cast logic and `TOMLUnmarshaler`/`TextUnmarshaler` dispatch
move over unchanged (they already take a node/bytes).

## What we keep (the parts that worked)

- **Lexer + ringbuf + CST** — byte-for-byte round-trip is the foundation. Untouched.
- **Document layer** — format-preserving `Get/Set/Delete`, comments. Untouched.
- **Encoder** — already emits exactly one canonical spelling into the original
  CST; format preservation lives here. The encode renderer is untouched.
- **Type algebra** — `analyze`/`classify*` → `TypeExpr` → `comp_ir` → `comp_build`
  folds. The decoder still folds over it; only the *leaf action* changes from
  "pattern-match the CST" to "read this kind from the model".
- **Validate hook, custom/text marshaler interfaces, telemetry, CLI.** Untouched.

## What we drop (the parts that held us back)

- Every spelling **fallback** in the decode renderers: #89 flat-key, #106/#108
  inline-table/array, #113 implicit-parent, the in-flight dotted-key/inline-array.
- The **root vs scoped renderer duality** (`compScoped*` mirrors of every
  container renderer) — the model has no document-root/scope distinction.
- `FindImplicitChildTable` + the synthetic-node machinery (`Node.Synthetic`,
  `implicitScope`, #113/#116) — implicit parents are materialized in `Decompose`.
- The scattered per-renderer duplicate guards (#90/#92/#102) and
  `cst.CheckNoDuplicateKeys` (#110) — duplicate detection is one rule in
  `Decompose`.
- `document.UndecodedKeys`'s inline-table descent (#109) — `Undecoded` is computed
  on the model.

These are not lost behaviors; they are **consolidated** into `Decompose` (one
grammar pass) + the model walk.

## Migration ("cut the turd loose")

Big-bang on the decode path, fenced by the fuzzer:

1. Land `Value` + `Decompose` with a direct conformance suite (every spelling →
   the same model; duplicates → error; #21/#103 fidelity).
2. Rewrite the decode renderer to fold over the model. Emit it **in parallel**
   (new file) so the old `DecodeX` keeps compiling until cutover.
3. Flip `TestRoundTripSpellingFuzz` to **gate every variant** (the promotion
   criterion). Run the full fuzz-sweep seed set.
4. Cut over `DecodeX`/`DecodeXInto` to the model walk; **delete** the old decode
   renderer, the `compScoped*` family, the implicit/synthetic machinery, the
   per-renderer dup guards, and `CheckNoDuplicateKeys`/`UndecodedKeys` inline
   descent.
5. Encode, CST, document untouched throughout.

## Test strategy

The spelling fuzzer becomes the gate, with all variants hard-failing. It is also
strengthened: with a normalizer, we can add a generator that emits *arbitrary
valid TOML for a value V* and assert it decodes to V — a stronger invariant than
"re-spell the encoder's output", retiring the fuzzer's own enumeration blind
spot (it could previously only test spellings the `Respell*` functions produce).

## Risks / open questions

- **Error-message quality** — the model drops CST positions. Mitigate with
  `Value.Path` provenance; errors quote the dotted key-path (decoders already
  key errors by name, not line).
- **#103 quoted-dot ambiguity** — `Decompose` resolves dotted keys structurally
  (segments), not by the joined string, so a quoted dot stays one segment. Same
  caveat as today, in one place.
- **Reflection marshal (`pkg/marshal`)** — out of scope initially; it can adopt
  `Decompose` in a later step or stay CST-based. Codegen is the churn epicenter
  and goes first.
- **Big-bang cutover** — mitigated by the parallel-emit + fuzzer-gate sequence;
  the old path stays until the new one is green across the sweep.
