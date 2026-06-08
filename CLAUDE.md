# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Overview

Tommy is a TOML library for Go that preserves comments and formatting on
round-trip. It provides a CST-based parser, a format-preserving document API,
reflection-based marshal/unmarshal, and a code generator that produces type-safe
Decode/Encode methods from struct annotations.

## Build & Test

``` sh
just                    # default: validate + build + test (the full nix lane)
just validate           # nix flake check — builds every check (bats lanes +
                        #   go-generate + fuzz-sweep) + library unit tests
just build              # nix build (the tommy binary)
just test               # nix lanes: bats (CLI e2e) + the offline Go ./generate suite
just test-bats-nix      # the bats lane (bats-default)
just test-go-generate-nix    # the Go ./generate suite offline in nix
just test-fuzz-sweep-nix     # multi-seed fuzz sweep (all 3 fuzzers) offline in nix
just test-bats-nix-tag fmt   # a single tagged lane

# Local fast iteration on the Go test suite (needs network for go/packages):
go test -v -run TestName ./generate/   # a single Go test
just debug-test TestName               # one Go test, verbose
```

**Codegen renderers & wire-format coverage.** Two jennifer-based renderers fold
over the compositional IR (`comp_ir.go`, built by `comp_build.go` over the
`TypeExpr` algebra in `typeexpr.go`): `RenderFile` (`generate/comp_render.go`)
emits the **encode** path, while the **decode** path is emitted by
`comp_decode_model.go` as a walk over the normalized value model produced by
`cst.Decompose` (the spelling-normalization layer — one reader per kind, no
per-spelling fallbacks; see `docs/decisions/2026-06-07-decode-normalization.md`).
Shared jennifer helpers live in `comp_support.go`. CI covers it three
ways: the bats lane (`bats-default`) exercises
`tommy generate` end-to-end against the installed binary; the `go-generate`
flake check runs the rich Go `./generate/...` integration suite (100+ cases,
incl. the #81/#82 regression tests) offline against a pinned module cache
(`goModCache` in `flake.nix`, `TOMMY_TEST_OFFLINE=1`; the synthetic modules can't
hit the network in the sandbox); and the `fuzz-sweep` flake check loops the three
generative fuzzers (`TestRoundTripFuzz`, `TestRoundTripFuzzDelegation`,
`TestRoundTripSpellingFuzz`) over many seeds (`fuzzSweepSeeds` in `flake.nix`) —
the `go-generate` check runs them at seed 1 only, the sweep widens shape coverage
per merge. When adding an emission edge case, add both a
bats test under `zz-tests_bats/` (tagged `generate`, e.g. `encode_wire_format.bats`)
and a Go integration test for depth.

## CLI Commands

The `tommy` binary has two subcommands:

- `tommy fmt [--check] <files...|->` --- Format TOML files (normalize whitespace
  around `=`, inline comment spacing, blank lines between tables, trailing
  whitespace). `--check` exits non-zero if not formatted.
- `tommy generate` --- Run via `//go:generate tommy generate` directive above a
  struct. Reads `$GOFILE` from the environment. Produces `*_tommy.go` with
  type-safe `Decode<Name>`/`Encode` methods.

Both subcommands emit best-effort stats-me/statsd telemetry (`internal/stats`,
mirroring piggy's `crates/piggy/src/stats.rs`): a `tommy.<cmd>.{success,failure}`
counter plus a `tommy.<cmd>.duration` timer per invocation (`<cmd>` is `generate`
or `fmt`), wrapped via `stats.Timed`. It is fire-and-forget UDP, gated on the
presence of `STATSD_HOST`/`STATSD_PORT` (a no-op when neither is set), so it
never perturbs codegen or formatting. See `stats-me-clients(7)`.

## Architecture

The library has four layers, each depending only on the one below it:

1.  **Lexer** (`internal/lexer/`) --- Tokenizes TOML input into `Token` structs
    with `Kind` and `Raw` bytes. Every byte of input appears in exactly one
    token. Backed by a ring buffer (`internal/ringbuf/`) that streams from an
    `io.Reader`, with a cached window for fast per-byte access and arena
    allocation for token `Raw` bytes.

2.  **CST** (`pkg/cst/`) --- Builds a concrete syntax tree from tokens. Every
    token becomes a leaf `Node`; structural nodes (`NodeTable`,
    `NodeArrayTable`, `NodeKeyValue`, `NodeArray`, `NodeInlineTable`,
    `NodeDottedKey`) group children. `Node.Bytes()` concatenation reproduces the
    original input byte-for-byte. `Decompose` collapses a parsed CST into a
    canonical `Value` model (the decode-normalization layer that erases TOML's
    spelling duality); undecoded-key detection lives there (`Value.Undecoded`).
    A consumer that decodes elsewhere (not through tommy) can still compute
    unknown keys off the model: `DecomposeBytes(data)`, mark recognized paths
    via `Value.Get`/`GetPath` + `MarkSeen`/`MarkConsumed`, then `Undecoded()` —
    spelling-correct, unlike a raw-CST key walk.

3.  **Document** (`pkg/document/`) --- High-level API over the CST for
    reading/writing TOML values by dotted key paths. Supports
    `Get`/`Set`/`Delete`/`Has` with generics, array-of-tables
    (`FindArrayTableNodes`, `AppendArrayTableEntry`), sub-tables
    (`FindSubTables`, `EnsureSubTable`), and map extraction
    (`GetStringMapFromTable`). Mutations edit CST nodes in-place to preserve
    formatting.

4.  **Marshal** (`pkg/marshal/`) --- Reflection-based
    `UnmarshalDocument`/`MarshalDocument` using `toml` struct tags. Returns a
    `DocumentHandle` that holds the CST for round-trip editing.

**Code Generator** (`generate/`) --- Static analysis pipeline: - `Analyze` uses
`go/packages` + `go/ast` + `go/types` to inspect structs with
`//go:generate tommy generate` directives - `classifyField` determines the
`FieldKind` for each tagged field (primitive, struct, pointer, slice, map,
custom marshaler, text marshaler, etc.) - `fieldType` (`typeexpr.go`) maps each
`FieldKind` to a compositional `TypeExpr` (Scalar/Ptr/Slice/Map/Struct/Delegated)
- the `comp_build.go` folds recurse over that algebra to build the `comp_ir.go`
node trees - `RenderFile` (`comp_render.go`) emits the **encode** method via
jennifer, while the **decode** method is emitted by `comp_decode_model.go`, which
walks the normalized `cst.Value` model from `cst.Decompose` (one reader per kind;
the old per-spelling CST-pattern decode renderer was retired by the 2026-06-07
decode-normalization ADR) - Cross-package struct fields use delegation:
`FieldDelegatedStruct` emits calls to the target package's `DecodeInto`/`EncodeFrom`
(`DecodeXInto` now takes a `*cst.Value`) instead of inlining field-by-field
decoding, enabling structs that contain unexported types

**Ring Buffer** (`internal/ringbuf/`) --- Circular buffer backed by an
`io.Reader`, ported from dodder's `catgut` package. Provides `Peek`,
`AdvanceRead`, `Fill`, and a `Slice` type for split-view access across the wrap
boundary. Used by the lexer to enable streaming tokenization without requiring
the entire input in memory.

**Formatter** (`internal/formatter/`) --- CST-based TOML formatter that
normalizes whitespace, comment spacing, and blank lines.

## Code Generation Field Kinds

The generator classifies struct fields into these categories, each with distinct
encode/decode codegen paths:

- `FieldPrimitive` --- string, int, int64, uint64, float64, bool (+ type
  aliases)
- `FieldPointerPrimitive` --- `*bool`, `*int`, etc.
- `FieldStruct` / `FieldPointerStruct` --- nested structs with toml tags
  (same-package only)
- `FieldDelegatedStruct` / `FieldPointerDelegatedStruct` --- cross-package
  structs that delegate to the target package's `DecodeInto`/`EncodeFrom`
- `FieldSlicePrimitive` --- `[]int`, `[]string`
- `FieldSliceStruct` --- `[]Server` (array-of-tables)
- `FieldCustom` --- implements `TOMLUnmarshaler`/`TOMLMarshaler`
- `FieldTextMarshaler` --- implements `encoding.TextMarshaler`/`TextUnmarshaler`
- `FieldMapStringString` --- `map[string]string`
- `FieldMapStringStruct` --- `map[string]SomeStruct`
- `FieldMapStringMapStringString` --- `map[string]NamedMapAlias`
- `FieldSliceTextMarshaler` --- `[]TextMarshalerType`

## Struct Tag Options

`toml:"key"` --- required for codegen to process a field `toml:"key,omitempty"`
--- omit zero-value fields on encode `toml:"key,multiline"` --- use `"""`
multiline string syntax

## Interfaces

- `TOMLUnmarshaler` / `TOMLMarshaler` (`pkg/tommy.go`) --- Custom
  marshal/unmarshal via `any` values
- `encoding.TextMarshaler` / `TextUnmarshaler` --- Supported for fields and
  slices; round-trips through string representation
- `Validate() error` --- When a struct implements this method, generated
  `Decode`/`Encode` methods call it automatically. Decode validates after all
  fields are set; Encode validates before writing to the CST.

## Testing

- Go unit tests cover the lexer, CST parser, document API, marshal, formatter,
  and code generator (analyze, emit, template, integration)
- Bats integration tests (`zz-tests_bats/`) test the CLI end-to-end by
  scaffolding temporary Go projects with `go generate`
- CST conformance test (`pkg/cst/conformance_test.go`) verifies byte-for-byte
  round-trip fidelity

## Nix

Built with `gomod2nix`. After changing Go dependencies, run `gomod2nix` to
regenerate `gomod2nix.toml`. The flake follows the stable-first nixpkgs
convention (see parent `eng/CLAUDE.md`).

Changing Go dependencies also invalidates `goModCache` (the pinned offline
module cache the `go-generate` check resolves synthetic modules against). After
`gomod2nix`, run `nix build .#go-generate` once — it fails with a hash mismatch
showing the new `got:` hash; paste that into `goModCache.outputHash` in
`flake.nix`.
