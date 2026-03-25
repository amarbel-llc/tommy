---
promotion-criteria: integration test passes for struct with Validate() method
status: proposed
---

# FDR: Struct-Level Validation in Code Generator

## Problem

Tommy users who want to validate TOML values beyond type correctness have two
bad options today:

1.  **TextUnmarshaler** --- coerces values to strings, losing native TOML types
    (int, bool, float) and breaking consumers that expect typed values.
2.  **Post-decode manual checks** --- no codegen support, easy to forget, no
    consistency between decode and encode paths.

There is no mechanism for the code generator to call user-defined validation
logic while preserving native TOML types.

## Solution

Detect `Validate() error` on structs during analysis. Generate calls to
`Validate()` in both the decode and encode paths.

Users define Go newtypes with validation methods:

``` go
type Port int

func (p Port) Validate() error {
    if p < 1 || p > 65535 {
        return fmt.Errorf("port must be 1-65535, got %d", p)
    }
    return nil
}
```

The codegen detects `Validate() error` via `go/types` during analysis (same
pattern as TOMLUnmarshaler/TextMarshaler detection). No interface import is
required by the user --- the method signature is sufficient.

## Interface

### Detection

`StructInfo` gains a `Validatable bool` field. Set during analysis via the
existing `hasMethod()` function:

``` go
si.Validatable = hasMethod(structObj, "Validate")
```

### Generated Decode

After all fields are decoded:

``` go
if err := d.data.Validate(); err != nil {
    return nil, fmt.Errorf("validation failed: %w", err)
}
```

### Generated Encode

Before any CST mutations:

``` go
if err := d.data.Validate(); err != nil {
    return nil, fmt.Errorf("validation failed: %w", err)
}
```

### Nested Structs

Each struct's generated `Decode`/`Encode` method calls its own `Validate()` if
present. Traversal is bottom-up (post-order): inner structs validate before
outer structs.

## Scope

This first cut is struct-level only. Per-field validation (newtypes over
primitives, slice elements, map values) is deferred.

## Future Levers

1.  **Fail-fast vs collect-all** --- currently fail-fast. Could later support
    multi-error returns to surface all validation failures at once.
2.  **Traversal ordering** --- currently post-order. Could later support
    pre-order or in-order for cases where outer validation should gate inner.
3.  **Per-field validation** --- add `Validatable bool` to `FieldInfo`, call
    `Validate()` on individual fields after they are set.
