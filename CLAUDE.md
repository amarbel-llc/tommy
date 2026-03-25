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
just                    # build + all tests (default)
just build              # go build -o build/tommy ./cmd/tommy
just test               # run both test-go and test-bats
just test-go            # go test -v ./...
just test-bats          # build then run bats integration tests

# Run a single Go test
go test -v -run TestName ./generate/

# Run bats tests (requires build first)
just build && cd zz-tests_bats && TOMMY_BIN=../build/tommy BATS_TEST_TIMEOUT=30 bats --tap generate.bats
```

## CLI Commands

The `tommy` binary has two subcommands:

- `tommy fmt [--check] <files...|->` --- Format TOML files (normalize whitespace
  around `=`, inline comment spacing, blank lines between tables, trailing
  whitespace). `--check` exits non-zero if not formatted.
- `tommy generate` --- Run via `//go:generate tommy generate` directive above a
  struct. Reads `$GOFILE` from the environment. Produces `*_tommy.go` with
  type-safe `Decode<Name>`/`Encode` methods.

## Architecture

The library has four layers, each depending only on the one below it:

1.  **Lexer** (`internal/lexer/`) --- Tokenizes TOML input into `Token` structs
    with `Kind` and `Raw` bytes. Every byte of input appears in exactly one
    token.

2.  **CST** (`pkg/cst/`) --- Builds a concrete syntax tree from tokens. Every
    token becomes a leaf `Node`; structural nodes (`NodeTable`,
    `NodeArrayTable`, `NodeKeyValue`, `NodeArray`, `NodeInlineTable`,
    `NodeDottedKey`) group children. `Node.Bytes()` concatenation reproduces the
    original input byte-for-byte.

3.  **Document** (`pkg/document/`) --- High-level API over the CST for
    reading/writing TOML values by dotted key paths. Supports
    `Get`/`Set`/`Delete`/`Has` with generics, array-of-tables
    (`FindArrayTableNodes`, `AppendArrayTableEntry`), sub-tables
    (`FindSubTables`, `EnsureSubTable`), and map extraction
    (`GetStringMapFromTable`). Mutations edit CST nodes in-place to preserve
    formatting. Also tracks consumed keys for undecoded-key detection.

4.  **Marshal** (`pkg/marshal/`) --- Reflection-based
    `UnmarshalDocument`/`MarshalDocument` using `toml` struct tags. Returns a
    `DocumentHandle` that holds the CST for round-trip editing.

**Code Generator** (`generate/`) --- Static analysis pipeline: - `Analyze` uses
`go/packages` + `go/ast` + `go/types` to inspect structs with
`//go:generate tommy generate` directives - `classifyField` determines the
`FieldKind` for each tagged field (primitive, struct, pointer, slice, map,
custom marshaler, text marshaler, etc.) - `emit.go` generates decode/encode
method bodies as Go source - `template.go` renders the final `*_tommy.go` file
via `text/template`

**Formatter** (`internal/formatter/`) --- CST-based TOML formatter that
normalizes whitespace, comment spacing, and blank lines.

## Code Generation Field Kinds

The generator classifies struct fields into these categories, each with distinct
encode/decode codegen paths:

- `FieldPrimitive` --- string, int, int64, uint64, float64, bool (+ type
  aliases)
- `FieldPointerPrimitive` --- `*bool`, `*int`, etc.
- `FieldStruct` / `FieldPointerStruct` --- nested structs with toml tags
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
