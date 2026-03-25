# Struct-Level Validation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Generate `Validate()` calls in decode/encode methods when a struct
implements `Validate() error`.

**Architecture:** Add `Validatable bool` to `StructInfo`. During analysis, check
if the struct type has a `Validate` method via the existing `hasMethod()`
function. In emit, append a `Validate()` call after all field decodes and
prepend one before field encodes. In the template, conditionally emit the calls
based on `si.Validatable`.

**Tech Stack:** `go/types` for method detection, `text/template` for code
generation, `fmt.Fprintf` for emit.

**Rollback:** N/A --- purely additive. Structs without `Validate()` are
unchanged.

--------------------------------------------------------------------------------

### Task 1: Analyzer --- detect Validate() on structs

**Files:**

- Modify: `generate/analyze.go:32-35` (StructInfo)
- Modify: `generate/analyze.go:137-173` (analyzeStruct)
- Modify: `generate/analyze.go:581-616` (resolveStructFromTypes)
- Test: `generate/analyze_test.go`

**Step 1: Write the failing test**

Add to `generate/analyze_test.go`:

``` go
func TestAnalyzeValidatableStruct(t *testing.T) {
    dir := t.TempDir()
    writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
    writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
    Port int `+"`"+`toml:"port"`+"`"+`
}

func (c Config) Validate() error {
    return nil
}
`)

    infos, err := Analyze(dir, "config.go")
    if err != nil {
        t.Fatal(err)
    }
    if len(infos) != 1 {
        t.Fatalf("expected 1 struct, got %d", len(infos))
    }
    if !infos[0].Validatable {
        t.Fatal("expected Validatable to be true")
    }
}

func TestAnalyzeNonValidatableStruct(t *testing.T) {
    dir := t.TempDir()
    writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
    writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
    Name string `+"`"+`toml:"name"`+"`"+`
}
`)

    infos, err := Analyze(dir, "config.go")
    if err != nil {
        t.Fatal(err)
    }
    if len(infos) != 1 {
        t.Fatalf("expected 1 struct, got %d", len(infos))
    }
    if infos[0].Validatable {
        t.Fatal("expected Validatable to be false")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestAnalyzeValidatableStruct ./generate/`

Expected: FAIL --- `Validatable` field does not exist on `StructInfo`.

**Step 3: Write minimal implementation**

In `generate/analyze.go`, add `Validatable` to `StructInfo` (line 34):

``` go
type StructInfo struct {
    Name        string
    Fields      []FieldInfo
    Validatable bool
}
```

In `analyzeStruct()` (after field loop, before `return si, nil` at line 173),
look up the struct's type object and check for the method:

``` go
obj := pkg.Types.Scope().Lookup(name)
if obj != nil {
    si.Validatable = hasMethod(obj, "Validate")
}
```

In `resolveStructFromTypes()` (after field loop, before `return si, nil` at line
615), check via the `types.Struct`'s parent named type. This requires passing
the `types.Object` or looking it up. The simplest approach: the callers of
`resolveStructFromTypes` already have the named type object. However, the
function signature doesn't include it. Instead, look up in the package scope
when possible, or accept that cross-package struct validation detection requires
the caller to set it. For the first cut, only same-package structs need
`Validatable` (since only structs with `//go:generate tommy generate` get
top-level codegen). Nested struct validation is handled by their own generated
`Decode`/`Encode` if they also have the directive.

**Step 4: Run test to verify it passes**

Run: `go test -v -run "TestAnalyze(Validatable|NonValidatable)" ./generate/`

Expected: PASS

**Step 5: Commit**

    feat(generate): detect Validate() method on structs during analysis

--------------------------------------------------------------------------------

### Task 2: Emit --- generate Validate() calls in decode and encode

**Files:**

- Modify: `generate/emit.go:9-15` (emitDecodeBody)
- Modify: `generate/emit.go:17-23` (emitEncodeBody)
- Test: `generate/emit_test.go`

**Step 1: Write the failing tests**

Add to `generate/emit_test.go`:

``` go
func TestEmitDecodeBodyWithValidation(t *testing.T) {
    si := StructInfo{
        Name:        "Config",
        Validatable: true,
        Fields: []FieldInfo{
            {GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
        },
    }
    code := emitDecodeBody(si)
    if !strings.Contains(code, "d.data.Validate()") {
        t.Fatalf("expected Validate() call in decode, got:\n%s", code)
    }
}

func TestEmitDecodeBodyWithoutValidation(t *testing.T) {
    si := StructInfo{
        Name: "Config",
        Fields: []FieldInfo{
            {GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
        },
    }
    code := emitDecodeBody(si)
    if strings.Contains(code, "Validate") {
        t.Fatalf("unexpected Validate() call in decode, got:\n%s", code)
    }
}

func TestEmitEncodeBodyWithValidation(t *testing.T) {
    si := StructInfo{
        Name:        "Config",
        Validatable: true,
        Fields: []FieldInfo{
            {GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
        },
    }
    code := emitEncodeBody(si)
    if !strings.Contains(code, "d.data.Validate()") {
        t.Fatalf("expected Validate() call in encode, got:\n%s", code)
    }
}

func TestEmitEncodeBodyWithoutValidation(t *testing.T) {
    si := StructInfo{
        Name: "Config",
        Fields: []FieldInfo{
            {GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
        },
    }
    code := emitEncodeBody(si)
    if strings.Contains(code, "Validate") {
        t.Fatalf("unexpected Validate() call in encode, got:\n%s", code)
    }
}
```

**Step 2: Run tests to verify they fail**

Run:
`go test -v -run "TestEmit(Decode|Encode)Body(With|Without)Validation" ./generate/`

Expected: FAIL --- `Validate()` call not present in output.

**Step 3: Write minimal implementation**

In `emitDecodeBody()`, after the field loop, add:

``` go
func emitDecodeBody(si StructInfo) string {
    var buf bytes.Buffer
    for _, fi := range si.Fields {
        buf.WriteString(emitDecodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()", ""))
    }
    if si.Validatable {
        fmt.Fprintf(&buf, "\tif err := d.data.Validate(); err != nil {\n")
        fmt.Fprintf(&buf, "\t\treturn nil, fmt.Errorf(\"validation failed: %%w\", err)\n")
        fmt.Fprintf(&buf, "\t}\n")
    }
    return buf.String()
}
```

In `emitEncodeBody()`, before the field loop, add:

``` go
func emitEncodeBody(si StructInfo) string {
    var buf bytes.Buffer
    if si.Validatable {
        fmt.Fprintf(&buf, "\tif err := d.data.Validate(); err != nil {\n")
        fmt.Fprintf(&buf, "\t\treturn nil, fmt.Errorf(\"validation failed: %%w\", err)\n")
        fmt.Fprintf(&buf, "\t}\n")
    }
    for _, fi := range si.Fields {
        buf.WriteString(emitEncodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()"))
    }
    return buf.String()
}
```

**Step 4: Run tests to verify they pass**

Run:
`go test -v -run "TestEmit(Decode|Encode)Body(With|Without)Validation" ./generate/`

Expected: PASS

**Step 5: Commit**

    feat(generate): emit Validate() calls in decode and encode bodies

--------------------------------------------------------------------------------

### Task 3: Integration test --- end-to-end validation

**Files:**

- Test: `generate/integration_test.go`

**Step 1: Write the integration test**

Add to `generate/integration_test.go`:

``` go
func TestIntegrationValidation(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test in short mode")
    }

    dir := t.TempDir()

    repoRoot, err := filepath.Abs(filepath.Join("..", "."))
    if err != nil {
        t.Fatal(err)
    }

    writeFixture(t, dir, "go.mod", strings.Join([]string{
        "module example.com/validation",
        "",
        "go 1.26",
        "",
        "require github.com/amarbel-llc/tommy v0.0.0",
        "",
        "replace github.com/amarbel-llc/tommy => " + repoRoot,
        "",
    }, "\n"))

    writeFixture(t, dir, "config.go", `package validation

import "fmt"

//go:generate tommy generate
type Config struct {
    Port int    `+"`"+`toml:"port"`+"`"+`
    Name string `+"`"+`toml:"name"`+"`"+`
}

func (c Config) Validate() error {
    if c.Port < 1 || c.Port > 65535 {
        return fmt.Errorf("port must be 1-65535, got %d", c.Port)
    }
    return nil
}
`)

    if err := Generate(dir, "config.go"); err != nil {
        t.Fatalf("Generate: %v", err)
    }

    writeFixture(t, dir, "validation_test.go", `package validation

import (
    "strings"
    "testing"
)

func TestDecodeValidInput(t *testing.T) {
    input := []byte("port = 8080\nname = \"myapp\"\n")
    doc, err := DecodeConfig(input)
    if err != nil {
        t.Fatalf("DecodeConfig: %v", err)
    }
    if doc.Data().Port != 8080 {
        t.Fatalf("Port = %d, want 8080", doc.Data().Port)
    }
}

func TestDecodeInvalidInput(t *testing.T) {
    input := []byte("port = 0\nname = \"myapp\"\n")
    _, err := DecodeConfig(input)
    if err == nil {
        t.Fatal("expected validation error, got nil")
    }
    if !strings.Contains(err.Error(), "port must be 1-65535") {
        t.Fatalf("expected port validation error, got: %v", err)
    }
}

func TestEncodeInvalidState(t *testing.T) {
    input := []byte("port = 8080\nname = \"myapp\"\n")
    doc, err := DecodeConfig(input)
    if err != nil {
        t.Fatalf("DecodeConfig: %v", err)
    }
    doc.Data().Port = 0
    _, err = doc.Encode()
    if err == nil {
        t.Fatal("expected validation error on encode, got nil")
    }
    if !strings.Contains(err.Error(), "port must be 1-65535") {
        t.Fatalf("expected port validation error, got: %v", err)
    }
}
`)

    cmd := exec.Command("go", "test", "-v", "./...")
    cmd.Dir = dir
    cmd.Env = append(os.Environ(), "GOFLAGS=")
    if output, err := cmd.CombinedOutput(); err != nil {
        t.Fatalf("go test failed: %v\n%s", err, output)
    }
}
```

**Step 2: Run the integration test**

Run: `go test -v -run TestIntegrationValidation ./generate/`

Expected: PASS (since Tasks 1 and 2 are already implemented).

**Step 3: Commit**

    test(generate): add integration test for struct-level Validate() support

--------------------------------------------------------------------------------

### Task 4: Run full test suite

**Step 1: Run all tests**

Run: `go test -v ./...`

Expected: All existing tests pass, no regressions.

**Step 2: Commit (if any fixups needed)**

--------------------------------------------------------------------------------

### Task 5: Update CLAUDE.md

**Files:**

- Modify: `CLAUDE.md`

**Step 1: Add Validate() to the Interfaces section**

Under `## Interfaces`, add a bullet:

``` markdown
- `Validate() error` — When a struct implements this method, generated
  `Decode`/`Encode` methods call it automatically. Decode validates after all
  fields are set; Encode validates before writing to the CST.
```

**Step 2: Commit**

    docs: document Validate() interface support in CLAUDE.md
