package generate

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeWithoutGoModPointsAtNix(t *testing.T) {
	dir := t.TempDir()
	if findGoMod(dir) != "" {
		t.Skip("the temp dir sits under a go.mod; the nix go-generate check runs this")
	}
	t.Setenv("GO111MODULE", "")
	writeFixture(t, dir, "config.go", "package test\n\n//go:generate tommy generate\ntype Config struct {\n\tName string `toml:\"name\"`\n}\n")

	_, err := Analyze(dir, "config.go")
	if err == nil {
		t.Fatal("expected an error without a go.mod")
	}
	for _, want := range []string{"no go.mod", "codegenCheck", "godyn-go"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDetectGoLangVersionReadsNearestGoMod(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/x\n\ngo 1.26\n")
	sub := filepath.Join(root, "a", "b")
	writeFixture(t, sub, "b.go", "package b\n")

	if got := findGoMod(sub); got != filepath.Join(root, "go.mod") {
		t.Fatalf("findGoMod = %q", got)
	}
	if got := detectGoLangVersion(sub); got != "go1.26" {
		t.Fatalf("detectGoLangVersion = %q, want go1.26", got)
	}
}
