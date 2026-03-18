# Tommy: Comment-Preserving TOML Library

## Problem

TOML libraries in Go (notably `pelletier/go-toml/v2`) discard comments on
round-trip. Programs that decode a human-edited TOML file, modify a field, and
re-encode it clobber all comments. This is a concrete problem in dodder, where
type configs, genesis configs, and workspace configs are human-authored TOML but
programmatically updated.

## Solution

A TOML library, formatter, and CLI built on a concrete syntax tree (CST) that
preserves every byte of the original input — including comments, whitespace, and
blank lines. Two generated API layers on top of the CST serve different consumer
needs without sacrificing Go's type safety.

## Architecture

```
┌──────────────────────────────────────────┐
│  Codegen B: Companion Document type      │  ← dodder's primary API
│  //go:generate tommy                     │
│  Struct unchanged, generated doc wraps   │
│  CST for round-trip encoding             │
├──────────────────────────────────────────┤
│  Codegen C: Field-level wrappers         │  ← consumers wanting per-field
│  //go:generate tommy --fields            │  comment access / finer control
│  tommy.Field[T] carries CST node         │
├──────────────────────────────────────────┤
│  Mid-level: Document Query API           │  ← CLI, scripts
│  doc.Get[T](key), doc.Set(key, val)      │
├──────────────────────────────────────────┤
│  Low-level: CST / Parser                 │  ← formatter, future LSP
│  Comments as first-class nodes           │
└──────────────────────────────────────────┘
```

All layers share the same CST as the single source of truth.

## CST Node Model

The parser produces a tree where every byte of input is represented. Comments,
whitespace, and newlines are first-class nodes alongside semantic content.

```go
type NodeKind int

const (
    // Structural
    NodeDocument    NodeKind = iota
    NodeTable                       // [table]
    NodeArrayTable                  // [[array-of-tables]]
    NodeKeyValue                    // key = value
    NodeKey                         // bare or quoted key
    NodeDottedKey                   // a.b.c

    // Values
    NodeString
    NodeInteger
    NodeFloat
    NodeBool
    NodeDateTime
    NodeArray                       // [1, 2, 3]
    NodeInlineTable                 // {a = 1, b = 2}

    // Trivia (non-semantic)
    NodeComment                     // # ...
    NodeWhitespace                  // spaces, tabs
    NodeNewline                     // \n, \r\n
)

type Node struct {
    Kind     NodeKind
    Raw      []byte
    Children []*Node
}
```

Key property: `concat(leaf.Raw for all leaves) == original input`. Round-trip
fidelity is guaranteed by construction.

## API Layers

### Document Query API (Mid-level)

Wraps the CST with keyed access. Used by the CLI and available to any consumer
that doesn't want struct overhead.

```go
doc, err := tommy.Parse(input)

buckets, err := tommy.Get[[]int](doc, "hash_buckets")
err := doc.Set("hash_buckets", []int{2, 4, 8})
err := doc.Delete("old_field")

out := doc.Bytes()
```

`Set` on an existing key replaces the value node while preserving surrounding
trivia. `Set` on a new key appends to the relevant table section.

### Codegen B: Companion Document (Struct + Doc Handle)

Downstream consumers keep their existing structs with `toml:` tags. A
`//go:generate tommy` directive above the struct produces a companion type that
pairs the struct with a CST for round-trip encoding.

```go
//go:generate tommy
type TomlV3 struct {
    HashBuckets []int  `toml:"hash_buckets"`
    BasePath    string `toml:"base_path,omitempty"`
    HashTypeId  string `toml:"hash_type-id"`
}

// Generated:
type TomlV3Document struct {
    cst  *tommy.Node
    data TomlV3
}

func DecodeTomlV3(input []byte) (*TomlV3Document, error) { ... }
func (d *TomlV3Document) Data() *TomlV3 { return &d.data }
func (d *TomlV3Document) Encode() []byte { ... }
```

### Codegen C: Field-Level Wrappers

For consumers wanting per-field CST node access (and future programmatic comment
support), a `--fields` flag generates structs with `tommy.Field[T]` wrappers:

```go
// Generated:
type TomlV3Document struct {
    HashBuckets tommy.Field[[]int]
    BasePath    tommy.Field[string]
    HashTypeId  tommy.Field[string]
}

// tommy.Field[T]:
//   .Get() T
//   .Set(T)
```

## Formatter

The primary CLI tool. Reads TOML, parses to CST, applies formatting rules,
writes back. Comments survive because they are CST nodes.

**Rules (starting set):**
- Normalize whitespace around `=` to single spaces
- One blank line between tables
- No trailing whitespace
- Consistent indentation for multi-line arrays/inline tables
- Trim trailing newlines to exactly one
- Key sorting within tables (opt-in flag, off by default)

**Interface:**
```
tommy fmt [files...]          # format in place
tommy fmt --check [files...]  # exit 1 if not formatted (CI)
tommy fmt -                   # stdin → stdout
```

Opinionated defaults, no config file in v1.

## Adoption Strategy

### Dodder Integration

Tommy does not replace any existing infrastructure at launch. Adoption is
incremental and additive:

1. **Tommy ships independently** — library, CLI, formatter usable without dodder
   changes.
2. **Dodder adopts per call site** — specific code paths that write human-edited
   configs (genesis configs, workspace configs, type blobs) switch to tommy's
   `UnmarshalDocument` / `MarshalDocument`. Hyphence stays on
   `pelletier/go-toml/v2`.
3. **Future: hyphence integration** — dodder's `CoderToml` switches to tommy as
   its parser, eliminating the double-parse. This is a separate effort.

This ordering was chosen because dodder is the biggest anticipated consumer at
launch, and its hyphence layer has deep generic dispatch that would be disruptive
to change immediately. The per-call-site approach lets dodder validate tommy on
real workloads before committing to deeper integration.

### Rollback

If tommy has bugs, dodder reverts the specific call sites that adopted it. The
hyphence/pelletier path remains functional. No dual-architecture period is
needed because tommy is purely additive.

## Project Structure

```
tommy/                         # standalone repo (github.com/amarbel-llc/tommy)
├── flake.nix
├── go.mod
├── gomod2nix.toml
├── cmd/
│   └── tommy/
│       └── main.go
├── pkg/
│   ├── cst/                   # CST node types, parser
│   ├── document/              # Document query API
│   └── marshal/               # Struct marshal/unmarshal + doc handle
├── internal/
│   ├── lexer/                 # Tokenizer
│   ├── parser/                # CST builder
│   └── formatter/             # Formatting rules
├── generate/                  # go:generate codegen
├── docs/
│   └── plans/
└── TODO.md
```

## Deferred

- LSP (diagnostics, completions, hover, go-to-definition)
- Richer CLI commands (validate, get/set, diff, merge, JSON/TOML conversion)
- Programmatic comment add/modify (B/C scope — preserve-only in v1)
- Hyphence-native integration (Mock A)

## Comment Preservation Scope

v1 targets **preserve-only**: programs read/write struct fields, human-written
comments survive untouched. Programmatic comment creation/modification
(`tommy.Field[T]` carrying comment metadata) is enabled by the architecture
(Codegen C) but deferred to a future iteration.
