package generate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #134: `tommy generate` must emit gofumpt-canonical Go. gofumpt requires a
// blank line between consecutive multiline top-level declarations; goimports /
// go/format.Source (gofmt) alone leave them packed, so freshly generated code
// fails any downstream treefmt/gofumpt gate. This pins the property on the real
// Render pipeline (analyze → render → goimports → gofumpt).
func TestRenderOutputSeparatesTopLevelDecls(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", "package test\n\n//go:generate tommy generate\ntype Config struct {\n\tName string `toml:\"name\"`\n}\n")

	out, err := Render(dir, "config.go")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "config_tommy.go", out, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse generated output: %v\n%s", err, out)
	}

	for i := 1; i < len(file.Decls); i++ {
		prev, cur := file.Decls[i-1], file.Decls[i]
		prevStart := fset.Position(declStart(prev)).Line
		prevEnd := fset.Position(prev.End()).Line
		curStart := fset.Position(declStart(cur)).Line
		curEnd := fset.Position(cur.End()).Line

		// gofumpt only mandates a blank line when a multiline declaration is
		// involved; single-line decls may legitimately sit adjacent.
		multiline := prevEnd > prevStart || curEnd > curStart
		if multiline && curStart-prevEnd < 2 {
			t.Errorf("no blank line between top-level decl ending at L%d and the one starting at L%d (gofumpt requires one):\n%s",
				prevEnd, curStart, out)
		}
	}
}

// declStart returns the line where a declaration begins for spacing purposes:
// its doc comment when present, otherwise the declaration keyword.
func declStart(d ast.Decl) token.Pos {
	switch n := d.(type) {
	case *ast.FuncDecl:
		if n.Doc != nil {
			return n.Doc.Pos()
		}
	case *ast.GenDecl:
		if n.Doc != nil {
			return n.Doc.Pos()
		}
	}
	return d.Pos()
}
