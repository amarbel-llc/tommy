# Tommy Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Build a comment-preserving TOML library, formatter, and CLI in Go.

**Architecture:** A CST parser preserves every byte of input (comments, whitespace, newlines as first-class nodes). Two API layers sit on top: a document query API for scripts/CLI, and a struct marshal API (with codegen) for typed consumers like dodder. A `tommy fmt` CLI provides opinionated formatting.

**Tech Stack:** Go 1.25, Nix (gomod2nix), standard library `testing` package, no external test frameworks.

**Rollback:** N/A — new project, purely additive.

---

## Phase 1: Project Scaffolding

### Task 1: Initialize Go module and Nix build

**Promotion criteria:** N/A

**Files:**
- Create: `go.mod`
- Create: `cmd/tommy/main.go`
- Create: `flake.nix`

**Step 1: Create go.mod**

```
cd /home/sasha/eng/repos/tommy
go mod init github.com/amarbel-llc/tommy
```

**Step 2: Create minimal CLI entrypoint**

Create `cmd/tommy/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: tommy <command>\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "fmt":
		fmt.Fprintf(os.Stderr, "tommy fmt: not yet implemented\n")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
```

**Step 3: Verify it builds**

Run: `cd /home/sasha/eng/repos/tommy && go build ./cmd/tommy`
Expected: binary `tommy` created with no errors

**Step 4: Create flake.nix**

Follow the dodder/chrest pattern with `buildGoApplication`, `gomod2nix`, stable-first nixpkgs convention. Single subpackage `cmd/tommy`. Reference `devenvs/go` from purse-first for the dev shell.

**Step 5: Commit**

```
git add go.mod cmd/ flake.nix
git commit -m "feat: initialize Go module and CLI skeleton"
```

---

## Phase 2: CST Node Types

### Task 2: Define CST node kinds and Node struct

**Promotion criteria:** N/A

**Files:**
- Create: `pkg/cst/node.go`
- Create: `pkg/cst/node_test.go`

**Step 1: Write the failing test**

Create `pkg/cst/node_test.go`:

```go
package cst

import "testing"

func TestNodeBytesReturnsRawForLeaf(t *testing.T) {
	node := &Node{
		Kind: NodeComment,
		Raw:  []byte("# hello"),
	}
	got := string(node.Bytes())
	if got != "# hello" {
		t.Errorf("expected '# hello', got %q", got)
	}
}

func TestNodeBytesConcatenatesChildren(t *testing.T) {
	node := &Node{
		Kind: NodeKeyValue,
		Children: []*Node{
			{Kind: NodeKey, Raw: []byte("key")},
			{Kind: NodeWhitespace, Raw: []byte(" ")},
			{Kind: NodeEquals, Raw: []byte("=")},
			{Kind: NodeWhitespace, Raw: []byte(" ")},
			{Kind: NodeString, Raw: []byte(`"value"`)},
			{Kind: NodeNewline, Raw: []byte("\n")},
		},
	}
	got := string(node.Bytes())
	expected := "key = \"value\"\n"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/sasha/eng/repos/tommy && go test ./pkg/cst/ -v`
Expected: FAIL — `cst` package does not exist

**Step 3: Write minimal implementation**

Create `pkg/cst/node.go`:

```go
package cst

type NodeKind int

const (
	NodeDocument   NodeKind = iota
	NodeTable               // [table]
	NodeArrayTable          // [[array-of-tables]]
	NodeKeyValue            // key = value
	NodeKey                 // bare or quoted key
	NodeDottedKey           // a.b.c
	NodeEquals              // =

	// Values
	NodeString
	NodeInteger
	NodeFloat
	NodeBool
	NodeDateTime
	NodeArray       // [1, 2, 3]
	NodeInlineTable // {a = 1, b = 2}

	// Trivia
	NodeComment    // # ...
	NodeWhitespace // spaces, tabs
	NodeNewline    // \n, \r\n

	// Punctuation
	NodeBracketOpen    // [
	NodeBracketClose   // ]
	NodeBraceOpen      // {
	NodeBraceClose     // }
	NodeComma          // ,
	NodeDot            // .
)

type Node struct {
	Kind     NodeKind
	Raw      []byte
	Children []*Node
}

func (n *Node) Bytes() []byte {
	if len(n.Children) == 0 {
		return n.Raw
	}
	var out []byte
	for _, child := range n.Children {
		out = append(out, child.Bytes()...)
	}
	return out
}
```

**Step 4: Run test to verify it passes**

Run: `cd /home/sasha/eng/repos/tommy && go test ./pkg/cst/ -v`
Expected: PASS

**Step 5: Commit**

```
git add pkg/cst/
git commit -m "feat: define CST node types with round-trip Bytes()"
```

---

## Phase 3: Lexer

### Task 3: Implement TOML lexer

The lexer tokenizes raw TOML input into a stream of tokens that map 1:1 to CST
leaf nodes. Each token preserves its exact source bytes.

**Promotion criteria:** N/A

**Files:**
- Create: `internal/lexer/token.go`
- Create: `internal/lexer/lexer.go`
- Create: `internal/lexer/lexer_test.go`

**Step 1: Write the failing test — basic key-value**

Create `internal/lexer/lexer_test.go`:

```go
package lexer

import "testing"

func TestLexBareKeyValueString(t *testing.T) {
	input := `key = "value"` + "\n"
	tokens := Lex([]byte(input))

	expected := []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenBasicString,
		TokenNewline,
	}

	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}

	for i, tok := range tokens {
		if tok.Kind != expected[i] {
			t.Errorf("token %d: expected %v, got %v", i, expected[i], tok.Kind)
		}
	}

	// Round-trip check
	var reconstructed []byte
	for _, tok := range tokens {
		reconstructed = append(reconstructed, tok.Raw...)
	}
	if string(reconstructed) != input {
		t.Errorf("round-trip failed: got %q", string(reconstructed))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/sasha/eng/repos/tommy && go test ./internal/lexer/ -v`
Expected: FAIL

**Step 3: Write tests for remaining TOML constructs**

Add test cases incrementally for each TOML feature. Each should verify the token
sequence and round-trip property:

- `TestLexComment` — `# comment\n`
- `TestLexInteger` — `port = 8080\n`
- `TestLexFloat` — `pi = 3.14\n`
- `TestLexBool` — `enabled = true\n`
- `TestLexTable` — `[section]\n`
- `TestLexArrayTable` — `[[items]]\n`
- `TestLexArray` — `ports = [80, 443]\n`
- `TestLexInlineTable` — `point = {x = 1, y = 2}\n`
- `TestLexMultilineBasicString` — triple-quoted strings
- `TestLexLiteralString` — single-quoted strings
- `TestLexDottedKey` — `a.b.c = "val"\n`
- `TestLexQuotedKey` — `"key with spaces" = "val"\n`
- `TestLexDateTime` — `created = 2026-03-18T12:00:00Z\n`
- `TestLexBlankLines` — whitespace-only lines between entries
- `TestLexCommentAfterValue` — `key = "val" # trailing\n`

**Step 4: Implement lexer**

Create `internal/lexer/token.go` with token kinds, then `internal/lexer/lexer.go`
with a `Lex(input []byte) []Token` function. The lexer scans byte-by-byte,
preserving exact source text in each token's `Raw` field.

Key token kinds:

```go
type TokenKind int

const (
	TokenBareKey TokenKind = iota
	TokenBasicString       // "..."
	TokenMultilineBasicString // """..."""
	TokenLiteralString     // '...'
	TokenMultilineLiteralString // '''...'''
	TokenInteger
	TokenFloat
	TokenBool
	TokenDateTime
	TokenEquals            // =
	TokenDot               // .
	TokenComma             // ,
	TokenBracketOpen       // [
	TokenBracketClose      // ]
	TokenDoubleBracketOpen // [[
	TokenDoubleBracketClose // ]]
	TokenBraceOpen         // {
	TokenBraceClose        // }
	TokenComment           // # ...
	TokenWhitespace        // spaces/tabs (not newlines)
	TokenNewline           // \n or \r\n
)

type Token struct {
	Kind TokenKind
	Raw  []byte
}
```

The lexer is context-sensitive: after `=` it expects a value; at line start it
expects a key or table header. Use a simple state machine (expecting key, expecting value, etc.).

**Step 5: Run all tests**

Run: `cd /home/sasha/eng/repos/tommy && go test ./internal/lexer/ -v`
Expected: PASS for all test cases

**Step 6: Commit**

```
git add internal/lexer/
git commit -m "feat: implement TOML lexer with round-trip preservation"
```

---

## Phase 4: Parser

### Task 4: Implement CST parser

The parser consumes the token stream and builds the CST tree. Every token
becomes a leaf node. Structural nodes (table, key-value, array, etc.) group
their children.

**Promotion criteria:** N/A

**Files:**
- Create: `internal/parser/parser.go`
- Create: `internal/parser/parser_test.go`

**Step 1: Write the failing test — simple document**

Create `internal/parser/parser_test.go`:

```go
package parser

import (
	"testing"

	"github.com/amarbel-llc/tommy/pkg/cst"
)

func TestParseSimpleKeyValue(t *testing.T) {
	input := []byte("key = \"value\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != cst.NodeDocument {
		t.Errorf("expected NodeDocument, got %v", doc.Kind)
	}

	// Round-trip
	got := string(doc.Bytes())
	if got != string(input) {
		t.Errorf("round-trip failed: expected %q, got %q", string(input), got)
	}
}

func TestParseCommentPreserved(t *testing.T) {
	input := []byte("# a comment\nkey = \"value\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got := string(doc.Bytes())
	if got != string(input) {
		t.Errorf("round-trip failed: expected %q, got %q", string(input), got)
	}
}

func TestParseTableWithComments(t *testing.T) {
	input := []byte(`# Storage config
[storage]
# Hash settings
hash_buckets = [2, 4]
base_path = "/data/blobs"  # override default

# Hashing algorithm
hash_type-id = "blake2b-256"
`)
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got := string(doc.Bytes())
	if got != string(input) {
		t.Errorf("round-trip failed:\nexpected:\n%s\ngot:\n%s", string(input), got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/sasha/eng/repos/tommy && go test ./internal/parser/ -v`
Expected: FAIL

**Step 3: Implement parser**

Create `internal/parser/parser.go`. The parser:

1. Calls `lexer.Lex(input)` to get tokens
2. Walks the token stream, grouping tokens into CST nodes
3. Trivia tokens (whitespace, newline, comment) become direct children of
   whichever structural node they appear in

Key parsing functions:
- `Parse(input []byte) (*cst.Node, error)` — entry point, returns `NodeDocument`
- `parseTable` — `[name]` header + key-values until next table
- `parseArrayTable` — `[[name]]` header + key-values
- `parseKeyValue` — key `=` value
- `parseValue` — dispatches to string/int/float/bool/datetime/array/inline-table
- `parseArray` — `[` value (`,` value)* `]`
- `parseInlineTable` — `{` key `=` value (`,` key `=` value)* `}`

**Step 4: Add tests for remaining constructs**

Add round-trip tests for:
- `TestParseArrayTable`
- `TestParseNestedTables`
- `TestParseDottedKeys`
- `TestParseMultilineStrings`
- `TestParseInlineTable`
- `TestParseArray`
- `TestParseMixedTypes`
- `TestParseEmptyDocument`
- `TestParseOnlyComments`

Each test asserts `doc.Bytes() == input` (byte-for-byte round-trip).

**Step 5: Run all tests**

Run: `cd /home/sasha/eng/repos/tommy && go test ./internal/parser/ -v`
Expected: PASS

**Step 6: Commit**

```
git add internal/parser/
git commit -m "feat: implement CST parser with byte-for-byte round-trip"
```

---

## Phase 5: Public Parse API

### Task 5: Expose Parse through pkg/cst

Wire the internal parser to the public `pkg/cst` package so consumers can call
`cst.Parse(input)`.

**Promotion criteria:** N/A

**Files:**
- Create: `pkg/cst/parse.go`
- Create: `pkg/cst/parse_test.go`

**Step 1: Write the failing test**

Create `pkg/cst/parse_test.go`:

```go
package cst

import "testing"

func TestParseRoundTrip(t *testing.T) {
	input := []byte("# comment\nkey = \"value\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got := string(doc.Bytes())
	if got != string(input) {
		t.Errorf("round-trip: expected %q, got %q", string(input), got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/sasha/eng/repos/tommy && go test ./pkg/cst/ -v`
Expected: FAIL — `Parse` not defined

**Step 3: Implement**

Create `pkg/cst/parse.go`:

```go
package cst

import "github.com/amarbel-llc/tommy/internal/parser"

func Parse(input []byte) (*Node, error) {
	return parser.Parse(input)
}
```

**Step 4: Run test to verify it passes**

Run: `cd /home/sasha/eng/repos/tommy && go test ./pkg/cst/ -v`
Expected: PASS

**Step 5: Commit**

```
git add pkg/cst/parse.go pkg/cst/parse_test.go
git commit -m "feat: expose Parse as public API in pkg/cst"
```

---

## Phase 6: Document Query API

### Task 6: Implement document Get/Set/Delete

**Promotion criteria:** N/A

**Files:**
- Create: `pkg/document/document.go`
- Create: `pkg/document/document_test.go`

**Step 1: Write the failing test — Get**

Create `pkg/document/document_test.go`:

```go
package document

import "testing"

func TestGetString(t *testing.T) {
	input := []byte(`name = "tommy"` + "\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get[string](doc, "name")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tommy" {
		t.Errorf("expected 'tommy', got %q", got)
	}
}

func TestGetIntSlice(t *testing.T) {
	input := []byte("buckets = [2, 4, 8]\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get[[]int](doc, "buckets")
	if err != nil {
		t.Fatal(err)
	}
	expected := []int{2, 4, 8}
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Errorf("index %d: expected %d, got %d", i, expected[i], got[i])
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/sasha/eng/repos/tommy && go test ./pkg/document/ -v`
Expected: FAIL

**Step 3: Write the failing test — Set preserving comments**

```go
func TestSetPreservesComments(t *testing.T) {
	input := []byte("# config\nkey = \"old\" # important\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	err = doc.Set("key", "new")
	if err != nil {
		t.Fatal(err)
	}

	got := string(doc.Bytes())
	expected := "# config\nkey = \"new\" # important\n"
	if got != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, got)
	}
}
```

**Step 4: Write the failing test — Set with nested table path**

```go
func TestSetNestedKey(t *testing.T) {
	input := []byte("[storage]\npath = \"/old\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	err = doc.Set("storage.path", "/new")
	if err != nil {
		t.Fatal(err)
	}

	got := string(doc.Bytes())
	expected := "[storage]\npath = \"/new\"\n"
	if got != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, got)
	}
}
```

**Step 5: Write the failing test — Delete**

```go
func TestDeletePreservesOtherEntries(t *testing.T) {
	input := []byte("a = 1\nb = 2\nc = 3\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	err = doc.Delete("b")
	if err != nil {
		t.Fatal(err)
	}

	got := string(doc.Bytes())
	expected := "a = 1\nc = 3\n"
	if got != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, got)
	}
}
```

**Step 6: Implement Document type**

Create `pkg/document/document.go`:

```go
package document

import "github.com/amarbel-llc/tommy/pkg/cst"

type Document struct {
	root *cst.Node
}

func Parse(input []byte) (*Document, error) {
	node, err := cst.Parse(input)
	if err != nil {
		return nil, err
	}
	return &Document{root: node}, nil
}

func Get[T any](doc *Document, key string) (T, error) { ... }
func (doc *Document) Set(key string, value any) error { ... }
func (doc *Document) Delete(key string) error { ... }
func (doc *Document) Bytes() []byte { return doc.root.Bytes() }
```

`Get` walks the CST to find the key-value node matching the dotted path, then
converts the value node to the requested Go type.

`Set` finds the key-value node matching the dotted path and replaces the value
node's `Raw` with the TOML-encoded representation of the new value. If the key
doesn't exist, appends a new key-value node to the appropriate table.

`Delete` removes the key-value node and its associated leading trivia from the
parent's children list.

**Step 7: Run all tests**

Run: `cd /home/sasha/eng/repos/tommy && go test ./pkg/document/ -v`
Expected: PASS

**Step 8: Commit**

```
git add pkg/document/
git commit -m "feat: implement document query API with Get/Set/Delete"
```

---

## Phase 7: Struct Marshal API

### Task 7: Implement UnmarshalDocument / MarshalDocument

This is the API dodder will use. Decode into a plain Go struct while retaining
the CST, then re-encode with struct changes merged back.

**Promotion criteria:** N/A

**Files:**
- Create: `pkg/marshal/marshal.go`
- Create: `pkg/marshal/marshal_test.go`

**Step 1: Write the failing test — round-trip with comment preservation**

Create `pkg/marshal/marshal_test.go`:

```go
package marshal

import "testing"

type Config struct {
	Name    string `toml:"name"`
	Port    int    `toml:"port"`
	Enabled bool   `toml:"enabled"`
}

func TestRoundTripPreservesComments(t *testing.T) {
	input := []byte(`# Server config
name = "myapp"  # the app name
port = 8080
enabled = true
`)
	var cfg Config
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Name != "myapp" {
		t.Errorf("Name: expected 'myapp', got %q", cfg.Name)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port: expected 8080, got %d", cfg.Port)
	}

	// Modify one field
	cfg.Port = 9090

	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	expected := `# Server config
name = "myapp"  # the app name
port = 9090
enabled = true
`
	if string(out) != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/sasha/eng/repos/tommy && go test ./pkg/marshal/ -v`
Expected: FAIL

**Step 3: Write additional test — nested table struct**

```go
type StorageConfig struct {
	HashBuckets []int  `toml:"hash_buckets"`
	BasePath    string `toml:"base_path"`
}

type FullConfig struct {
	Storage StorageConfig `toml:"storage"`
}

func TestRoundTripNestedTable(t *testing.T) {
	input := []byte(`[storage]
# Hash bucket sizes
hash_buckets = [2, 4]
base_path = "/data"  # data directory
`)
	var cfg FullConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	cfg.Storage.HashBuckets = []int{2, 4, 8}

	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	expected := `[storage]
# Hash bucket sizes
hash_buckets = [2, 4, 8]
base_path = "/data"  # data directory
`
	if string(out) != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
```

**Step 4: Implement**

Create `pkg/marshal/marshal.go`:

```go
package marshal

import "github.com/amarbel-llc/tommy/pkg/document"

type DocumentHandle struct {
	doc *document.Document
}

func UnmarshalDocument(input []byte, v any) (*DocumentHandle, error) { ... }
func MarshalDocument(handle *DocumentHandle, v any) ([]byte, error) { ... }
```

`UnmarshalDocument`:
1. Calls `document.Parse(input)` to build the CST-backed document
2. Uses reflection to walk the struct's `toml:` tags
3. For each field, calls `document.Get` to extract the value and sets the struct field

`MarshalDocument`:
1. Uses reflection to walk the struct's `toml:` tags
2. For each field, compares current value against what was originally decoded
3. Calls `document.Set` for any changed fields
4. Returns `doc.Bytes()`

**Step 5: Run all tests**

Run: `cd /home/sasha/eng/repos/tommy && go test ./pkg/marshal/ -v`
Expected: PASS

**Step 6: Commit**

```
git add pkg/marshal/
git commit -m "feat: implement struct marshal/unmarshal with comment preservation"
```

---

## Phase 8: Formatter

### Task 8: Implement formatting rules

**Promotion criteria:** N/A

**Files:**
- Create: `internal/formatter/formatter.go`
- Create: `internal/formatter/formatter_test.go`

**Step 1: Write the failing test — whitespace normalization**

Create `internal/formatter/formatter_test.go`:

```go
package formatter

import "testing"

func TestNormalizeEqualsWhitespace(t *testing.T) {
	input := []byte("key   =   \"value\"\n")
	expected := "key = \"value\"\n"
	got := string(Format(input))
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestPreservesComments(t *testing.T) {
	input := []byte("# important comment\nkey = \"value\"\n")
	got := string(Format(input))
	if got != string(input) {
		t.Errorf("formatting should preserve already-formatted input: got %q", got)
	}
}

func TestTrailingWhitespaceRemoved(t *testing.T) {
	input := []byte("key = \"value\"   \n")
	expected := "key = \"value\"\n"
	got := string(Format(input))
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestBlankLinesBetweenTables(t *testing.T) {
	input := []byte("[a]\nkey = 1\n[b]\nkey = 2\n")
	expected := "[a]\nkey = 1\n\n[b]\nkey = 2\n"
	got := string(Format(input))
	if got != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestTrailingNewlineNormalized(t *testing.T) {
	input := []byte("key = 1\n\n\n")
	expected := "key = 1\n"
	got := string(Format(input))
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /home/sasha/eng/repos/tommy && go test ./internal/formatter/ -v`
Expected: FAIL

**Step 3: Implement formatter**

Create `internal/formatter/formatter.go`:

```go
package formatter

import "github.com/amarbel-llc/tommy/pkg/cst"

func Format(input []byte) []byte {
	doc, err := cst.Parse(input)
	if err != nil {
		return input // return unchanged on parse error
	}
	formatNode(doc)
	return doc.Bytes()
}
```

The formatter walks the CST and applies transformations to trivia nodes:
- Normalize whitespace around `=` to exactly one space
- Remove trailing whitespace before newlines
- Ensure exactly one blank line between table/array-table headers
- Trim trailing newlines to exactly one
- Normalize indentation in multi-line arrays and inline tables

**Step 4: Add test for idempotency**

```go
func TestFormatIdempotent(t *testing.T) {
	input := []byte("# comment\n[table]\nkey = \"value\" # trailing\n")
	first := Format(input)
	second := Format(first)
	if string(first) != string(second) {
		t.Errorf("format is not idempotent:\nfirst:  %q\nsecond: %q", string(first), string(second))
	}
}
```

**Step 5: Run all tests**

Run: `cd /home/sasha/eng/repos/tommy && go test ./internal/formatter/ -v`
Expected: PASS

**Step 6: Commit**

```
git add internal/formatter/
git commit -m "feat: implement TOML formatter with comment preservation"
```

---

## Phase 9: CLI

### Task 9: Wire `tommy fmt` command

**Promotion criteria:** N/A

**Files:**
- Modify: `cmd/tommy/main.go`
- Create: `cmd/tommy/fmt.go`

**Step 1: Implement fmt subcommand**

Create `cmd/tommy/fmt.go`:

```go
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/amarbel-llc/tommy/internal/formatter"
)

func runFmt(args []string) int {
	check := false
	var files []string

	for _, arg := range args {
		switch arg {
		case "--check":
			check = true
		case "-":
			files = append(files, "-")
		default:
			files = append(files, arg)
		}
	}

	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "usage: tommy fmt [--check] <files...|->\n")
		return 1
	}

	exitCode := 0
	for _, file := range files {
		if err := fmtFile(file, check); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", file, err)
			exitCode = 1
		}
	}
	return exitCode
}

func fmtFile(path string, check bool) error {
	var input []byte
	var err error

	if path == "-" {
		input, err = io.ReadAll(os.Stdin)
	} else {
		input, err = os.ReadFile(path)
	}
	if err != nil {
		return err
	}

	output := formatter.Format(input)

	if check {
		if string(output) != string(input) {
			return fmt.Errorf("not formatted")
		}
		return nil
	}

	if path == "-" {
		_, err = os.Stdout.Write(output)
		return err
	}

	return os.WriteFile(path, output, 0644)
}
```

**Step 2: Update main.go**

Update `cmd/tommy/main.go` to call `runFmt`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: tommy <command>\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "fmt":
		os.Exit(runFmt(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
```

**Step 3: Build and test manually**

```
cd /home/sasha/eng/repos/tommy
go build ./cmd/tommy
echo 'key  =   "value"' > /tmp/test.toml
./tommy fmt /tmp/test.toml
cat /tmp/test.toml
# Expected: key = "value"
```

**Step 4: Test --check flag**

```
echo 'key  =   "value"' > /tmp/test.toml
./tommy fmt --check /tmp/test.toml
echo $?
# Expected: 1

echo 'key = "value"' > /tmp/test.toml
./tommy fmt --check /tmp/test.toml
echo $?
# Expected: 0
```

**Step 5: Test stdin mode**

```
echo 'key  =   "value"' | ./tommy fmt -
# Expected stdout: key = "value"
```

**Step 6: Commit**

```
git add cmd/tommy/
git commit -m "feat: wire tommy fmt CLI with --check and stdin support"
```

---

## Phase 10: TOML Spec Conformance

### Task 10: Add TOML test suite

Use the official TOML test suite (https://github.com/toml-lang/toml-test) to
validate parser correctness against the TOML v1.0.0 spec.

**Promotion criteria:** N/A

**Files:**
- Create: `internal/parser/conformance_test.go`

**Step 1: Add toml-test as a test dependency or embed test fixtures**

Download the TOML test suite valid/invalid fixtures into `testdata/`.
Alternatively, use the Go toml-test harness if available.

**Step 2: Write conformance test runner**

```go
func TestTOMLSpecConformance(t *testing.T) {
	// Walk testdata/valid/ — each .toml file must parse without error
	// and round-trip (Bytes() == input)

	// Walk testdata/invalid/ — each .toml file must produce a parse error
}
```

**Step 3: Fix any spec violations found**

Iterate until all valid inputs parse and round-trip, and all invalid inputs
produce errors.

**Step 4: Commit**

```
git add internal/parser/conformance_test.go testdata/
git commit -m "test: add TOML v1.0.0 spec conformance tests"
```

---

## Phase 11: Nix Build Finalization

### Task 11: Complete Nix packaging

**Promotion criteria:** N/A

**Files:**
- Modify: `flake.nix`
- Create: `gomod2nix.toml`

**Step 1: Generate gomod2nix.toml**

Run: `cd /home/sasha/eng/repos/tommy && gomod2nix`
Expected: `gomod2nix.toml` created with locked dependency hashes

**Step 2: Verify nix build**

Run: `nix build --show-trace`
Expected: builds successfully, `result/bin/tommy` exists

**Step 3: Verify nix flake check**

Run: `nix flake check`
Expected: PASS

**Step 4: Commit**

```
git add flake.nix gomod2nix.toml
git commit -m "feat: complete Nix packaging with gomod2nix"
```
