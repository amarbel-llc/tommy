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
	writeFixture(t, dir, "config.go", "package test\n\n//go:generate tommy generate\ntype Config struct {\n\tName    string  `toml:\"name\"`\n\tPort    int     `toml:\"port\"`\n\tEnabled bool    `toml:\"enabled\"`\n}\n")

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
	writeFixture(t, dir, "config.go", "package test\n\n//go:generate tommy generate\ntype Config struct {\n\tServers []Server `toml:\"servers\"`\n}\n\ntype Server struct {\n\tName    string `toml:\"name\"`\n\tCommand string `toml:\"command\"`\n}\n")

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
	writeFixture(t, dir, "config.go", "package test\n\n//go:generate tommy generate\ntype Config struct {\n\tData map[string]string `toml:\"data\"`\n}\n")

	_, err := Analyze(dir, "config.go")
	if err == nil {
		t.Fatal("expected error for unsupported map type")
	}
}
