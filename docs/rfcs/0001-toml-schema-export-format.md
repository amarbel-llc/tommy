---
status: proposed
date: 2026-06-13
---

# TOML Schema Export Format (`*_tommy_schema.json`)

## Abstract

This document specifies a stable, versioned JSON document that `tommy`
emits to describe the TOML configuration schema of a Go struct: its
field TOML-keys, value types, optionality, and nesting. The format is
derived mechanically from the same static analysis that drives `tommy`'s
Go codec generation, so a consumer (e.g. a Nix home-manager module
generator) can derive an artifact that stays in lock-step with the Go
struct instead of hand-shadowing it. This RFC specifies the export
format only; it does not specify any particular consumer.

## Introduction

`tommy generate` analyzes a Go struct annotated with
`//go:generate tommy generate` and emits a type-safe TOML codec. The
struct is the single source of truth for a config's shape. A growing
consumer pattern (tommy#133) also generates the *config file* — or a
typed interface to it — from Nix: a home-manager module hand-builds an
attrset mirroring the struct, then renders TOML via
`(pkgs.formats.toml {}).generate`. That attrset is a hand-maintained
shadow of the struct: when the struct gains a field, the Nix side drifts
silently until a downstream validation step catches it.

This RFC defines a machine-readable export of the struct's schema so the
Nix side (and any other consumer) can be derived from the same source
`tommy` already analyzes. It is the foundational layer ("C") of the
C-then-B plan recorded in tommy#133: a stable schema-export *contract*
now, with a home-manager module *generator* ("B") to be built later as a
consumer of this format. The module generator is explicitly out of scope
here.

The format mirrors `tommy`'s internal compositional type model — the
`TypeExpr`/`spkType` algebra introduced by the compositional codegen work
(see `docs/decisions/2026-06-01-compositional-codegen.md`): a struct is a
list of fields, and each field's type is a node in a closed algebra of
Scalar / Pointer / Slice / Map / Struct / Delegated constructors. The
export is a faithful, language-neutral projection of that model.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in RFC 2119.

## Specification

### Document structure

A schema export document MUST be a single JSON object (a "schema
document") with the following members:

| Member | Type | Required | Description |
|--------|------|----------|-------------|
| `schemaVersion` | integer | MUST | Format version of this document. This RFC defines version `1`. |
| `tommyVersion` | string | SHOULD | The producing `tommy` build (version + commit), for provenance. Informative. |
| `roots` | array of string | MUST | The struct definition names that carry a `//go:generate tommy generate` directive in the analyzed file. Each entry MUST be a key of `definitions`. |
| `definitions` | object | MUST | Map from struct name to a *struct definition*. MUST include every struct reachable from any root (transitively, through field types). |

Producers MUST set `schemaVersion` to `1` for documents conforming to
this RFC. Consumers MUST reject a document whose `schemaVersion` they do
not support rather than partially interpreting it.

A schema document MUST be valid UTF-8 JSON. Producers SHOULD emit it
deterministically (stable key and array ordering for a fixed input +
`tommy` build) so it can be committed and diffed.

### Struct definition

Each value in `definitions` MUST be an object:

| Member | Type | Required | Description |
|--------|------|----------|-------------|
| `fields` | array of *field* | MUST | The struct's TOML-tagged fields, in declaration order. Fields with no `toml:` tag (which `tommy` does not codec) MUST be omitted. |
| `validated` | boolean | SHOULD | `true` iff the struct implements `Validate() error` (tommy runs it on decode/encode). Informative — a consumer cannot reproduce the Go validation, but the signal is preserved. |

Definition keys are the struct's Go type name as `tommy` resolves it:
bare (e.g. `TLS`) for a struct in the analyzed package, or qualified
(e.g. `othermod.Credentials`) for a cross-package struct reached by
delegation. A `ref` (see below) MUST match a `definitions` key exactly.

### Field

Each entry of a definition's `fields` array MUST be an object:

| Member | Type | Required | Description |
|--------|------|----------|-------------|
| `tomlKey` | string | MUST | The TOML key (the `toml:"<key>"` tag name). MUST be non-empty. |
| `goName` | string | MUST | The Go field name. Informative; useful for diagnostics. |
| `type` | *type node* | MUST | The field's value type (see below). |
| `omitempty` | boolean | MUST | `true` iff the field's tag carries `,omitempty`. |
| `multiline` | boolean | MAY | `true` iff the tag carries `,multiline` (string fields rendered with `"""`). Presentation hint; MAY be omitted when `false`. |

**Optionality.** A consumer MUST treat a field as optional (not required
to be present in the TOML) iff `omitempty` is `true` OR the field's
top-level `type` node has `kind` `"pointer"`. Otherwise the field is
required. This rule is the normative definition of required-ness; the
format does not carry a separate `required` member.

### Type nodes

A *type node* is a JSON object with a `kind` member drawn from the closed
set below. Each `kind` MUST carry exactly the members specified for it.
Consumers MUST reject a type node with an unrecognized `kind`.

#### `scalar`

A leaf value.

| Member | Type | Required | Description |
|--------|------|----------|-------------|
| `kind` | `"scalar"` | MUST | |
| `scalar` | string | MUST | The abstract TOML value kind: one of `"string"`, `"integer"`, `"float"`, `"boolean"`, or `"unknown"`. |
| `codec` | string | MUST | How the Go value crosses the TOML boundary: `"primitive"` (a native Go scalar), `"text"` (implements `encoding.TextMarshaler`/`TextUnmarshaler`; round-trips through a TOML string), or `"custom"` (implements `tommy.TOMLMarshaler`/`TOMLUnmarshaler`). |
| `goType` | string | MUST | The underlying Go type name (e.g. `"string"`, `"int"`, `"int64"`, `"uint64"`, `"float64"`, `"bool"`, or a qualified `"pkg.Type"` for text/custom codecs). Preserves Go fidelity that `scalar` abstracts away. |

A `"text"` codec MUST set `scalar` to `"string"` (a `TextMarshaler`
always serializes to a TOML string). A `"custom"` codec MUST set
`scalar` to `"unknown"`: a `TOMLMarshaler` returns an arbitrary value,
so the static TOML kind is not knowable; consumers SHOULD map `"unknown"`
to their most permissive type.

#### `pointer`

A nullable wrapper (`*T`).

| Member | Type | Required | Description |
|--------|------|----------|-------------|
| `kind` | `"pointer"` | MUST | |
| `elem` | type node | MUST | The pointed-to type. |

A `pointer` node MAY appear anywhere a type node may (including nested in
`array`/`table`, e.g. `[]*T`). A consumer SHOULD map `pointer` to a
nullable type.

#### `array`

A TOML array (`[]T`), including an array-of-tables when `element` is a
`struct`.

| Member | Type | Required | Description |
|--------|------|----------|-------------|
| `kind` | `"array"` | MUST | |
| `element` | type node | MUST | The element type. A `struct` element denotes a TOML array-of-tables. |
| `goType` | string | MAY | Present iff the Go type is a named slice alias (e.g. `"pkg.IntSlice"`). |

#### `table`

A TOML table keyed by arbitrary strings (`map[string]T`).

| Member | Type | Required | Description |
|--------|------|----------|-------------|
| `kind` | `"table"` | MUST | |
| `value` | type node | MUST | The value type for every key. |
| `goType` | string | MAY | Present iff the Go type is a named map alias (e.g. `"pkg.Labels"`). |

A TOML table whose key set is *fixed* (a nested struct) MUST use the
`struct` kind, not `table`. `table` denotes only the open-keyed
`map[string]T` case.

#### `struct`

A reference to a named struct definition (a nested or cross-package
struct whose shape `tommy` resolved).

| Member | Type | Required | Description |
|--------|------|----------|-------------|
| `kind` | `"struct"` | MUST | |
| `ref` | string | MUST | A key of the document's `definitions`. |

A `struct` node MUST NOT inline the struct's fields; it MUST reference a
`definitions` entry. Both same-package nested structs and cross-package
delegated structs (whose shape `tommy` resolved) use this kind, and the
referenced struct MUST appear in `definitions`. This makes recursive and
shared structs representable without infinite inlining.

#### `opaque`

A value whose shape `tommy` could not resolve (e.g. a delegated
cross-package struct in a package `tommy` cannot analyze).

| Member | Type | Required | Description |
|--------|------|----------|-------------|
| `kind` | `"opaque"` | MUST | |
| `goType` | string | MUST | The qualified Go type name. |

A consumer SHOULD map `opaque` to its most permissive type and SHOULD
surface that the shape is unknown.

### Example

For the Go source:

```go
//go:generate tommy generate
type Server struct {
	Host   string            `toml:"host"`
	Port   int               `toml:"port,omitempty"`
	Tags   []string          `toml:"tags"`
	Labels map[string]string `toml:"labels"`
	TLS    *TLS              `toml:"tls"`
}

type TLS struct {
	Cert string `toml:"cert"`
}
```

a conforming producer emits:

```json
{
  "schemaVersion": 1,
  "tommyVersion": "0.4.4 (394fb58)",
  "roots": ["Server"],
  "definitions": {
    "Server": {
      "validated": false,
      "fields": [
        { "tomlKey": "host", "goName": "Host", "omitempty": false,
          "type": { "kind": "scalar", "scalar": "string", "codec": "primitive", "goType": "string" } },
        { "tomlKey": "port", "goName": "Port", "omitempty": true,
          "type": { "kind": "scalar", "scalar": "integer", "codec": "primitive", "goType": "int" } },
        { "tomlKey": "tags", "goName": "Tags", "omitempty": false,
          "type": { "kind": "array",
                    "element": { "kind": "scalar", "scalar": "string", "codec": "primitive", "goType": "string" } } },
        { "tomlKey": "labels", "goName": "Labels", "omitempty": false,
          "type": { "kind": "table",
                    "value": { "kind": "scalar", "scalar": "string", "codec": "primitive", "goType": "string" } } },
        { "tomlKey": "tls", "goName": "TLS", "omitempty": false,
          "type": { "kind": "pointer", "elem": { "kind": "struct", "ref": "TLS" } } }
      ]
    },
    "TLS": {
      "validated": false,
      "fields": [
        { "tomlKey": "cert", "goName": "Cert", "omitempty": false,
          "type": { "kind": "scalar", "scalar": "string", "codec": "primitive", "goType": "string" } }
      ]
    }
  }
}
```

Here `port` is optional (`omitempty`), `tls` is optional (top-level
`pointer`), and the remaining fields are required.

An **invalid** document — a `struct` node whose `ref` is absent from
`definitions`:

```json
{ "kind": "struct", "ref": "Missing" }
```

Consumers MUST reject a `ref` with no matching `definitions` key.

### Delivery (non-normative for the format)

This RFC specifies the document; how it is produced and named is left to
the implementation. A producer SHOULD make the export an opt-in mode of
`tommy generate` (e.g. a `--schema` flag) and, when writing to disk,
SHOULD use the `<base>_tommy_schema.json` filename next to the
`<base>_tommy.go` codec. The format is independent of delivery: the same
JSON MAY be written to stdout, a file, or embedded by another tool.

## Security Considerations

A schema document describes the *shape* of a configuration, not its
values; it contains Go type names and TOML keys drawn from source the
producer already controls. It carries no secrets and grants no
authority.

Two considerations apply to consumers:

- **Untrusted input.** A consumer that ingests a schema document from an
  untrusted source MUST treat it as data, not code. In particular,
  `goType`, `tomlKey`, `goName`, and `ref` values are arbitrary strings
  and MUST NOT be interpolated into a shell, a Nix expression, or
  generated code without escaping appropriate to that sink. A malicious
  document could otherwise inject content through these fields.
- **Resource bounds.** `definitions` may reference each other to
  represent recursive types. A consumer that resolves `ref`s MUST guard
  against unbounded recursion (cycle detection or a depth limit) so a
  cyclic document cannot cause non-termination.

The producer reads only the Go source it is already analyzing for codec
generation, so the export introduces no new read surface or trust
boundary on the producing side.

## Conformance Testing

This section applies once a `tommy` build implements the export (the
format precedes its implementation; this RFC is `proposed`). When a
producing binary exists, conformance tests for it MUST live in
`zz-tests_bats/` and use binary injection via `bats-emo`:

    require_bin TOMMY_BIN tommy

so the suite is portable across implementations.

### Covered Requirements

| Requirement | Description |
|-------------|-------------|
| Document structure, `schemaVersion` MUST be `1` | Emitted document carries `schemaVersion: 1`. |
| Document structure, `roots` ⊆ `definitions` | Every `roots` entry is a `definitions` key. |
| Struct definition, reachability | Every struct reachable from a root appears in `definitions`. |
| `struct` node, `ref` resolves | Every `ref` matches a `definitions` key. |
| Field, optionality rule | `omitempty` and top-level `pointer` round-trip from tagged source. |
| Type nodes, kind coverage | Each `spkType` constructor maps to the specified `kind` (scalar/pointer/array/table/struct/opaque). |
| Determinism | Re-running the producer on fixed input yields byte-identical output. |

## Compatibility

`schemaVersion` governs compatibility. Within a major version,
producers MAY add new OPTIONAL members to documents, definitions,
fields, and type nodes; consumers MUST ignore members they do not
recognize. Any change that removes a member, changes a member's meaning,
or adds a new `type` node `kind` that older consumers cannot safely
ignore MUST increment `schemaVersion`. Consumers MUST reject a document
whose `schemaVersion` exceeds the highest version they implement.

This RFC defines the foundational format. The home-manager module
generator ("B" in tommy#133) is a *consumer* of this format and will be
specified separately (an FDR for the generator feature, and/or its own
RFC if it defines a further interface). The `scalar`/`codec`/`goType`
triple and the closed `kind` set are designed so that a consumer can map
each node onto a target type system — e.g. the home-manager module
types: `scalar`→`types.{str,int,float,bool}`, `pointer`→`types.nullOr`,
`array`→`types.listOf`, `table`→`types.attrsOf`, `struct`→
`types.submodule`, `opaque`→a permissive type.

## References

### Normative

- [RFC 2119] Bradner, S., "Key words for use in RFCs to Indicate
  Requirement Levels", BCP 14, RFC 2119, March 1997.

### Informative

- tommy#133 — "emit a Nix-native representation of a tommy TOML schema"
  (the originating exploration; records the C-then-B decision).
- `docs/decisions/2026-06-01-compositional-codegen.md` — the `spkType`
  compositional type algebra this format projects.
