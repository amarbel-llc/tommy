# Compositional renderer cutover — execution plan & handoff

**Status:** Phase 1, Step A landed (`generate/typeexpr.go`). Steps B→C→D remain.
This doc is a self-contained handoff so a fresh session can execute without
re-deriving the analysis. Read the two reference points below FIRST.

## What this is

Promote the *compositional* (algebraic) code-generator renderer to **replace**
the *enumerated* one, delivering the shrink the accepted ADR
(`docs/decisions/2026-06-01-compositional-codegen.md`) proposed. Phase 1 keeps
the existing classifier (`analyze.go`) and swaps only the IR + builder +
renderer. Phase 2 (separate, deferred) replaces the classifier.

### Read these first (all on `master`)
- **ADR**: `docs/decisions/2026-06-01-compositional-codegen.md` — design + why.
- **Reference implementations (the proven spikes)** — these ARE the structural
  template for production; port from them:
  - `generate/spike_compositional_test.go` — proves the fold reproduces the
    *current* op trees for all 16 FieldKinds (DeepEqual + render diff, 8 fixtures
    + 500 random trees).
  - `generate/spikev2_test.go` — the compositional-IR PoC: ~4-node-per-direction
    IR + recursive renderers, compile-and-round-trip across the full surface
    incl. codecs, delegation, maps, #55 flat fallback, #10 positional nesting.
- **The current production pipeline to replace**: `generate/ir.go` (ops),
  `generate/ir_build.go` (FieldKind switches), `generate/ir_render.go`
  (`RenderFile`/`jenEmitStruct` + per-op helpers). Keep `render_helpers.go`,
  `ir_target.go`, `generate.go`, `analyze.go`.

## What the spikes prove vs. what they dropped (the gap analysis)

The spikes proved the compositional **structure** works end to end. They
deliberately omitted/simplified four things that production REQUIRES. Closing
these four gaps is the entire substance of Step C.

| Gap | Spike behavior | Production requirement |
|---|---|---|
| consumed / undecoded-key tracking | dropped entirely (no `consumed` map; delegated calls pass a throwaway `map[string]bool{}`) | thread `consumed` everywhere; `Undecoded()` depends on it |
| handle tracking | always re-finds array nodes | receiver-context same-pkg `[]struct` must store + reuse `*cst.Node` handles for round-trip identity |
| positional nesting (#10) | one level only (`v2RenderPositional` panics on non-leaf nested struct) | full `jenPosOp` dispatch (all container kinds, recursive) |
| flat-key fallback (#55) | primitive-scalar leaves only | all inner field kinds, via a second child list |

## The four gaps — exact mechanics

### 1. consumed / undecoded-key tracking
Reintroduce a decode context mirroring today's `jenCtx` (`ir_render.go:16-56`):
```go
type decCtx struct { consumed *jen.Statement; retErr func(string,...jen.Code) jen.Code; docVar *jen.Statement; rootChildren func() *jen.Statement }
func (c decCtx) mc(k TOMLKey) jen.Code      { return c.consumed.Clone().Index(k.Jen()).Op("=").True() }
func (c decCtx) mcExpr(e *jen.Statement) jen.Code { return c.consumed.Clone().Index(e).Op("=").True() }
```
Provide `receiverDecCtx()` (`consumed`=`d.consumed`, `docVar`=`d.cstDoc`) and
`freeDecCtx()` (`consumed`=`consumed` param, `docVar`=`doc`) — lift of
`receiverJenCtx`/`freeJenCtx`. Thread `ctx` through every render fn.

Emit marks exactly where the current renderer does:
- **leaf**: after the assign inside `if ok {…}`, `ctx.mc(l.TKey)` (cf.
  `jenLeafCase`, every kind). Each leaf node must carry **`TKey TOMLKey`** (full
  dotted/prefixed key); the bare case-label is `TKey.BareKey()`.
- **table / array / map containers**: mark the container key on match
  (`jenIT:403`, `jenFAT:466`, `jenFMS:516`), and per map entry
  `ctx.mcExpr(TKey.Jen() + "." + _mk)` (`jenFMS:523`, `jenMSS:360`).
- **delegation**: pass the real `ctx.consumed.Clone()` + `keyPrefix` to
  `DecodeXInto` (`jenDS:741`, `jenDSl:788`, `jenDM:820`) — NOT a throwaway map.

**`injectMapKey` is eliminated** (current `ir_render.go:846-904,1388-1440`,
~120 lines): instead of post-hoc rewriting inner op `TKey`s to splice the
runtime `_mk`, the fold threads it. Give the fold position a `tkey TOMLKey`
field; `mapStructNode` seeds child fold `tkey = parent.tkey.Dot(staticKey).Var("_mk")`
so inner leaf keys build `targets.<_mk>.host` directly. `TOMLKey` already
supports `.Var(name)` (`ir_target.go`). **Verify the generated consumed-key
strings match** — oracle: `TestUndecodedMapKeysAllConsumed`, the inline
`doc.Undecoded()` asserts.

Restore signatures via the scaffolding:
`DecodeXInto(data *X, doc *document.Document, container *cst.Node, consumed map[string]bool, keyPrefix string) error` and
`EncodeXFrom(data *X, doc *document.Document, container *cst.Node) error`.

### 2. handle tracking (receiver context only)
For same-pkg `[]struct` (`isSamePackageSliceStruct`, `render_helpers.go:52`):
- Document gets `<unexport(GoName)> []<unexport(TypeName)>Handle` fields and a
  `type <unexport(TypeName)>Handle struct { node *cst.Node }` (emitted in the
  scaffolding, cf. `jenEmitStruct:87-100`).
- Array nodes carry `TrackHandles bool` (+ `HandleField`, `TypeName`).
  `TrackHandles = emitHandles && !strings.Contains(TypeName, ".")`.
- Decode populates `d.<field>[i] = <type>Handle{node:_node}` (cf. `jenFAT:455-471`).
- Encode reuses `if i < len(d.<field>) { container = d.<field>[i].node } else { AppendArrayTableEntryAfter }` (cf. `jenForEncodeArrayTable:1231-1243`).
- `emitHandles` is threaded into the folds: top-level receiver Decode/Encode =
  `true`; nested + `DecodeXInto`/`EncodeXFrom` = `false`. Free/cross-pkg context
  keeps the spike's `FindArrayTableNodes` path.
- **Names must agree** between decode-populate and encode-reuse. Oracle:
  `TestAppendPreservesExisting`, `TestAppendNewEntry`.

### 3. positional nesting (#10)
Replace `v2RenderPositional` (one-level panic) with a full `jenPosOp`
(`ir_render.go:541-563`) equivalent dispatch over `InTable`/`MapScalar`/
`ArrayTable`/`NilGuard`/`MapStruct`/`Delegate*`, keyed off `_pi`/`_inScope`
counting against the parent `[[pk]]` dotted key. **Transplant the existing
correct bodies** (`jenPIT:572`, `jenPMSS:588`, `jenPFAT:603`, `jenPIPT:645`,
`jenPFMS:675`) into the compositional walk threaded with `ctx`; don't re-derive.
Oracle: `TestNesting*`, `TestDeeplyNested`.

### 4. flat-key fallback (#55)
The nil-guard node needs **two** child lists: `Children` (in-table, dotted keys)
and `FlatChildren` (parent-level, bare keys; array-table sub-fields keep dotted
keys — cf. `ir_build.go:131-138`). The else-branch renders `FlatChildren`
through `renderDecodeBody` with a `foundVar` threaded so each leaf sets
`_found = true` after assign (cf. `jenLeafCase` `fv != ""` path, `:259-261`, and
`jenIPT` else `:427-434`). This is the one place the spike's single-list
elegance must regress to two lists for parity.

## Full-file emission (`emitStruct`) — reproduce `jenEmitStruct` (`ir_render.go:85-128`)
In order: (1) per-field `<type>Handle` types; (2) `<Name>Document{data, cstDoc,
consumed map[string]bool, <handle fields>}`; (3) `Decode<Name>` (parse →
`consumed: make(...)` → decode → `Validate()` hook → return); (4) `Data()`
(verbatim); (5) `Encode()` (Validate hook → encode → `Bytes()`); (6)
`Undecoded()` → `document.UndecodedKeys(...)` (verbatim); (7)
Comment/SetComment/InlineComment/SetInlineComment (verbatim); (8)
`Decode<Name>Into`; (9) `Encode<Name>From`. Keep `RenderFile`'s header,
`ImportName`s, `collectImportPaths` loop, and the **load-bearing** `var _ =
fmt.Errorf / cst.NodeKind / strings.Contains` blank-usage block (goimports runs
`FormatOnly:true` — it does NOT add/remove imports, so a dangling import =
compile failure).

## File map
- **Add**: `generate/typeexpr.go` (DONE), `generate/ir.go` body (compositional
  nodes + new fields: leaf `TKey`, array `TrackHandles/HandleField/TypeName`,
  nil-guard `FlatChildren`), `generate/ir_build.go` body (folds), `generate/ir_render.go`
  body (recursive renderer + `decCtx`/`encCtx` + scaffolding). (Or new
  `comp_*.go` files kept alongside the old until the flip.)
- **Keep**: `render_helpers.go`, `ir_target.go` (TargetPath + TOMLKey are
  reused), `generate.go` (`RenderFile(&buf, pkg, infos)` signature), `analyze.go`.
- **Delete (Step D)**: old `ir.go` ops, `ir_build.go` switches, old `ir_render.go`
  helpers (incl. `injectMapKey`, the `jenPos*` family), and **both spike files**
  (the equivalence harness asserts fold==OLD builder, so it can't outlive it).

## Migration ordering (tree compiles + suite runs at each step)
- **A (DONE)**: `TypeExpr`+`fieldType` → `generate/typeexpr.go`.
- **B**: stand up the compositional nodes/folds/renderer as production symbols
  (recommended: new `comp_*.go` files so the old pipeline stays authoritative and
  nothing collides). `RenderFile` still old.
- **C**: harden to full parity — the four gaps above + scaffolding. Add a
  temporary compile test of the new scaffolding output.
- **D (one commit)**: flip `RenderFile`/`emitStruct` to the compositional path;
  delete old `ir*`/spike files. Highest-risk commit; C de-risks it.

## Parity bar & verification
No cross-renderer golden files. Gate on `just` (= `validate build test`): offline
`nix build .#go-generate` (regenerate+compile), all bats lanes, and
`go test ./generate/...` (184 integration tests = compile+run+round-trip — the
dominant behavioral oracle).

Run crux oracles first during C/D:
`go test ./generate/ -run 'TestUndecoded|TestAppend|TestNesting|TestDeeplyNested|TestFacade|TestDelegated|TestPointer' -v`
- consumed: `TestUndecoded*`, `TestUndecodedMapKeysAllConsumed`.
- handles: `TestAppendPreservesExisting`, `TestAppendNewEntry`.
- positional: `TestNesting*`, `TestDeeplyNested`.
- flat fallback: pointer-struct / implicit-parent tests.
- #82: `encode_wire_format.bats` + the `tags = []` integration asserts.

The ~10 shape-checks the new renderer must satisfy: exactly-one `func DecodeX(`
and `func (d *XDocument) Encode` (`generate.bats`); `DecodeXInto`/`EncodeXFrom`
present for delegated structs; generated file imports the facade NOT the
`internal/` pkg (#81, `alias_imports.bats` + `TestFacade*`); no unexported
cross-pkg type names leaked; `generate_is_idempotent` byte-diff (determinism —
**confirm no map-range ordering leaks into node lists**; the folds iterate the
ordered `si.Fields` slice, so safe, but audit `collectImportPaths`).

## Risks (ranked)
1. **Positional generalization (#10)** — transplant the existing `jenPos*`
   bodies; highest chance of `_pi`/`_inScope` counting bugs.
2. **Map-entry consumed keys** — the `injectMapKey`→`tkey.Var("_mk")` collapse
   must reproduce exact strings incl. map-of-delegated `keyPrefix`.
3. **Flat-fallback two-list regression** — bare-key vs dotted-key distinction
   for array-table sub-fields is easy to get wrong.
4. **Handle field/type name agreement** — mismatch silently breaks round-trip
   identity.
5. **The Step-D flip+delete commit** — de-risk with the Step-C temporary
   compile test.

## Phase 2 (deferred — separate issue)
Replace the dual `classifyField`(go/ast) / `classifyFromType`(go/types) with one
recursive `go/types`→`TypeExpr` classifier. Wins: kills the #32/#36 dual-path
divergence; centralizes the #81 alias-vs-declaring-package import logic
(currently duplicated across ~8 sites). Reusable as-is: tag parsing
(`extractTomlTag`), interface detection (`hasMethod`/`hasMarshalTOML`), embedded
promotion, `Validate` detection. The #81 alias-import logic re-expressed over
TypeExpr is the risk locus. Do AFTER Phase 1 lands.
