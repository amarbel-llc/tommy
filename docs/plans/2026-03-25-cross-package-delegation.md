# Cross-Package Struct Delegation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** When a cross-package struct field has its own tommy codegen, emit
delegation calls (`DecodeInto`/`EncodeFrom`) instead of inlining field-by-field
decoding --- unblocking cross-package structs that contain unexported types
(#35).

**Architecture:** Add a new `FieldDelegatedStruct` kind that signals "this
cross-package struct has its own tommy codegen --- delegate." The template
generates a new `DecodeInto`/`EncodeFrom` function pair alongside the existing
`Decode{Name}`/`Encode`. The analyzer detects delegation eligibility by checking
whether the cross-package struct's fields include unexported types (which forces
delegation) or whether the type is from a different package and has tagged
fields (which enables optional delegation). The emitter generates delegation
calls in the consumer's code.

**Tech Stack:** `go/ast`, `go/types`, `golang.org/x/tools/go/packages`,
`text/template`, `fmt.Fprintf`

**Rollback:** Purely additive --- generated files with `DecodeInto`/`EncodeFrom`
can be deleted. Existing `Decode{Name}`/`Encode` functions are unchanged.

--------------------------------------------------------------------------------

### Task 1: Generate `DecodeInto` and `EncodeFrom` functions in template

**Promotion criteria:** N/A

**Files:**

- Modify: `generate/template.go`
- Test: `generate/template_test.go`

Every struct that gets `Decode{Name}` and `Encode` should also get:

``` go
func Decode{Name}Into(data *{Name}, doc *document.Document, container *cst.Node, consumed map[string]bool, keyPrefix string) error {
    // same decode logic as DecodeBody, but operating on provided container/consumed/prefix
}

func Encode{Name}From(data *{Name}, doc *document.Document, container *cst.Node) error {
    // same encode logic as EncodeBody, but operating on provided container
}
```

**Step 1: Write failing test**

Add a test in `template_test.go` that renders a simple struct and asserts the
output contains `func Decode{Name}Into(` and `func Encode{Name}From(`.

``` go
func TestTemplateRendersDecodeInto(t *testing.T) {
    si := StructInfo{
        Name: "Config",
        Fields: []FieldInfo{
            {GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
        },
    }
    var buf bytes.Buffer
    if err := RenderFile(&buf, "test", []StructInfo{si}); err != nil {
        t.Fatal(err)
    }
    output := buf.String()
    if !strings.Contains(output, "func DecodeConfigInto(") {
        t.Fatalf("expected DecodeConfigInto function in output:\n%s", output)
    }
    if !strings.Contains(output, "func EncodeConfigFrom(") {
        t.Fatalf("expected EncodeConfigFrom function in output:\n%s", output)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestTemplateRendersDecodeInto ./generate/` Expected: FAIL
--- output doesn't contain the new functions.

**Step 3: Add `DecodeInto` and `EncodeFrom` to template**

In `generate/template.go`, add to the `fileTemplate` constant after the
`Undecoded` method:

``` go
func Decode{{.Name}}Into(data *{{.Name}}, doc *document.Document, container *cst.Node, consumed map[string]bool, keyPrefix string) error {
{{emitDecodeInto .}}
    return nil
}

func Encode{{.Name}}From(data *{{.Name}}, doc *document.Document, container *cst.Node) error {
{{emitEncodeFrom .}}
    return nil
}
```

**Step 4: Add `emitDecodeIntoBody` and `emitEncodeFromBody` in emit.go**

These are variants of `emitDecodeBody`/`emitEncodeBody` that:

- Use `data` instead of `d.data` as the data path
- Use `doc` instead of `d.cstDoc` as the document variable
- Use `container` instead of `d.cstDoc.Root()` as the container expression
- Use `consumed` instead of `d.consumed` for consumed key tracking
- Prepend `keyPrefix +` to consumed key strings

``` go
func emitDecodeIntoBody(si StructInfo) string {
    var buf bytes.Buffer
    for _, fi := range si.Fields {
        buf.WriteString(emitDecodeField(fi, "data", "doc", "container", "\" + keyPrefix + \""))
    }
    return buf.String()
}

func emitEncodeFromBody(si StructInfo) string {
    var buf bytes.Buffer
    for _, fi := range si.Fields {
        buf.WriteString(emitEncodeField(fi, "data", "doc", "container"))
    }
    return buf.String()
}
```

Wire them into the template FuncMap:

``` go
"emitDecodeInto": func(si StructInfo) string { return emitDecodeIntoBody(si) },
"emitEncodeFrom": func(si StructInfo) string { return emitEncodeFromBody(si) },
```

**Step 5: Run test to verify it passes**

Run: `go test -v -run TestTemplateRendersDecodeInto ./generate/` Expected: PASS

**Step 6: Run full test suite**

Run: `go test -v ./generate/` Expected: All existing tests pass (the new
functions are additive).

**Step 7: Commit**

    feat(generate): add DecodeInto/EncodeFrom functions to generated output

    Every tommy-generated file now includes DecodeInto and EncodeFrom
    functions that accept an external document, container node, consumed
    map, and key prefix. These enable cross-package delegation where a
    consumer calls the target package's DecodeInto instead of inlining
    field-by-field decoding.

    Part of #35.

--------------------------------------------------------------------------------

### Task 2: Integration test --- verify `DecodeInto`/`EncodeFrom` compile and work

**Promotion criteria:** N/A

**Files:**

- Modify: `generate/integration_test.go`

**Step 1: Write integration test**

Add a test that generates code for a simple struct, then writes a test file that
calls `DecodeInto`/`EncodeFrom` directly.

``` go
func TestIntegrationDecodeIntoEncodeFrom(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test in short mode")
    }

    dir := t.TempDir()
    repoRoot, err := filepath.Abs(filepath.Join("..", "."))
    if err != nil {
        t.Fatal(err)
    }

    writeFixture(t, dir, "go.mod", strings.Join([]string{
        "module example.com/inttest",
        "",
        "go 1.26",
        "",
        "require github.com/amarbel-llc/tommy v0.0.0",
        "",
        "replace github.com/amarbel-llc/tommy => " + repoRoot,
        "",
    }, "\n"))

    writeFixture(t, dir, "config.go", `package inttest

//go:generate tommy generate
type Settings struct {
    Host string `+"`"+`toml:"host"`+"`"+`
    Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

    if err := Generate(dir, "config.go"); err != nil {
        t.Fatalf("Generate: %v", err)
    }

    writeFixture(t, dir, "inttest_test.go", `package inttest

import (
    "testing"

    "github.com/amarbel-llc/tommy/pkg/document"
)

func TestDecodeIntoRoundTrip(t *testing.T) {
    input := []byte("host = \"localhost\"\nport = 8080\n")

    doc, err := document.Parse(input)
    if err != nil {
        t.Fatal(err)
    }

    var data Settings
    consumed := make(map[string]bool)
    if err := DecodeSettingsInto(&data, doc, doc.Root(), consumed, ""); err != nil {
        t.Fatalf("DecodeSettingsInto: %v", err)
    }

    if data.Host != "localhost" {
        t.Fatalf("Host = %q, want \"localhost\"", data.Host)
    }
    if data.Port != 8080 {
        t.Fatalf("Port = %d, want 8080", data.Port)
    }

    // Modify and encode back
    data.Port = 9090
    if err := EncodeSettingsFrom(&data, doc, doc.Root()); err != nil {
        t.Fatalf("EncodeSettingsFrom: %v", err)
    }

    out := doc.Bytes()
    doc2, err := document.Parse(out)
    if err != nil {
        t.Fatalf("re-parse: %v", err)
    }

    var data2 Settings
    consumed2 := make(map[string]bool)
    if err := DecodeSettingsInto(&data2, doc2, doc2.Root(), consumed2, ""); err != nil {
        t.Fatalf("re-decode: %v", err)
    }
    if data2.Port != 9090 {
        t.Fatalf("re-decoded Port = %d, want 9090", data2.Port)
    }
}
`)

    cmd := exec.Command("go", "test", "-v", "./...")
    cmd.Dir = dir
    cmd.Env = append(os.Environ(), "GOFLAGS=")
    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("test failed:\n%s", output)
    }
}
```

**Step 2: Run test**

Run: `go test -v -run TestIntegrationDecodeIntoEncodeFrom ./generate/` Expected:
PASS (the functions were added in Task 1).

**Step 3: Commit**

    test(generate): integration test for DecodeInto/EncodeFrom

    Verifies the generated DecodeInto and EncodeFrom functions compile
    and correctly decode/encode when called with an external document.

    Part of #35.

--------------------------------------------------------------------------------

### Task 3: Add `FieldDelegatedStruct` kind and detection in analyzer

**Promotion criteria:** Once all cross-package struct fields use delegation,
remove the unexported-type error guard added earlier in this session.

**Files:**

- Modify: `generate/analyze.go`
- Modify: `generate/analyze_test.go`

**Step 1: Write failing test**

The test from the current session
(`TestAnalyzeCrossPackageUnexportedNestedStruct`) currently expects an error.
Change it to expect success with `FieldDelegatedStruct`. Also add a new test for
the simpler case: a cross-package struct with all exported fields should *also*
use delegation (it's always correct for cross-package structs).

``` go
func TestAnalyzeCrossPackageStructDelegation(t *testing.T) {
    dir := t.TempDir()

    extDir := filepath.Join(dir, "ext")
    writeFixture(t, extDir, "go.mod", "module example.com/ext\n\ngo 1.26\n")
    writeFixture(t, extDir, "ext.go", `package ext

type Inner struct {
    Name string `+"`"+`toml:"name"`+"`"+`
}
`)

    mainDir := filepath.Join(dir, "main")
    writeFixture(t, mainDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n")
    writeFixture(t, mainDir, "config.go", `package main

import "example.com/ext"

//go:generate tommy generate
type Config struct {
    Settings ext.Inner `+"`"+`toml:"settings"`+"`"+`
}
`)

    infos, err := Analyze(mainDir, "config.go")
    if err != nil {
        t.Fatal(err)
    }
    if len(infos) != 1 {
        t.Fatalf("expected 1 struct, got %d", len(infos))
    }
    f := infos[0].Fields[0]
    if f.Kind != FieldDelegatedStruct {
        t.Fatalf("expected FieldDelegatedStruct, got %v", f.Kind)
    }
    if f.TypeName != "ext.Inner" {
        t.Fatalf("expected TypeName ext.Inner, got %s", f.TypeName)
    }
    if f.ImportPath != "example.com/ext" {
        t.Fatalf("expected ImportPath example.com/ext, got %s", f.ImportPath)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestAnalyzeCrossPackageStructDelegation ./generate/`
Expected: FAIL --- `FieldDelegatedStruct` is undefined.

**Step 3: Add `FieldDelegatedStruct` kind**

In `generate/analyze.go`, add to the `FieldKind` const block:

``` go
FieldDelegatedStruct  // cross-package struct with its own tommy codegen
```

**Step 4: Modify `classifyFromType` to use delegation for cross-package
structs**

In the `*types.Named` case, replace the existing cross-package struct block:

``` go
// Check if underlying is struct
if structType, ok := t.Underlying().(*types.Struct); ok {
    fi.Kind = FieldDelegatedStruct
    fi.TypeName = obj.Pkg().Name() + "." + obj.Name()
    fi.ImportPath = obj.Pkg().Path()
    innerInfo, err := resolveStructFromTypes(pkg, obj.Name(), structType)
    if err != nil {
        return fi, err
    }
    fi.InnerInfo = &innerInfo
    return fi, nil
}
```

Also update the `*types.Pointer` case to handle `FieldDelegatedStruct`:

``` go
// Upgrade to pointer variant where applicable
if innerFi.Kind == FieldStruct {
    innerFi.Kind = FieldPointerStruct
} else if innerFi.Kind == FieldDelegatedStruct {
    innerFi.Kind = FieldPointerDelegatedStruct
}
```

Add `FieldPointerDelegatedStruct` to the const block as well.

Similarly, update `classifySelectorExpr` --- it calls `classifyFromType` for
non-TextMarshaler cross-package types, so delegation will flow through
naturally.

**Step 5: Update `TestAnalyzeCrossPackageUnexportedNestedStruct`**

Change this test to expect `FieldDelegatedStruct` (no error) instead of an
error. The inner fields still get resolved (for `InnerInfo`) but the consumer
code won't inline them --- it will delegate.

**Step 6: Remove the unexported-type error guard**

Remove the `!obj.Exported()` check added earlier, since delegation handles this
case.

**Step 7: Run tests**

Run: `go test -v -run 'TestAnalyzeCrossPackage' ./generate/` Expected: All pass.

**Step 8: Commit**

    feat(generate): add FieldDelegatedStruct kind for cross-package structs

    Cross-package struct fields are now classified as FieldDelegatedStruct
    instead of FieldStruct. This signals to the emitter that it should
    delegate to the target package's DecodeInto/EncodeFrom rather than
    inlining field-by-field decode/encode.

    Part of #35.

--------------------------------------------------------------------------------

### Task 4: Emit delegation calls for `FieldDelegatedStruct`

**Promotion criteria:** N/A

**Files:**

- Modify: `generate/emit.go`
- Modify: `generate/emit_test.go`

**Step 1: Write failing emit test**

``` go
func TestEmitDecodeDelegatedStruct(t *testing.T) {
    fi := FieldInfo{
        GoName:     "Settings",
        TomlKey:    "settings",
        Kind:       FieldDelegatedStruct,
        TypeName:   "ext.Inner",
        ImportPath: "example.com/ext",
    }
    code := emitDecodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()", "")
    if !strings.Contains(code, "ext.DecodeInnerInto") {
        t.Fatalf("expected delegation call, got:\n%s", code)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestEmitDecodeDelegatedStruct ./generate/` Expected: FAIL
--- no case for `FieldDelegatedStruct` in emitDecodeField.

**Step 3: Add `FieldDelegatedStruct` case to `emitDecodeField`**

``` go
case FieldDelegatedStruct:
    if containerExpr == "d.cstDoc.Root()" {
        fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
    } else {
        fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
            docVar, containerExpr, fi.TomlKey)
    }
    fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
    // Extract package name and type name from qualified name (e.g., "ext.Inner")
    parts := strings.SplitN(fi.TypeName, ".", 2)
    fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(&%s, %s, tableNode, d.consumed, %q); err != nil {\n",
        parts[0], parts[1], target, docVar, consumedKey+".")
    fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
    fmt.Fprintf(&buf, "\t\t}\n")
    fmt.Fprintf(&buf, "\t}\n")
```

**Step 4: Add `FieldDelegatedStruct` case to `emitEncodeField`**

``` go
case FieldDelegatedStruct:
    if containerExpr == "d.cstDoc.Root()" {
        fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
    } else {
        fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
            docVar, containerExpr, fi.TomlKey)
    }
    parts := strings.SplitN(fi.TypeName, ".", 2)
    fmt.Fprintf(&buf, "\t\tif err := %s.Encode%sFrom(&%s, %s, tableNode); err != nil {\n",
        parts[0], parts[1], source, docVar)
    fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
    fmt.Fprintf(&buf, "\t\t}\n")
    fmt.Fprintf(&buf, "\t}\n")
```

**Step 5: Add `FieldPointerDelegatedStruct` cases** (similar but with nil check
and pointer dereference).

**Step 6: Run tests**

Run: `go test -v -run TestEmitDecodeDelegatedStruct ./generate/` Expected: PASS

**Step 7: Commit**

    feat(generate): emit delegation calls for FieldDelegatedStruct

    The emitter now generates calls to DecodeInto/EncodeFrom for cross-
    package struct fields instead of inlining field-by-field decoding.

    Part of #35.

--------------------------------------------------------------------------------

### Task 5: Integration test --- cross-package delegation with unexported nested type

**Promotion criteria:** N/A

**Files:**

- Modify: `generate/integration_test.go`

**Step 1: Write integration test**

This is the core #35 test: a consumer uses a cross-package struct that contains
an unexported pointer struct field. Both packages get tommy codegen. The
consumer delegates to the cross-package type's `DecodeInto`/`EncodeFrom`.

``` go
func TestIntegrationCrossPackageDelegation(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test in short mode")
    }

    dir := t.TempDir()
    repoRoot, err := filepath.Abs(filepath.Join("..", "."))
    if err != nil {
        t.Fatal(err)
    }

    // External package with unexported nested struct
    extDir := filepath.Join(dir, "options")
    writeFixture(t, extDir, "go.mod", strings.Join([]string{
        "module example.com/test/options",
        "",
        "go 1.26",
        "",
        "require github.com/amarbel-llc/tommy v0.0.0",
        "",
        "replace github.com/amarbel-llc/tommy => " + repoRoot,
        "",
    }, "\n"))
    writeFixture(t, extDir, "options.go", `package options

type abbreviations struct {
    ZettelIds *bool `+"`"+`toml:"zettel_ids"`+"`"+`
    MarkIds   *bool `+"`"+`toml:"mark_ids"`+"`"+`
}

//go:generate tommy generate
type PrintOptions struct {
    Abbreviations *abbreviations `+"`"+`toml:"abbreviations"`+"`"+`
    PrintColors   *bool          `+"`"+`toml:"print-colors"`+"`"+`
}
`)

    // Generate code for the external package first
    if err := Generate(extDir, "options.go"); err != nil {
        t.Fatalf("Generate options: %v", err)
    }

    // Consumer package
    consumerDir := filepath.Join(dir, "consumer")
    writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
        "module example.com/test/consumer",
        "",
        "go 1.26",
        "",
        "require (",
        "\tgithub.com/amarbel-llc/tommy v0.0.0",
        "\texample.com/test/options v0.0.0",
        ")",
        "",
        "replace (",
        "\tgithub.com/amarbel-llc/tommy => " + repoRoot,
        "\texample.com/test/options => ../options",
        ")",
        "",
    }, "\n"))
    writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/test/options"

//go:generate tommy generate
type Config struct {
    Name         string               `+"`"+`toml:"name"`+"`"+`
    PrintOptions options.PrintOptions `+"`"+`toml:"cli-output"`+"`"+`
}
`)

    // Generate code for the consumer — this must NOT error
    if err := Generate(consumerDir, "config.go"); err != nil {
        t.Fatalf("Generate consumer: %v", err)
    }

    writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestCrossPackageDelegationRoundTrip(t *testing.T) {
    input := []byte("name = \"myapp\"\n\n[cli-output]\nprint-colors = true\n\n[cli-output.abbreviations]\nzettel_ids = true\nmark_ids = false\n")

    doc, err := DecodeConfig(input)
    if err != nil {
        t.Fatalf("DecodeConfig: %v", err)
    }

    cfg := doc.Data()
    if cfg.Name != "myapp" {
        t.Fatalf("Name = %q, want \"myapp\"", cfg.Name)
    }
    if cfg.PrintOptions.PrintColors == nil || !*cfg.PrintOptions.PrintColors {
        t.Fatal("PrintColors should be true")
    }
    if cfg.PrintOptions.Abbreviations == nil {
        t.Fatal("Abbreviations should not be nil")
    }
    if cfg.PrintOptions.Abbreviations.ZettelIds == nil || !*cfg.PrintOptions.Abbreviations.ZettelIds {
        t.Fatal("ZettelIds should be true")
    }

    // Modify and round-trip
    v := false
    cfg.PrintOptions.Abbreviations.ZettelIds = &v

    out, err := doc.Encode()
    if err != nil {
        t.Fatalf("Encode: %v", err)
    }

    doc2, err := DecodeConfig(out)
    if err != nil {
        t.Fatalf("re-decode: %v", err)
    }
    d2 := doc2.Data()
    if d2.PrintOptions.Abbreviations.ZettelIds == nil || *d2.PrintOptions.Abbreviations.ZettelIds {
        t.Fatal("re-decoded ZettelIds should be false")
    }
}
`)

    cmd := exec.Command("go", "test", "-v", "./...")
    cmd.Dir = consumerDir
    cmd.Env = append(os.Environ(), "GOFLAGS=")
    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("test failed:\n%s", output)
    }
}
```

**Step 2: Run test**

Run: `go test -v -run TestIntegrationCrossPackageDelegation ./generate/`
Expected: PASS

**Step 3: Commit**

    test(generate): integration test for cross-package delegation (#35)

    Verifies that a consumer struct with a cross-package field containing
    unexported nested types compiles and round-trips correctly via
    DecodeInto/EncodeFrom delegation.

    Closes #35.

--------------------------------------------------------------------------------

### Task 6: Handle `FieldDelegatedStruct` in `map[string]` and `[]` contexts

**Promotion criteria:** N/A

**Files:**

- Modify: `generate/analyze.go`
- Modify: `generate/emit.go`
- Modify: `generate/integration_test.go`

Cross-package struct fields also appear as `map[string]CrossPackageStruct` and
`[]CrossPackageStruct`. These currently use `FieldMapStringStruct` and
`FieldSliceStruct` with inlined field decoding. They need delegation variants
too.

**Step 1: Add `FieldMapStringDelegatedStruct` and `FieldSliceDelegatedStruct`**

Add to the `FieldKind` const block in `analyze.go`.

**Step 2: Update map and slice classification in `classifyFromType`**

In the `*types.Map` case, when the value is a cross-package `*types.Named`
struct, use `FieldMapStringDelegatedStruct` instead of `FieldMapStringStruct`.

In the `*types.Slice` case, when the element is a cross-package `*types.Named`
struct, use `FieldSliceDelegatedStruct` instead of `FieldSliceStruct`.

**Step 3: Add emit cases for map and slice delegation**

For `FieldMapStringDelegatedStruct`, emit:

``` go
{
    subTables := doc.FindSubTables("key")
    if len(subTables) > 0 {
        d.consumed["key"] = true
        d.data.Field = make(map[string]pkg.Type)
        for _, subTable := range subTables {
            mapKey := document.SubTableKey(subTable, "key")
            d.consumed["key." + mapKey] = true
            var entry pkg.Type
            if err := pkg.DecodeTypeInto(&entry, d.cstDoc, subTable, d.consumed, "key." + mapKey + "."); err != nil {
                return nil, fmt.Errorf("key.%s: %w", mapKey, err)
            }
            d.data.Field[mapKey] = entry
        }
    }
}
```

For `FieldSliceDelegatedStruct`, emit delegation within the array-of-tables
loop, calling `DecodeInto` for each entry instead of inlining fields.

**Step 4: Write integration tests for both patterns**

- `map[string]CrossPackageStruct` with delegation
- `[]CrossPackageStruct` with delegation (array-of-tables)

**Step 5: Run full test suite**

Run: `go test -v ./generate/` Expected: All pass.

**Step 6: Commit**

    feat(generate): delegation for map[string] and slice cross-package structs

    Cross-package struct fields in map values and slice elements now use
    DecodeInto/EncodeFrom delegation instead of inlining field-by-field
    decoding.

    Part of #35.

--------------------------------------------------------------------------------

### Task 7: Update existing cross-package struct tests

**Promotion criteria:** N/A

**Files:**

- Modify: `generate/analyze_test.go`
- Modify: `generate/integration_test.go`

Several existing tests assert `FieldStruct` or `FieldMapStringStruct` for
cross-package struct fields. These need updating to expect the delegated
variants.

**Step 1: Update `TestAnalyzeCrossPackageEmbeddedStruct`**

Embedded cross-package structs promote their fields --- delegation doesn't apply
to promoted fields. Verify this test still passes unchanged.

**Step 2: Update `TestIntegrationCrossPackageNamedStruct`**

This test uses `other.Config` as a named field. The generated code will now
delegate. The external `other` package needs tommy codegen too, so add a
`//go:generate tommy generate` directive and run `Generate` on it before the
consumer.

**Step 3: Update `TestIntegrationMapStringCrossPackageStruct`**

Same pattern --- the cross-package struct needs its own tommy codegen.

**Step 4: Run full test suite**

Run: `go test -v ./generate/` Expected: All pass.

**Step 5: Commit**

    test(generate): update cross-package tests for delegation

    Existing cross-package struct tests now generate tommy code for both
    packages, matching the delegation pattern.
