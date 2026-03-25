# Tommy

A TOML library for Go that preserves comments, formatting, and whitespace on
round-trip.

Most TOML libraries parse into a map or struct and discard everything else.
Tommy keeps a concrete syntax tree (CST) so that reading a config, modifying a
value, and writing it back produces minimal diffs --- comments stay where they
were, blank lines don't move, and whitespace around `=` signs is untouched.

## Features

- **Round-trip fidelity** --- parse and serialize without losing comments or
  formatting
- **Code generator** --- annotate a struct with `//go:generate tommy generate`
  and get type-safe `Decode`/`Encode` methods with undecoded-key detection
- **Reflection-based marshal** --- `UnmarshalDocument`/`MarshalDocument` for
  quick use without codegen
- **Document API** --- read and write values by dotted key path while preserving
  the surrounding document
- **Formatter** --- `tommy fmt` normalizes whitespace, comment spacing, and
  blank lines

## Install

``` sh
go install github.com/amarbel-llc/tommy/cmd/tommy@latest
```

Or with Nix:

``` sh
nix build github:amarbel-llc/tommy
```

## How It Works

Tommy parses TOML into a concrete syntax tree where comments, whitespace, and
blank lines are first-class nodes --- not discarded during parsing. When you
modify a value through any of Tommy's APIs, only the value node in the tree is
updated. Everything else --- comments above, inline comments after values, blank
line separators between sections --- stays exactly where it was.

    input.toml                          after Encode()
    ─────────────                       ──────────────
    # Server configuration              # Server configuration
    [server]                            [server]
    port = 8080 # default port          port = 9090 # default port
    host = "localhost"                  host = "localhost"

This is automatic. There is no flag to enable or annotation to add --- every
path through Tommy (codegen, reflection marshal, document API) preserves the
full document structure. The only thing that changes in the output is the value
you changed.

The recommended way to use Tommy is through its code generator. Add a
`//go:generate tommy generate` directive above your struct and you get type-safe
`Decode`/`Encode` methods that handle all of this transparently --- you just
read and write normal Go struct fields. No CST manipulation, no special APIs, no
awareness of the preservation machinery needed.

The code generator is not yet fully type-exhaustive --- support for additional
Go type patterns is being added as needed. See the [open
issues](https://github.com/amarbel-llc/tommy/issues) for what's planned and in
progress.

This matters for config files that humans maintain: version-controlled TOML with
explanatory comments, hand-tuned formatting, or sections separated by blank
lines. A programmatic update to one field should not rewrite the entire file.

## Quick Start

### Code Generator (recommended)

Annotate your struct and run `go generate`:

``` go
//go:generate tommy generate
type Config struct {
    Title   string `toml:"title"`
    Port    int    `toml:"port"`
    Debug   bool   `toml:"debug,omitempty"`
}
```

This produces a `config_tommy.go` file with:

``` go
func DecodeConfig(input []byte) (*ConfigDocument, error)
func (d *ConfigDocument) Data() *Config
func (d *ConfigDocument) Encode() ([]byte, error)
func (d *ConfigDocument) Undecoded() []string
```

Usage:

``` go
doc, err := DecodeConfig(input)
cfg := doc.Data()
cfg.Port = 9090
output, err := doc.Encode()
// output preserves all comments and formatting from input
```

### Reflection-based Marshal

For simpler cases without code generation:

``` go
import "github.com/amarbel-llc/tommy/pkg/marshal"

var cfg Config
handle, err := marshal.UnmarshalDocument(input, &cfg)

cfg.Port = 9090
output, err := marshal.MarshalDocument(handle, &cfg)
```

### Document API

For direct key-value manipulation:

``` go
import "github.com/amarbel-llc/tommy/pkg/document"

doc, err := document.Parse(input)
port, err := document.Get[int](doc, "server.port")
err = doc.Set("server.port", 9090)
output := doc.Bytes()
```

## Supported Field Types

The code generator handles:

  Type                                          TOML Representation
  --------------------------------------------- -------------------------------
  `string`, `int`, `int64`, `float64`, `bool`   Scalar values
  `*string`, `*int`, `*bool`, etc.              Optional scalars
  Nested structs                                `[table]` sections
  `*Struct`                                     `[table]` or flat dotted keys
  `[]int`, `[]string`                           Arrays
  `[]Struct`                                    `[[array-of-tables]]`
  `map[string]string`                           `[table]` with string values
  `map[string]Struct`                           Sub-tables (`[parent.key]`)
  `TOMLMarshaler`/`TOMLUnmarshaler`             Custom marshal via `any`
  `TextMarshaler`/`TextUnmarshaler`             Custom marshal via string

### Validation

If your struct implements `Validate() error`, the generated `Decode` and
`Encode` methods call it automatically. Decode validates after all fields are
set; Encode validates before writing to the CST.

``` go
//go:generate tommy generate
type Config struct {
    Port int    `toml:"port"`
    Name string `toml:"name"`
}

func (c Config) Validate() error {
    if c.Port < 1 || c.Port > 65535 {
        return fmt.Errorf("port must be 1-65535, got %d", c.Port)
    }
    return nil
}
```

No interface import is required --- just add the method and re-run
`go generate`. This also works with newtypes to validate individual values while
preserving their native TOML types (no string coercion).

### Struct Tag Options

``` go
`toml:"key"`                // required — maps field to TOML key
`toml:"key,omitempty"`      // omit zero-value fields on encode
`toml:"key,multiline"`      // use """ multiline string syntax
`toml:"-"`                  // skip this field
```

## CLI

``` sh
# Format TOML files in-place
tommy fmt config.toml settings.toml

# Check formatting without modifying (exits non-zero if unformatted)
tommy fmt --check config.toml

# Format from stdin
cat config.toml | tommy fmt -

# Generate code (typically via go generate, not called directly)
tommy generate
```

## License

MIT
