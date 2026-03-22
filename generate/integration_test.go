package generate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrationRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	// Absolute path to the repo root for the replace directive.
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/roundtrip",
		"",
		"go 1.25.6",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package roundtrip

//go:generate tommy generate
type Config struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Port    int    `+"`"+`toml:"port"`+"`"+`
	Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}
`)

	// Run code generation.
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify generated file exists.
	genPath := filepath.Join(dir, "config_tommy.go")
	genData, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("generated file not found: %v", err)
	}
	t.Logf("Generated code:\n%s", genData)

	// Write a test that exercises the generated decode/encode round-trip.
	writeFixture(t, dir, "roundtrip_test.go", `package roundtrip

import (
	"strings"
	"testing"
)

const testInput = `+"`"+`# Application config
name = "myapp"
port = 8080
enabled = true
`+"`"+`

func TestDecodeEncode(t *testing.T) {
	doc, err := DecodeConfig([]byte(testInput))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	data := doc.Data()
	if data.Name != "myapp" {
		t.Fatalf("Name = %q, want %q", data.Name, "myapp")
	}
	if data.Port != 8080 {
		t.Fatalf("Port = %d, want %d", data.Port, 8080)
	}
	if !data.Enabled {
		t.Fatal("Enabled = false, want true")
	}

	// Modify a field and encode.
	data.Port = 9090

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)

	// Comment must survive the round-trip.
	if !strings.Contains(result, "# Application config") {
		t.Fatalf("comment lost in round-trip:\n%s", result)
	}

	// The modified value must appear.
	if !strings.Contains(result, "9090") {
		t.Fatalf("modified port not found:\n%s", result)
	}

	// The original value must not appear.
	if strings.Contains(result, "8080") {
		t.Fatalf("old port value still present:\n%s", result)
	}

	// Decode the re-encoded output to verify it round-trips cleanly.
	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("second DecodeConfig: %v", err)
	}
	d2 := doc2.Data()
	if d2.Port != 9090 {
		t.Fatalf("re-decoded Port = %d, want 9090", d2.Port)
	}
	if d2.Name != "myapp" {
		t.Fatalf("re-decoded Name = %q, want %q", d2.Name, "myapp")
	}
}
`)

	// Run go test in the temp dir.
	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", output)
	if err != nil {
		t.Fatalf("go test failed: %v", err)
	}
}

func TestIntegrationArrayOfTables(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/aot",
		"",
		"go 1.25.6",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package aot

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

	// Run code generation.
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify generated file exists.
	genPath := filepath.Join(dir, "config_tommy.go")
	genData, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("generated file not found: %v", err)
	}
	t.Logf("Generated code:\n%s", genData)

	writeFixture(t, dir, "aot_test.go", `package aot

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

	// Run go test in the temp dir.
	cmd := exec.Command("go", "test", "-v", "-run", "TestAOTRoundTrip", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", output)
	if err != nil {
		t.Fatalf("generated test failed:\n%s", output)
	}
}

func TestIntegrationCustomAndPointerTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/custom",
		"",
		"go 1.25.6",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

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

	// Run code generation.
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify generated file exists.
	genPath := filepath.Join(dir, "config_tommy.go")
	genData, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("generated file not found: %v", err)
	}
	t.Logf("Generated code:\n%s", genData)

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

	// Run go test in the temp dir.
	cmd := exec.Command("go", "test", "-v", "-run", "TestCustomTypes", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", output)
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationMoxyMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/moxy",
		"",
		"go 1.25.6",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package main

import (
	"fmt"
	"strings"
)

//go:generate tommy generate
type Config struct {
	Servers []ServerConfig `+"`"+`toml:"servers"`+"`"+`
}

type ServerConfig struct {
	Name                  string            `+"`"+`toml:"name"`+"`"+`
	Command               Command           `+"`"+`toml:"command"`+"`"+`
	Annotations           *AnnotationFilter `+"`"+`toml:"annotations"`+"`"+`
	Paginate              bool              `+"`"+`toml:"paginate"`+"`"+`
	GenerateResourceTools *bool             `+"`"+`toml:"generate-resource-tools"`+"`"+`
}

type Command struct {
	parts []string
}

func (c *Command) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		c.parts = strings.Fields(v)
		if len(c.parts) == 0 {
			return fmt.Errorf("command string is empty")
		}
		return nil
	case []any:
		c.parts = make([]string, len(v))
		for i, elem := range v {
			s, ok := elem.(string)
			if !ok {
				return fmt.Errorf("command array element %d is not a string", i)
			}
			c.parts[i] = s
		}
		if len(c.parts) == 0 {
			return fmt.Errorf("command array is empty")
		}
		return nil
	default:
		return fmt.Errorf("command must be a string or array of strings")
	}
}

func (c Command) MarshalTOML() (any, error) {
	return strings.Join(c.parts, " "), nil
}

func (c Command) String() string {
	return strings.Join(c.parts, " ")
}

func MakeCommand(parts ...string) Command {
	return Command{parts: parts}
}

type AnnotationFilter struct {
	ReadOnlyHint    *bool `+"`"+`toml:"readOnlyHint"`+"`"+`
	DestructiveHint *bool `+"`"+`toml:"destructiveHint"`+"`"+`
	IdempotentHint  *bool `+"`"+`toml:"idempotentHint"`+"`"+`
	OpenWorldHint   *bool `+"`"+`toml:"openWorldHint"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	genData, err := os.ReadFile(filepath.Join(dir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("generated file not found: %v", err)
	}
	t.Logf("Generated code:\n%s", genData)

	writeFixture(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	writeFixture(t, dir, "moxy_test.go", `package main

import "testing"

func TestDecodeBasicMoxyfile(t *testing.T) {
	input := []byte("#  MCP server configuration\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve --verbose\"\npaginate = true\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Name != "grit" {
		t.Fatalf("expected Name 'grit', got %q", cfg.Servers[0].Name)
	}
	if cfg.Servers[0].Command.String() != "grit mcp" {
		t.Fatalf("expected Command 'grit mcp', got %q", cfg.Servers[0].Command.String())
	}
	if cfg.Servers[0].Annotations != nil {
		t.Fatal("expected nil Annotations for grit")
	}
	if cfg.Servers[0].Paginate != false {
		t.Fatal("expected Paginate false for grit")
	}
	if cfg.Servers[1].Name != "lux" {
		t.Fatalf("expected Name 'lux', got %q", cfg.Servers[1].Name)
	}
	if cfg.Servers[1].Command.String() != "lux serve --verbose" {
		t.Fatalf("expected Command 'lux serve --verbose', got %q", cfg.Servers[1].Command.String())
	}
	if cfg.Servers[1].Paginate != true {
		t.Fatal("expected Paginate true for lux")
	}
}

func TestDecodeWithAnnotationSubTable(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[servers.annotations]\nreadOnlyHint = true\ndestructiveHint = false\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].Annotations == nil {
		t.Fatal("expected non-nil Annotations")
	}
	if cfg.Servers[0].Annotations.ReadOnlyHint == nil || *cfg.Servers[0].Annotations.ReadOnlyHint != true {
		t.Fatal("expected ReadOnlyHint true")
	}
	if cfg.Servers[0].Annotations.DestructiveHint == nil || *cfg.Servers[0].Annotations.DestructiveHint != false {
		t.Fatal("expected DestructiveHint false")
	}
	if cfg.Servers[0].Annotations.IdempotentHint != nil {
		t.Fatal("expected IdempotentHint nil (not present)")
	}
}

func TestRoundTripPreservesComments(t *testing.T) {
	input := []byte("# MCP server configuration\n\n[[servers]]\nname = \"grit\"  # the git server\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	cfg.Servers[1].Command = MakeCommand("lux", "mcp")

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "# MCP server configuration\n\n[[servers]]\nname = \"grit\"  # the git server\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux mcp\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestRoundTripGenerateResourceTools(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\ngenerate-resource-tools = true\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].GenerateResourceTools == nil {
		t.Fatal("expected non-nil GenerateResourceTools")
	}
	if *cfg.Servers[0].GenerateResourceTools != true {
		t.Fatal("expected GenerateResourceTools true")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Fatalf("expected byte-identical round-trip.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestWriteServerEquivalent(t *testing.T) {
	input := []byte("# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	cfg.Servers = append(cfg.Servers, ServerConfig{
		Name:    "lux",
		Command: MakeCommand("lux", "serve"),
	})

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestDecodeNoAnnotationSubTable(t *testing.T) {
	// Flat annotation keys in the server table should be picked up as a fallback.
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\nreadOnlyHint = true\ndestructiveHint = false\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].Annotations == nil {
		t.Fatal("expected non-nil Annotations from flat keys")
	}
	if cfg.Servers[0].Annotations.ReadOnlyHint == nil || *cfg.Servers[0].Annotations.ReadOnlyHint != true {
		t.Fatal("expected ReadOnlyHint true")
	}
	if cfg.Servers[0].Annotations.DestructiveHint == nil || *cfg.Servers[0].Annotations.DestructiveHint != false {
		t.Fatal("expected DestructiveHint false")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", output)
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationFlatKeyFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/flatkey",
		"",
		"go 1.25.6",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package flatkey

import (
	"fmt"
	"strings"
)

//go:generate tommy generate
type Config struct {
	Servers []ServerConfig `+"`"+`toml:"servers"`+"`"+`
}

type ServerConfig struct {
	Name        string            `+"`"+`toml:"name"`+"`"+`
	Command     Command           `+"`"+`toml:"command"`+"`"+`
	Annotations *AnnotationFilter `+"`"+`toml:"annotations"`+"`"+`
}

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
	ReadOnlyHint    *bool `+"`"+`toml:"readOnlyHint"`+"`"+`
	DestructiveHint *bool `+"`"+`toml:"destructiveHint"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	genData, err := os.ReadFile(filepath.Join(dir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("generated file not found: %v", err)
	}
	t.Logf("Generated code:\n%s", genData)

	writeFixture(t, dir, "flatkey_test.go", `package flatkey

import "testing"

func TestFlatKeysDecoded(t *testing.T) {
	// Flat annotation keys directly in the server table (no [servers.annotations] sub-table).
	// The codegen should fall back to reading these from the parent container.
	input := []byte("[[servers]]\nname = \"lux\"\ncommand = \"lux\"\nreadOnlyHint = true\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].Annotations == nil {
		t.Fatal("expected non-nil Annotations from flat keys, got nil")
	}
	if cfg.Servers[0].Annotations.ReadOnlyHint == nil || *cfg.Servers[0].Annotations.ReadOnlyHint != true {
		t.Fatal("expected ReadOnlyHint true")
	}
	if cfg.Servers[0].Annotations.DestructiveHint != nil {
		t.Fatal("expected DestructiveHint nil (not present)")
	}
}

func TestSubTableTakesPrecedence(t *testing.T) {
	// When both flat keys and a sub-table exist, the sub-table should win.
	input := []byte("[[servers]]\nname = \"lux\"\ncommand = \"lux\"\nreadOnlyHint = false\n\n[servers.annotations]\nreadOnlyHint = true\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].Annotations == nil {
		t.Fatal("expected non-nil Annotations")
	}
	if cfg.Servers[0].Annotations.ReadOnlyHint == nil || *cfg.Servers[0].Annotations.ReadOnlyHint != true {
		t.Fatal("expected ReadOnlyHint true from sub-table, not false from flat key")
	}
}

func TestNoFlatKeysNoSubTable(t *testing.T) {
	// No annotation keys at all — Annotations should remain nil.
	input := []byte("[[servers]]\nname = \"lux\"\ncommand = \"lux\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].Annotations != nil {
		t.Fatal("expected nil Annotations when no keys present")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", output)
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationZeroValuePrimitiveSkip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/zeroval",
		"",
		"go 1.25.6",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package zeroval

//go:generate tommy generate
type Config struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Port    int    `+"`"+`toml:"port"`+"`"+`
	Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	genData, err := os.ReadFile(filepath.Join(dir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("generated file not found: %v", err)
	}
	t.Logf("Generated code:\n%s", genData)

	writeFixture(t, dir, "zeroval_test.go", `package zeroval

import "testing"

func TestZeroValueNotAppended(t *testing.T) {
	// Only name and port are in the TOML — enabled (bool, zero = false)
	// should NOT be appended on encode.
	input := []byte("name = \"app\"\nport = 8080\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Enabled != false {
		t.Fatal("expected Enabled false")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestZeroValuePreservedWhenExplicit(t *testing.T) {
	// enabled = false is explicit in the TOML — it should be preserved.
	input := []byte("name = \"app\"\nport = 8080\nenabled = false\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", output)
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationArrayOfTablesAppend(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/aotappend",
		"",
		"go 1.25.6",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package aotappend

//go:generate tommy generate
type Config struct {
	Servers []Server `+"`"+`toml:"servers"`+"`"+`
}

type Server struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Command string `+"`"+`toml:"command"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	genData, err := os.ReadFile(filepath.Join(dir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("generated file not found: %v", err)
	}
	t.Logf("Generated code:\n%s", genData)

	writeFixture(t, dir, "append_test.go", `package aotappend

import "testing"

func TestAppendNewEntry(t *testing.T) {
	// Start with one server, append a second, encode.
	input := []byte("# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	cfg.Servers = append(cfg.Servers, Server{
		Name:    "lux",
		Command: "lux serve",
	})

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestAppendPreservesExisting(t *testing.T) {
	// Modify existing entry + append new one.
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	cfg.Servers[0].Command = "grit serve"
	cfg.Servers = append(cfg.Servers, Server{
		Name:    "lux",
		Command: "lux mcp",
	})

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "[[servers]]\nname = \"grit\"\ncommand = \"grit serve\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux mcp\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", output)
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}
