# Codegen B Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Generate compile-time companion document types that pair user structs
with direct CST node references, replacing runtime reflection for round-trip
TOML encoding.

**Architecture:** A `tommy generate` CLI subcommand invokes the `generate/`
package, which uses `go/ast` + `go/types` to analyze structs with
`//go:generate tommy generate` directives, then emits `_tommy.go` files using
`text/template` for the file skeleton and `fmt.Fprintf` for per-field
decode/encode logic.

**Tech Stack:** `go/ast`, `go/types`, `golang.org/x/tools/go/packages`,
`text/template`, `fmt.Fprintf`

**Rollback:** Purely additive --- delete generated `_tommy.go` files and remove
`//go:generate` directives. `pkg/marshal` remains fully functional.

--------------------------------------------------------------------------------

### Task 1: Define TOMLUnmarshaler and TOMLMarshaler interfaces

**Promotion criteria:** N/A

**Files:**

- Create: `pkg/tommy.go`
- Test: `pkg/tommy_test.go`

**Step 1: Write the failing test**

``` go
// pkg/tommy_test.go
package tommy

import "testing"

type mockUnmarshaler struct{ called bool }

func (m *mockUnmarshaler) UnmarshalTOML(data any) error {
    m.called = true
    return nil
}

type mockMarshaler struct{}

func (m *mockMarshaler) MarshalTOML() (any, error) {
    return "test", nil
}

func TestInterfaceCompliance(t *testing.T) {
    var u TOMLUnmarshaler = &mockUnmarshaler{}
    if err := u.UnmarshalTOML("hello"); err != nil {
        t.Fatal(err)
    }

    var m TOMLMarshaler = &mockMarshaler{}
    v, err := m.MarshalTOML()
    if err != nil {
        t.Fatal(err)
    }
    if v != "test" {
        t.Fatalf("expected %q, got %q", "test", v)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestInterfaceCompliance ./pkg/`

Expected: FAIL --- `TOMLUnmarshaler` and `TOMLMarshaler` not defined.

**Step 3: Write minimal implementation**

``` go
// pkg/tommy.go
package tommy

type TOMLUnmarshaler interface {
    UnmarshalTOML(data any) error
}

type TOMLMarshaler interface {
    MarshalTOML() (any, error)
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestInterfaceCompliance ./pkg/`

Expected: PASS

**Step 5: Commit**

``` text
feat(tommy): define TOMLUnmarshaler and TOMLMarshaler interfaces
```

--------------------------------------------------------------------------------

### Task 2: Add GetRawFromContainer to document package

**Promotion criteria:** N/A

**Files:**

- Modify: `pkg/document/document.go`
- Modify: `pkg/document/document_test.go`

**Step 1: Write the failing test**

``` go
// append to pkg/document/document_test.go

func TestGetRawFromContainerString(t *testing.T) {
    input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")
    doc, err := Parse(input)
    if err != nil {
        t.Fatal(err)
    }
    nodes := doc.FindArrayTableNodes("servers")
    if len(nodes) != 1 {
        t.Fatalf("expected 1 node, got %d", len(nodes))
    }
    raw, err := GetRawFromContainer(doc, nodes[0], "command")
    if err != nil {
        t.Fatal(err)
    }
    s, ok := raw.(string)
    if !ok {
        t.Fatalf("expected string, got %T", raw)
    }
    if s != "grit mcp" {
        t.Fatalf("expected %q, got %q", "grit mcp", s)
    }
}

func TestGetRawFromContainerBool(t *testing.T) {
    input := []byte("[[entries]]\nenabled = true\n")
    doc, err := Parse(input)
    if err != nil {
        t.Fatal(err)
    }
    nodes := doc.FindArrayTableNodes("entries")
    raw, err := GetRawFromContainer(doc, nodes[0], "enabled")
    if err != nil {
        t.Fatal(err)
    }
    b, ok := raw.(bool)
    if !ok {
        t.Fatalf("expected bool, got %T", raw)
    }
    if !b {
        t.Fatal("expected true")
    }
}

func TestGetRawFromContainerArray(t *testing.T) {
    input := []byte("[[entries]]\ntags = [\"a\", \"b\"]\n")
    doc, err := Parse(input)
    if err != nil {
        t.Fatal(err)
    }
    nodes := doc.FindArrayTableNodes("entries")
    raw, err := GetRawFromContainer(doc, nodes[0], "tags")
    if err != nil {
        t.Fatal(err)
    }
    arr, ok := raw.([]any)
    if !ok {
        t.Fatalf("expected []any, got %T", raw)
    }
    if len(arr) != 2 {
        t.Fatalf("expected 2 elements, got %d", len(arr))
    }
    if arr[0] != "a" || arr[1] != "b" {
        t.Fatalf("expected [a, b], got %v", arr)
    }
}

func TestGetRawFromContainerNotFound(t *testing.T) {
    input := []byte("[[entries]]\nname = \"x\"\n")
    doc, err := Parse(input)
    if err != nil {
        t.Fatal(err)
    }
    nodes := doc.FindArrayTableNodes("entries")
    _, err = GetRawFromContainer(doc, nodes[0], "missing")
    if err == nil {
        t.Fatal("expected error for missing key")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestGetRawFromContainer ./pkg/document/`

Expected: FAIL --- `GetRawFromContainer` not defined.

**Step 3: Write minimal implementation**

Add to `pkg/document/document.go`:

``` go
func GetRawFromContainer(doc *Document, container *cst.Node, key string) (any, error) {
    valueNode, err := findValueInContainer(container, key)
    if err != nil {
        return nil, err
    }
    return convertNodeToRaw(valueNode)
}

func convertNodeToRaw(node *cst.Node) (any, error) {
    switch node.Kind {
    case cst.NodeString:
        return stripQuotes(string(node.Raw)), nil
    case cst.NodeInteger:
        v, err := strconv.ParseInt(string(node.Raw), 10, 64)
        if err != nil {
            return nil, err
        }
        return v, nil
    case cst.NodeFloat:
        v, err := strconv.ParseFloat(string(node.Raw), 64)
        if err != nil {
            return nil, err
        }
        return v, nil
    case cst.NodeBool:
        v, err := strconv.ParseBool(string(node.Raw))
        if err != nil {
            return nil, err
        }
        return v, nil
    case cst.NodeArray:
        return convertArrayToRaw(node)
    default:
        return string(node.Raw), nil
    }
}

func convertArrayToRaw(node *cst.Node) ([]any, error) {
    var result []any
    for _, child := range node.Children {
        switch child.Kind {
        case cst.NodeString, cst.NodeInteger, cst.NodeFloat, cst.NodeBool:
            v, err := convertNodeToRaw(child)
            if err != nil {
                return nil, err
            }
            result = append(result, v)
        }
    }
    return result, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestGetRawFromContainer ./pkg/document/`

Expected: PASS

**Step 5: Commit**

``` text
feat(document): add GetRawFromContainer for custom unmarshal types
```

--------------------------------------------------------------------------------

### Task 3: Add FindTableInContainer to document package

**Promotion criteria:** N/A

**Files:**

- Modify: `pkg/document/document.go`
- Modify: `pkg/document/document_test.go`

**Step 1: Write the failing test**

``` go
// append to pkg/document/document_test.go

func TestFindTableInContainer(t *testing.T) {
    input := []byte("[[servers]]\nname = \"grit\"\n[servers.annotations]\nreadOnlyHint = true\n")
    doc, err := Parse(input)
    if err != nil {
        t.Fatal(err)
    }
    nodes := doc.FindArrayTableNodes("servers")
    if len(nodes) != 1 {
        t.Fatalf("expected 1 node, got %d", len(nodes))
    }
    tableNode := doc.FindTableInContainer(nodes[0], "annotations")
    if tableNode == nil {
        t.Fatal("expected to find annotations table")
    }
    v, err := GetFromContainer[bool](doc, tableNode, "readOnlyHint")
    if err != nil {
        t.Fatal(err)
    }
    if !v {
        t.Fatal("expected readOnlyHint true")
    }
}

func TestFindTableInContainerNotFound(t *testing.T) {
    input := []byte("[[servers]]\nname = \"grit\"\n")
    doc, err := Parse(input)
    if err != nil {
        t.Fatal(err)
    }
    nodes := doc.FindArrayTableNodes("servers")
    tableNode := doc.FindTableInContainer(nodes[0], "missing")
    if tableNode != nil {
        t.Fatal("expected nil for missing table")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestFindTableInContainer ./pkg/document/`

Expected: FAIL --- `FindTableInContainer` not defined.

**Step 3: Write minimal implementation**

Add to `pkg/document/document.go`:

``` go
func (doc *Document) FindTableInContainer(container *cst.Node, key string) *cst.Node {
    for _, child := range container.Children {
        if child.Kind == cst.NodeTable && tableHeaderKey(child) == key {
            return child
        }
    }

    containerKey := tableHeaderKey(container)
    if containerKey == "" {
        return nil
    }
    qualifiedKey := containerKey + "." + key
    for _, child := range doc.root.Children {
        if child.Kind == cst.NodeTable && tableHeaderKey(child) == qualifiedKey {
            return child
        }
    }
    return nil
}
```

Note: Sub-tables within array-of-tables entries like `[servers.annotations]` are
siblings at the document root level in the CST, not children of the
`[[servers]]` node. The implementation checks both locations.

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestFindTableInContainer ./pkg/document/`

Expected: PASS

**Step 5: Commit**

``` text
feat(document): add FindTableInContainer for pointer-to-struct fields
```

--------------------------------------------------------------------------------

### Task 4: Create generate package --- analysis model

**Promotion criteria:** N/A

**Files:**

- Create: `generate/analyze.go`
- Create: `generate/analyze_test.go`

**Step 1: Write the failing test**

Create a test fixture directory with a simple struct, then test the analyzer
extracts the correct `StructInfo`.

``` go
// generate/analyze_test.go
package generate

import (
    "os"
    "path/filepath"
    "testing"
)

func writeFixture(t *testing.T, dir, filename, content string) {
    t.Helper()
    if err := os.MkdirAll(dir, 0o755); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
        t.Fatal(err)
    }
}

func TestAnalyzePrimitiveFields(t *testing.T) {
    dir := t.TempDir()
    writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
    writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
    Name    string  `+"`"+`toml:"name"`+"`"+`
    Port    int     `+"`"+`toml:"port"`+"`"+`
    Enabled bool    `+"`"+`toml:"enabled"`+"`"+`
}
`)

    infos, err := Analyze(dir, "config.go")
    if err != nil {
        t.Fatal(err)
    }
    if len(infos) != 1 {
        t.Fatalf("expected 1 struct, got %d", len(infos))
    }
    si := infos[0]
    if si.Name != "Config" {
        t.Fatalf("expected name Config, got %s", si.Name)
    }
    if len(si.Fields) != 3 {
        t.Fatalf("expected 3 fields, got %d", len(si.Fields))
    }
    if si.Fields[0].Kind != FieldPrimitive {
        t.Fatalf("expected FieldPrimitive, got %v", si.Fields[0].Kind)
    }
    if si.Fields[0].GoName != "Name" {
        t.Fatalf("expected GoName Name, got %s", si.Fields[0].GoName)
    }
    if si.Fields[0].TomlKey != "name" {
        t.Fatalf("expected TomlKey name, got %s", si.Fields[0].TomlKey)
    }
}

func TestAnalyzeSliceOfStructs(t *testing.T) {
    dir := t.TempDir()
    writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
    writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
    Servers []Server `+"`"+`toml:"servers"`+"`"+`
}

type Server struct {
    Name    string `+"`"+`toml:"name"`+"`"+`
    Command string `+"`"+`toml:"command"`+"`"+`
}
`)

    infos, err := Analyze(dir, "config.go")
    if err != nil {
        t.Fatal(err)
    }
    if len(infos) != 1 {
        t.Fatalf("expected 1 struct, got %d", len(infos))
    }
    f := infos[0].Fields[0]
    if f.Kind != FieldSliceStruct {
        t.Fatalf("expected FieldSliceStruct, got %v", f.Kind)
    }
    if f.TypeName != "Server" {
        t.Fatalf("expected TypeName Server, got %s", f.TypeName)
    }
    if f.InnerInfo == nil {
        t.Fatal("expected InnerInfo to be set")
    }
    if len(f.InnerInfo.Fields) != 2 {
        t.Fatalf("expected 2 inner fields, got %d", len(f.InnerInfo.Fields))
    }
}

func TestAnalyzeUnsupportedTypeErrors(t *testing.T) {
    dir := t.TempDir()
    writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
    writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
    Data map[string]string `+"`"+`toml:"data"`+"`"+`
}
`)

    _, err := Analyze(dir, "config.go")
    if err == nil {
        t.Fatal("expected error for unsupported map type")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestAnalyze ./generate/`

Expected: FAIL --- package and `Analyze` function don't exist.

**Step 3: Write minimal implementation**

``` go
// generate/analyze.go
package generate

import (
    "fmt"
    "go/ast"
    "go/token"
    "strings"

    "golang.org/x/tools/go/packages"
)

type FieldKind int

const (
    FieldPrimitive      FieldKind = iota // string, int, int64, float64, bool
    FieldStruct                          // nested struct with toml tags
    FieldPointerStruct                   // *SomeStruct
    FieldSlicePrimitive                  // []int, []string
    FieldSliceStruct                     // []Server (array-of-tables)
    FieldCustom                          // implements TOMLUnmarshaler
    FieldPointerPrimitive                // *bool, *int, etc.
)

type StructInfo struct {
    Name   string
    Fields []FieldInfo
}

type FieldInfo struct {
    GoName    string
    TomlKey   string
    Kind      FieldKind
    ElemType  string
    TypeName  string
    InnerInfo *StructInfo
}

func Analyze(dir, filename string) ([]StructInfo, error) {
    cfg := &packages.Config{
        Mode: packages.NeedName |
            packages.NeedTypes |
            packages.NeedTypesInfo |
            packages.NeedSyntax |
            packages.NeedFiles,
        Dir:  dir,
        Fset: token.NewFileSet(),
    }

    pkgs, err := packages.Load(cfg, ".")
    if err != nil {
        return nil, fmt.Errorf("loading package: %w", err)
    }

    if len(pkgs) == 0 {
        return nil, fmt.Errorf("no packages found in %s", dir)
    }

    pkg := pkgs[0]
    if len(pkg.Errors) > 0 {
        return nil, fmt.Errorf("package errors: %v", pkg.Errors[0])
    }

    var targetFile *ast.File
    for i, f := range pkg.CompiledGoFiles {
        if strings.HasSuffix(f, filename) {
            targetFile = pkg.Syntax[i]
            break
        }
    }
    if targetFile == nil {
        return nil, fmt.Errorf("file %s not found in package", filename)
    }

    var infos []StructInfo

    for _, decl := range targetFile.Decls {
        genDecl, ok := decl.(*ast.GenDecl)
        if !ok || genDecl.Tok != token.TYPE {
            continue
        }

        if !hasGenerateDirective(pkg.Fset, targetFile, genDecl) {
            continue
        }

        for _, spec := range genDecl.Specs {
            typeSpec, ok := spec.(*ast.TypeSpec)
            if !ok {
                continue
            }
            structType, ok := typeSpec.Type.(*ast.StructType)
            if !ok {
                continue
            }

            si, err := analyzeStruct(pkg, typeSpec.Name.Name, structType)
            if err != nil {
                return nil, err
            }
            infos = append(infos, si)
        }
    }

    return infos, nil
}

func hasGenerateDirective(fset *token.FileSet, file *ast.File, decl *ast.GenDecl) bool {
    declLine := fset.Position(decl.Pos()).Line
    for _, cg := range file.Comments {
        for _, c := range cg.List {
            commentLine := fset.Position(c.Pos()).Line
            if commentLine == declLine-1 &&
                strings.Contains(c.Text, "//go:generate tommy generate") {
                return true
            }
        }
    }
    return false
}

func analyzeStruct(pkg *packages.Package, name string, st *ast.StructType) (StructInfo, error) {
    si := StructInfo{Name: name}

    for _, field := range st.Fields.List {
        if field.Tag == nil {
            continue
        }
        tag := field.Tag.Value
        tomlKey := extractTomlTag(tag)
        if tomlKey == "" {
            continue
        }

        for _, ident := range field.Names {
            fi, err := classifyField(pkg, ident.Name, tomlKey, field.Type)
            if err != nil {
                return si, fmt.Errorf("field %s.%s: %w", name, ident.Name, err)
            }
            si.Fields = append(si.Fields, fi)
        }
    }

    return si, nil
}

func extractTomlTag(raw string) string {
    tag := strings.Trim(raw, "`")
    idx := strings.Index(tag, `toml:"`)
    if idx < 0 {
        return ""
    }
    rest := tag[idx+6:]
    end := strings.IndexByte(rest, '"')
    if end < 0 {
        return ""
    }
    name, _, _ := strings.Cut(rest[:end], ",")
    return name
}

var primitiveTypes = map[string]bool{
    "string":  true,
    "int":     true,
    "int64":   true,
    "float64": true,
    "bool":    true,
}

func classifyField(pkg *packages.Package, goName, tomlKey string, expr ast.Expr) (FieldInfo, error) {
    fi := FieldInfo{GoName: goName, TomlKey: tomlKey}

    switch t := expr.(type) {
    case *ast.Ident:
        if primitiveTypes[t.Name] {
            fi.Kind = FieldPrimitive
            fi.TypeName = t.Name
            return fi, nil
        }
        return classifyNamedType(pkg, fi, t.Name)

    case *ast.StarExpr:
        inner, ok := t.X.(*ast.Ident)
        if !ok {
            return fi, fmt.Errorf("unsupported pointer type")
        }
        if primitiveTypes[inner.Name] {
            fi.Kind = FieldPointerPrimitive
            fi.TypeName = inner.Name
            return fi, nil
        }
        fi.Kind = FieldPointerStruct
        fi.TypeName = inner.Name
        innerInfo, err := resolveStructByName(pkg, inner.Name)
        if err != nil {
            return fi, err
        }
        fi.InnerInfo = &innerInfo
        return fi, nil

    case *ast.ArrayType:
        elemIdent, ok := t.Elt.(*ast.Ident)
        if !ok {
            return fi, fmt.Errorf("unsupported slice element type")
        }
        fi.ElemType = elemIdent.Name
        if primitiveTypes[elemIdent.Name] {
            fi.Kind = FieldSlicePrimitive
            return fi, nil
        }
        fi.Kind = FieldSliceStruct
        fi.TypeName = elemIdent.Name
        innerInfo, err := resolveStructByName(pkg, elemIdent.Name)
        if err != nil {
            return fi, err
        }
        fi.InnerInfo = &innerInfo
        return fi, nil

    default:
        return fi, fmt.Errorf("unsupported type %T", expr)
    }
}

func classifyNamedType(pkg *packages.Package, fi FieldInfo, typeName string) (FieldInfo, error) {
    obj := pkg.Types.Scope().Lookup(typeName)
    if obj == nil {
        return fi, fmt.Errorf("type %s not found", typeName)
    }

    unmarshalerIface := "UnmarshalTOML"
    for i := range obj.Type().NumMethods() {
        if obj.Type().Method(i).Name() == unmarshalerIface {
            fi.Kind = FieldCustom
            fi.TypeName = typeName
            return fi, nil
        }
    }

    ptrMethods := obj.Type().Underlying()
    _ = ptrMethods // check pointer receiver methods too
    // Also check pointer receiver
    ptrType := types.NewPointer(obj.Type())
    mset := types.NewMethodSet(ptrType)
    for i := range mset.Len() {
        if mset.At(i).Obj().Name() == unmarshalerIface {
            fi.Kind = FieldCustom
            fi.TypeName = typeName
            return fi, nil
        }
    }

    fi.Kind = FieldStruct
    fi.TypeName = typeName
    innerInfo, err := resolveStructByName(pkg, typeName)
    if err != nil {
        return fi, err
    }
    fi.InnerInfo = &innerInfo
    return fi, nil
}

func resolveStructByName(pkg *packages.Package, name string) (StructInfo, error) {
    for _, file := range pkg.Syntax {
        for _, decl := range file.Decls {
            genDecl, ok := decl.(*ast.GenDecl)
            if !ok || genDecl.Tok != token.TYPE {
                continue
            }
            for _, spec := range genDecl.Specs {
                typeSpec, ok := spec.(*ast.TypeSpec)
                if !ok {
                    continue
                }
                if typeSpec.Name.Name != name {
                    continue
                }
                structType, ok := typeSpec.Type.(*ast.StructType)
                if !ok {
                    return StructInfo{}, fmt.Errorf("%s is not a struct", name)
                }
                return analyzeStruct(pkg, name, structType)
            }
        }
    }
    return StructInfo{}, fmt.Errorf("struct %s not found in package", name)
}
```

Note: The `classifyNamedType` function needs `import "go/types"` added to the
import block.

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestAnalyze ./generate/`

Expected: PASS

**Step 5: Commit**

``` text
feat(generate): add struct analyzer with field classification
```

--------------------------------------------------------------------------------

### Task 5: Create generate package --- template skeleton

**Promotion criteria:** N/A

**Files:**

- Create: `generate/template.go`
- Create: `generate/template_test.go`

**Step 1: Write the failing test**

``` go
// generate/template_test.go
package generate

import (
    "bytes"
    "strings"
    "testing"
)

func TestTemplateRendersHeader(t *testing.T) {
    si := StructInfo{
        Name: "Config",
        Fields: []FieldInfo{
            {GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
        },
    }

    var buf bytes.Buffer
    err := RenderFile(&buf, "testpkg", []StructInfo{si})
    if err != nil {
        t.Fatal(err)
    }

    out := buf.String()
    if !strings.Contains(out, "Code generated by tommy; DO NOT EDIT.") {
        t.Fatal("missing generated header")
    }
    if !strings.Contains(out, "package testpkg") {
        t.Fatal("missing package declaration")
    }
    if !strings.Contains(out, "ConfigDocument") {
        t.Fatal("missing ConfigDocument type")
    }
    if !strings.Contains(out, "DecodeConfig") {
        t.Fatal("missing DecodeConfig function")
    }
    if !strings.Contains(out, "func (d *ConfigDocument) Data() *Config") {
        t.Fatal("missing Data() method")
    }
    if !strings.Contains(out, "func (d *ConfigDocument) Encode() ([]byte, error)") {
        t.Fatal("missing Encode() method")
    }
}

func TestTemplateRendersUnexportedHandle(t *testing.T) {
    si := StructInfo{
        Name: "Config",
        Fields: []FieldInfo{
            {
                GoName: "Servers", TomlKey: "servers", Kind: FieldSliceStruct,
                TypeName: "Server",
                InnerInfo: &StructInfo{
                    Name: "Server",
                    Fields: []FieldInfo{
                        {GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
                    },
                },
            },
        },
    }

    var buf bytes.Buffer
    err := RenderFile(&buf, "testpkg", []StructInfo{si})
    if err != nil {
        t.Fatal(err)
    }

    out := buf.String()
    if !strings.Contains(out, "serverHandle") {
        t.Fatal("missing unexported serverHandle type")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestTemplate ./generate/`

Expected: FAIL --- `RenderFile` not defined.

**Step 3: Write minimal implementation**

``` go
// generate/template.go
package generate

import (
    "io"
    "strings"
    "text/template"
    "unicode"
)

var fileTmpl = template.Must(template.New("file").Funcs(template.FuncMap{
    "unexport": unexport,
    "lower":    strings.ToLower,
}).Parse(`// Code generated by tommy; DO NOT EDIT.

package {{.Package}}

import (
    "fmt"

    "github.com/amarbel-llc/tommy/pkg/cst"
    "github.com/amarbel-llc/tommy/pkg/document"
)

// Ensure imports are used.
var (
    _ = fmt.Errorf
    _ cst.NodeKind
)
{{range .Structs}}
{{- range .Fields}}{{if eq .Kind 4}}
type {{unexport .TypeName}}Handle struct {
    node *cst.Node
}
{{end}}{{end}}
type {{.Name}}Document struct {
    data   {{.Name}}
    cstDoc *document.Document
{{- range .Fields}}{{if eq .Kind 4}}
    {{unexport .GoName}} []{{unexport .TypeName}}Handle
{{- end}}{{end}}
}

func Decode{{.Name}}(input []byte) (*{{.Name}}Document, error) {
    doc, err := document.Parse(input)
    if err != nil {
        return nil, err
    }

    d := &{{.Name}}Document{cstDoc: doc}

    {{emitDecode .}}

    return d, nil
}

func (d *{{.Name}}Document) Data() *{{.Name}} { return &d.data }

func (d *{{.Name}}Document) Encode() ([]byte, error) {
    {{emitEncode .}}

    return d.cstDoc.Bytes(), nil
}
{{end}}`))

func unexport(s string) string {
    if s == "" {
        return s
    }
    runes := []rune(s)
    runes[0] = unicode.ToLower(runes[0])
    return string(runes)
}

type fileData struct {
    Package string
    Structs []StructInfo
}

func RenderFile(w io.Writer, pkg string, structs []StructInfo) error {
    tmpl := template.Must(template.New("file").Funcs(template.FuncMap{
        "unexport":   unexport,
        "lower":      strings.ToLower,
        "emitDecode": func(si StructInfo) string { return emitDecodeBody(si) },
        "emitEncode": func(si StructInfo) string { return emitEncodeBody(si) },
    }).Parse(fileTemplate))

    return tmpl.Execute(w, fileData{
        Package: pkg,
        Structs: structs,
    })
}
```

The `fileTemplate` string and `emitDecodeBody`/`emitEncodeBody` functions will
be stubs initially --- they are filled in by Task 6.

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestTemplate ./generate/`

Expected: PASS

**Step 5: Commit**

``` text
feat(generate): add template skeleton for companion document types
```

--------------------------------------------------------------------------------

### Task 6: Create generate package --- field emission

**Promotion criteria:** N/A

**Files:**

- Create: `generate/emit.go`
- Create: `generate/emit_test.go`

**Step 1: Write the failing test**

Test that the emitter produces correct decode/encode code for each field kind.

``` go
// generate/emit_test.go
package generate

import (
    "strings"
    "testing"
)

func TestEmitDecodePrimitive(t *testing.T) {
    fi := FieldInfo{GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"}
    code := emitDecodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()")
    if !strings.Contains(code, `GetFromContainer[string]`) {
        t.Fatalf("expected GetFromContainer[string], got:\n%s", code)
    }
    if !strings.Contains(code, `d.data.Name`) {
        t.Fatalf("expected d.data.Name, got:\n%s", code)
    }
}

func TestEmitDecodeSliceStruct(t *testing.T) {
    fi := FieldInfo{
        GoName: "Servers", TomlKey: "servers", Kind: FieldSliceStruct,
        TypeName: "Server",
        InnerInfo: &StructInfo{
            Name: "Server",
            Fields: []FieldInfo{
                {GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
            },
        },
    }
    code := emitDecodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()")
    if !strings.Contains(code, `FindArrayTableNodes("servers")`) {
        t.Fatalf("expected FindArrayTableNodes, got:\n%s", code)
    }
    if !strings.Contains(code, `serverHandle`) {
        t.Fatalf("expected serverHandle, got:\n%s", code)
    }
}

func TestEmitDecodePointerPrimitive(t *testing.T) {
    fi := FieldInfo{GoName: "Enabled", TomlKey: "enabled", Kind: FieldPointerPrimitive, TypeName: "bool"}
    code := emitDecodeField(fi, "d.data", "d.cstDoc", "container")
    if !strings.Contains(code, `GetFromContainer[bool]`) {
        t.Fatalf("expected GetFromContainer[bool], got:\n%s", code)
    }
    if !strings.Contains(code, `&v`) {
        t.Fatalf("expected pointer assignment, got:\n%s", code)
    }
}

func TestEmitDecodeCustom(t *testing.T) {
    fi := FieldInfo{GoName: "Command", TomlKey: "command", Kind: FieldCustom, TypeName: "Command"}
    code := emitDecodeField(fi, "d.data", "d.cstDoc", "container")
    if !strings.Contains(code, `GetRawFromContainer`) {
        t.Fatalf("expected GetRawFromContainer, got:\n%s", code)
    }
    if !strings.Contains(code, `UnmarshalTOML`) {
        t.Fatalf("expected UnmarshalTOML call, got:\n%s", code)
    }
}

func TestEmitDecodePointerStruct(t *testing.T) {
    fi := FieldInfo{
        GoName: "Annotations", TomlKey: "annotations", Kind: FieldPointerStruct,
        TypeName: "AnnotationFilter",
        InnerInfo: &StructInfo{
            Name: "AnnotationFilter",
            Fields: []FieldInfo{
                {GoName: "ReadOnlyHint", TomlKey: "readOnlyHint", Kind: FieldPointerPrimitive, TypeName: "bool"},
            },
        },
    }
    code := emitDecodeField(fi, "d.data", "d.cstDoc", "container")
    if !strings.Contains(code, `FindTableInContainer`) {
        t.Fatalf("expected FindTableInContainer, got:\n%s", code)
    }
    if !strings.Contains(code, `AnnotationFilter{}`) {
        t.Fatalf("expected AnnotationFilter construction, got:\n%s", code)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestEmitDecode ./generate/`

Expected: FAIL --- `emitDecodeField` not defined.

**Step 3: Write minimal implementation**

``` go
// generate/emit.go
package generate

import (
    "bytes"
    "fmt"
    "unicode"
)

func emitDecodeBody(si StructInfo) string {
    var buf bytes.Buffer
    for _, fi := range si.Fields {
        buf.WriteString(emitDecodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()"))
    }
    return buf.String()
}

func emitEncodeBody(si StructInfo) string {
    var buf bytes.Buffer
    for _, fi := range si.Fields {
        buf.WriteString(emitEncodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()"))
    }
    return buf.String()
}

func emitDecodeField(fi FieldInfo, dataPath, docVar, containerExpr string) string {
    var buf bytes.Buffer
    target := dataPath + "." + fi.GoName

    switch fi.Kind {
    case FieldPrimitive:
        fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[%s](%s, %s, %q); err == nil {\n",
            fi.TypeName, docVar, containerExpr, fi.TomlKey)
        fmt.Fprintf(&buf, "\t\t%s = v\n", target)
        fmt.Fprintf(&buf, "\t}\n")

    case FieldPointerPrimitive:
        fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[%s](%s, %s, %q); err == nil {\n",
            fi.TypeName, docVar, containerExpr, fi.TomlKey)
        fmt.Fprintf(&buf, "\t\t%s = &v\n", target)
        fmt.Fprintf(&buf, "\t}\n")

    case FieldCustom:
        fmt.Fprintf(&buf, "\tif raw, err := document.GetRawFromContainer(%s, %s, %q); err == nil {\n",
            docVar, containerExpr, fi.TomlKey)
        fmt.Fprintf(&buf, "\t\tif err := %s.UnmarshalTOML(raw); err != nil {\n", target)
        fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
        fmt.Fprintf(&buf, "\t\t}\n")
        fmt.Fprintf(&buf, "\t}\n")

    case FieldStruct:
        if fi.InnerInfo != nil {
            tableExpr := fmt.Sprintf("findTableNode(%s.Root(), %q)", docVar, fi.TomlKey)
            fmt.Fprintf(&buf, "\tif tableNode := %s; tableNode != nil {\n", tableExpr)
            for _, inner := range fi.InnerInfo.Fields {
                code := emitDecodeField(inner, target, docVar, "tableNode")
                buf.WriteString(code)
            }
            fmt.Fprintf(&buf, "\t}\n")
        }

    case FieldPointerStruct:
        if fi.InnerInfo != nil {
            fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
                docVar, containerExpr, fi.TomlKey)
            fmt.Fprintf(&buf, "\t\tv := &%s{}\n", fi.TypeName)
            for _, inner := range fi.InnerInfo.Fields {
                code := emitDecodeField(inner, "v", docVar, "tableNode")
                buf.WriteString("\t" + code)
            }
            fmt.Fprintf(&buf, "\t\t%s = v\n", target)
            fmt.Fprintf(&buf, "\t}\n")
        }

    case FieldSlicePrimitive:
        fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[[]%s](%s, %s, %q); err == nil {\n",
            fi.ElemType, docVar, containerExpr, fi.TomlKey)
        fmt.Fprintf(&buf, "\t\t%s = v\n", target)
        fmt.Fprintf(&buf, "\t}\n")

    case FieldSliceStruct:
        handleName := toLowerFirst(fi.TypeName) + "Handle"
        nodesVar := fi.TomlKey + "Nodes"
        fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%q)\n", nodesVar, docVar, fi.TomlKey)
        fmt.Fprintf(&buf, "\td.%s = make([]%s, len(%s))\n", toLowerFirst(fi.GoName), handleName, nodesVar)
        fmt.Fprintf(&buf, "\t%s = make([]%s, len(%s))\n", target, fi.TypeName, nodesVar)
        fmt.Fprintf(&buf, "\tfor i, node := range %s {\n", nodesVar)
        fmt.Fprintf(&buf, "\t\td.%s[i] = %s{node: node}\n", toLowerFirst(fi.GoName), handleName)
        if fi.InnerInfo != nil {
            for _, inner := range fi.InnerInfo.Fields {
                indexedTarget := fmt.Sprintf("%s[i]", target)
                code := emitDecodeField(inner, indexedTarget, docVar, "node")
                buf.WriteString("\t" + code)
            }
        }
        fmt.Fprintf(&buf, "\t}\n")
    }

    return buf.String()
}

func emitEncodeField(fi FieldInfo, dataPath, docVar, containerExpr string) string {
    var buf bytes.Buffer
    source := dataPath + "." + fi.GoName

    switch fi.Kind {
    case FieldPrimitive:
        fmt.Fprintf(&buf, "\tif err := %s.SetInContainer(%s, %q, %s); err != nil {\n",
            docVar, containerExpr, fi.TomlKey, source)
        fmt.Fprintf(&buf, "\t\treturn nil, err\n")
        fmt.Fprintf(&buf, "\t}\n")

    case FieldPointerPrimitive:
        fmt.Fprintf(&buf, "\tif %s != nil {\n", source)
        fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, *%s); err != nil {\n",
            docVar, containerExpr, fi.TomlKey, source)
        fmt.Fprintf(&buf, "\t\t\treturn nil, err\n")
        fmt.Fprintf(&buf, "\t\t}\n")
        fmt.Fprintf(&buf, "\t}\n")

    case FieldCustom:
        fmt.Fprintf(&buf, "\t{\n")
        fmt.Fprintf(&buf, "\t\tv, err := %s.MarshalTOML()\n", source)
        fmt.Fprintf(&buf, "\t\tif err != nil {\n")
        fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
        fmt.Fprintf(&buf, "\t\t}\n")
        fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, v); err != nil {\n",
            docVar, containerExpr, fi.TomlKey)
        fmt.Fprintf(&buf, "\t\t\treturn nil, err\n")
        fmt.Fprintf(&buf, "\t\t}\n")
        fmt.Fprintf(&buf, "\t}\n")

    case FieldSliceStruct:
        fmt.Fprintf(&buf, "\tfor i, h := range d.%s {\n", toLowerFirst(fi.GoName))
        if fi.InnerInfo != nil {
            for _, inner := range fi.InnerInfo.Fields {
                indexedSource := fmt.Sprintf("%s[i]", source)
                code := emitEncodeField(inner, indexedSource, docVar, "h.node")
                buf.WriteString("\t" + code)
            }
        }
        fmt.Fprintf(&buf, "\t}\n")

    case FieldSlicePrimitive:
        fmt.Fprintf(&buf, "\tif err := %s.SetInContainer(%s, %q, %s); err != nil {\n",
            docVar, containerExpr, fi.TomlKey, source)
        fmt.Fprintf(&buf, "\t\treturn nil, err\n")
        fmt.Fprintf(&buf, "\t}\n")
    }

    return buf.String()
}

func toLowerFirst(s string) string {
    if s == "" {
        return s
    }
    runes := []rune(s)
    runes[0] = unicode.ToLower(runes[0])
    return string(runes)
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestEmitDecode ./generate/`

Expected: PASS

**Step 5: Commit**

``` text
feat(generate): add per-field decode/encode code emission
```

--------------------------------------------------------------------------------

### Task 7: Create generate package --- orchestrator

**Promotion criteria:** N/A

**Files:**

- Create: `generate/generate.go`
- Create: `generate/generate_test.go`

**Step 1: Write the failing test**

End-to-end test: write a Go fixture package, run the generator, compile the
output.

``` go
// generate/generate_test.go
package generate

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestGenerateProducesValidFile(t *testing.T) {
    dir := t.TempDir()
    writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
    writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
    Name    string `+"`"+`toml:"name"`+"`"+`
    Port    int    `+"`"+`toml:"port"`+"`"+`
    Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}
`)

    err := Generate(dir, "config.go")
    if err != nil {
        t.Fatal(err)
    }

    outPath := filepath.Join(dir, "config_tommy.go")
    data, err := os.ReadFile(outPath)
    if err != nil {
        t.Fatalf("generated file not found: %v", err)
    }

    out := string(data)
    if !strings.Contains(out, "Code generated by tommy; DO NOT EDIT.") {
        t.Fatal("missing generated header")
    }
    if !strings.Contains(out, "ConfigDocument") {
        t.Fatal("missing ConfigDocument")
    }
    if !strings.Contains(out, "DecodeConfig") {
        t.Fatal("missing DecodeConfig")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestGenerateProducesValidFile ./generate/`

Expected: FAIL --- `Generate` not defined.

**Step 3: Write minimal implementation**

``` go
// generate/generate.go
package generate

import (
    "bytes"
    "fmt"
    "go/format"
    "os"
    "path/filepath"
    "strings"
)

func Generate(dir, filename string) error {
    infos, err := Analyze(dir, filename)
    if err != nil {
        return fmt.Errorf("analyze: %w", err)
    }

    if len(infos) == 0 {
        return fmt.Errorf("no structs with //go:generate tommy generate found in %s", filename)
    }

    pkgName, err := detectPackageName(dir, filename)
    if err != nil {
        return err
    }

    var buf bytes.Buffer
    if err := RenderFile(&buf, pkgName, infos); err != nil {
        return fmt.Errorf("render: %w", err)
    }

    formatted, err := format.Source(buf.Bytes())
    if err != nil {
        return fmt.Errorf("gofmt: %w\nraw output:\n%s", err, buf.String())
    }

    outName := strings.TrimSuffix(filename, ".go") + "_tommy.go"
    outPath := filepath.Join(dir, outName)
    return os.WriteFile(outPath, formatted, 0o644)
}

func detectPackageName(dir, filename string) (string, error) {
    data, err := os.ReadFile(filepath.Join(dir, filename))
    if err != nil {
        return "", err
    }
    for _, line := range strings.Split(string(data), "\n") {
        line = strings.TrimSpace(line)
        if strings.HasPrefix(line, "package ") {
            return strings.Fields(line)[1], nil
        }
    }
    return "", fmt.Errorf("no package declaration in %s", filename)
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestGenerateProducesValidFile ./generate/`

Expected: PASS

**Step 5: Commit**

``` text
feat(generate): add orchestrator tying analyze, template, and emit
```

--------------------------------------------------------------------------------

### Task 8: Add `generate` subcommand to CLI

**Promotion criteria:** N/A

**Files:**

- Modify: `cmd/tommy/main.go`
- Create: `cmd/tommy/generate.go`

**Step 1: Write the failing test**

No unit test needed --- the CLI is a thin wrapper. Verify by building.

Run: `go build -o build/tommy ./cmd/tommy`

Expected: build succeeds (after implementation).

**Step 2: Write minimal implementation**

``` go
// cmd/tommy/generate.go
package main

import (
    "fmt"
    "os"

    "github.com/amarbel-llc/tommy/generate"
)

func runGenerate(args []string) int {
    goFile := os.Getenv("GOFILE")
    if goFile == "" {
        fmt.Fprintf(os.Stderr, "tommy generate: $GOFILE not set (must be run via go generate)\n")
        return 1
    }

    dir, err := os.Getwd()
    if err != nil {
        fmt.Fprintf(os.Stderr, "tommy generate: %s\n", err)
        return 1
    }

    if err := generate.Generate(dir, goFile); err != nil {
        fmt.Fprintf(os.Stderr, "tommy generate: %s\n", err)
        return 1
    }

    return 0
}
```

Update `cmd/tommy/main.go` to add the case:

``` go
case "generate":
    os.Exit(runGenerate(os.Args[2:]))
```

**Step 3: Build and verify**

Run: `go build -o build/tommy ./cmd/tommy`

Expected: PASS

**Step 4: Commit**

``` text
feat(cmd): add generate subcommand for companion document codegen
```

--------------------------------------------------------------------------------

### Task 9: End-to-end integration test

**Promotion criteria:** N/A

**Files:**

- Create: `generate/integration_test.go`

**Step 1: Write the integration test**

Create a fixture package with the same structs as `pkg/marshal/marshal_test.go`,
run the generator, compile the output, and verify the generated code produces
correct round-trip behavior.

``` go
// generate/integration_test.go
package generate

import (
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestIntegrationRoundTrip(t *testing.T) {
    dir := t.TempDir()

    writeFixture(t, dir, "go.mod", `module example.com/integration

go 1.25.6

require github.com/amarbel-llc/tommy v0.0.0

replace github.com/amarbel-llc/tommy => `+repoRoot(t)+`
`)

    writeFixture(t, dir, "config.go", `package main

//go:generate tommy generate
type Config struct {
    Name    string `+"`"+`toml:"name"`+"`"+`
    Port    int    `+"`"+`toml:"port"`+"`"+`
    Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}
`)

    // Run generator
    if err := Generate(dir, "config.go"); err != nil {
        t.Fatal(err)
    }

    // Verify generated file exists
    genPath := filepath.Join(dir, "config_tommy.go")
    if _, err := os.Stat(genPath); err != nil {
        t.Fatalf("generated file not found: %v", err)
    }

    // Write a main.go that exercises the generated code
    writeFixture(t, dir, "main_test.go", `package main

import (
    "testing"
)

func TestGeneratedRoundTrip(t *testing.T) {
    input := []byte("# comment\nname = \"myapp\"  # inline\nport = 8080\nenabled = true\n")
    doc, err := DecodeConfig(input)
    if err != nil {
        t.Fatal(err)
    }

    cfg := doc.Data()
    if cfg.Name != "myapp" {
        t.Fatalf("expected Name myapp, got %s", cfg.Name)
    }
    if cfg.Port != 8080 {
        t.Fatalf("expected Port 8080, got %d", cfg.Port)
    }

    cfg.Port = 9090
    out, err := doc.Encode()
    if err != nil {
        t.Fatal(err)
    }

    expected := "# comment\nname = \"myapp\"  # inline\nport = 9090\nenabled = true\n"
    if string(out) != expected {
        t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
    }
}
`)

    // Run the generated tests
    cmd := exec.Command("go", "test", "-v", "-run", "TestGeneratedRoundTrip", ".")
    cmd.Dir = dir
    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("generated test failed:\n%s", output)
    }
}

func repoRoot(t *testing.T) string {
    t.Helper()
    dir, err := os.Getwd()
    if err != nil {
        t.Fatal(err)
    }
    // Walk up to find go.mod
    for {
        if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
            return dir
        }
        parent := filepath.Dir(dir)
        if parent == dir {
            t.Fatal("could not find repo root")
        }
        dir = parent
    }
}
```

**Step 2: Run the integration test**

Run: `go test -v -run TestIntegrationRoundTrip ./generate/ -timeout 60s`

Expected: PASS --- the generated code compiles and produces correct round-trip
output with comments preserved.

**Step 3: Commit**

``` text
test(generate): add end-to-end integration test for codegen round-trip
```

--------------------------------------------------------------------------------

### Task 10: Integration test for array-of-tables

**Promotion criteria:** N/A

**Files:**

- Modify: `generate/integration_test.go`

**Step 1: Write the test**

Add a second integration test that exercises array-of-tables (slice-of-structs)
codegen with the same structs used in `pkg/marshal/marshal_test.go`.

``` go
// append to generate/integration_test.go

func TestIntegrationArrayOfTables(t *testing.T) {
    dir := t.TempDir()

    writeFixture(t, dir, "go.mod", `module example.com/aot

go 1.25.6

require github.com/amarbel-llc/tommy v0.0.0

replace github.com/amarbel-llc/tommy => `+repoRoot(t)+`
`)

    writeFixture(t, dir, "config.go", `package main

//go:generate tommy generate
type Config struct {
    Title   string   `+"`"+`toml:"title"`+"`"+`
    Servers []Server `+"`"+`toml:"servers"`+"`"+`
}

type Server struct {
    Name    string `+"`"+`toml:"name"`+"`"+`
    Command string `+"`"+`toml:"command"`+"`"+`
}
`)

    if err := Generate(dir, "config.go"); err != nil {
        t.Fatal(err)
    }

    writeFixture(t, dir, "main_test.go", `package main

import "testing"

func TestAOTRoundTrip(t *testing.T) {
    input := []byte("# my servers\ntitle = \"config\"\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n")
    doc, err := DecodeConfig(input)
    if err != nil {
        t.Fatal(err)
    }

    cfg := doc.Data()
    if len(cfg.Servers) != 2 {
        t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
    }

    cfg.Servers[1].Command = "lux mcp"
    out, err := doc.Encode()
    if err != nil {
        t.Fatal(err)
    }

    expected := "# my servers\ntitle = \"config\"\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux mcp\"\n"
    if string(out) != expected {
        t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
    }
}
`)

    cmd := exec.Command("go", "test", "-v", "-run", "TestAOTRoundTrip", ".")
    cmd.Dir = dir
    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("generated test failed:\n%s", output)
    }
}
```

**Step 2: Run the test**

Run: `go test -v -run TestIntegrationArrayOfTables ./generate/ -timeout 60s`

Expected: PASS

**Step 3: Commit**

``` text
test(generate): add integration test for array-of-tables codegen
```

--------------------------------------------------------------------------------

### Task 11: Integration test for custom unmarshal and pointer-to-struct

**Promotion criteria:** N/A

**Files:**

- Modify: `generate/integration_test.go`

**Step 1: Write the test**

This exercises the moxy-shaped types: `Command` (custom `UnmarshalTOML`) and
`*AnnotationFilter` (pointer-to-struct with `*bool` fields).

``` go
// append to generate/integration_test.go

func TestIntegrationCustomAndPointerTypes(t *testing.T) {
    dir := t.TempDir()

    writeFixture(t, dir, "go.mod", `module example.com/custom

go 1.25.6

require github.com/amarbel-llc/tommy v0.0.0

replace github.com/amarbel-llc/tommy => `+repoRoot(t)+`
`)

    writeFixture(t, dir, "types.go", `package main

import (
    "fmt"
    "strings"
)

type Command struct {
    parts []string
}

func (c *Command) UnmarshalTOML(data any) error {
    switch v := data.(type) {
    case string:
        c.parts = strings.Fields(v)
        return nil
    case []any:
        c.parts = make([]string, len(v))
        for i, elem := range v {
            s, ok := elem.(string)
            if !ok {
                return fmt.Errorf("element %d not a string", i)
            }
            c.parts[i] = s
        }
        return nil
    default:
        return fmt.Errorf("unsupported type %T", data)
    }
}

func (c Command) MarshalTOML() (any, error) {
    return strings.Join(c.parts, " "), nil
}

func (c Command) String() string {
    return strings.Join(c.parts, " ")
}

type AnnotationFilter struct {
    ReadOnlyHint *bool `+"`"+`toml:"readOnlyHint"`+"`"+`
}
`)

    writeFixture(t, dir, "config.go", `package main

//go:generate tommy generate
type Config struct {
    Servers []ServerConfig `+"`"+`toml:"servers"`+"`"+`
}

type ServerConfig struct {
    Name        string            `+"`"+`toml:"name"`+"`"+`
    Command     Command           `+"`"+`toml:"command"`+"`"+`
    Annotations *AnnotationFilter `+"`"+`toml:"annotations"`+"`"+`
}
`)

    if err := Generate(dir, "config.go"); err != nil {
        t.Fatal(err)
    }

    writeFixture(t, dir, "main_test.go", `package main

import "testing"

func TestCustomTypes(t *testing.T) {
    input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")
    doc, err := DecodeConfig(input)
    if err != nil {
        t.Fatal(err)
    }

    cfg := doc.Data()
    if len(cfg.Servers) != 1 {
        t.Fatalf("expected 1 server, got %d", len(cfg.Servers))
    }
    if cfg.Servers[0].Command.String() != "grit mcp" {
        t.Fatalf("expected command 'grit mcp', got %q", cfg.Servers[0].Command.String())
    }
    if cfg.Servers[0].Annotations != nil {
        t.Fatal("expected nil annotations")
    }
}
`)

    cmd := exec.Command("go", "test", "-v", "-run", "TestCustomTypes", ".")
    cmd.Dir = dir
    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("test failed:\n%s", output)
    }
}
```

**Step 2: Run the test**

Run:
`go test -v -run TestIntegrationCustomAndPointerTypes ./generate/ -timeout 60s`

Expected: PASS

**Step 3: Commit**

``` text
test(generate): add integration test for custom unmarshal and pointer-to-struct
```

--------------------------------------------------------------------------------

### Task 12: Run full test suite and verify no regressions

**Promotion criteria:** N/A

**Step 1: Run all tests**

Run: `just test-go`

Expected: All existing tests pass, all new tests pass.

**Step 2: Build the CLI**

Run: `just build`

Expected: Builds cleanly.

**Step 3: Final commit (if any fixups needed)**

``` text
chore: fix any regressions from codegen B implementation
```

Plan complete and saved to `docs/plans/2026-03-22-codegen-b-plan.md`. Ready to
execute?
