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
		"go 1.26",
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
	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


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
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
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
		"go 1.26",
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
	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


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
		"go 1.26",
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
	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
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

	// Run go test in the temp dir.
	cmd := exec.Command("go", "test", "-v", "-run", "TestCustomTypes", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()

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
		"go 1.26",
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

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


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
		"go 1.26",
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

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


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

	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationMapStringString(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/sweatfile",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package sweatfile

//go:generate tommy generate
type Sweatfile struct {
	SystemPrompt *string           `+"`"+`toml:"system-prompt"`+"`"+`
	GitExcludes  []string          `+"`"+`toml:"git-excludes"`+"`"+`
	Env          map[string]string `+"`"+`toml:"env"`+"`"+`
	Hooks        *Hooks            `+"`"+`toml:"hooks"`+"`"+`
}

type Hooks struct {
	Create               *string `+"`"+`toml:"create"`+"`"+`
	Stop                 *string `+"`"+`toml:"stop"`+"`"+`
	DisallowMainWorktree *bool   `+"`"+`toml:"disallow-main-worktree"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "sweatfile_test.go", `package sweatfile

import "testing"

func TestDecodeSweatfile(t *testing.T) {
	input := []byte("system-prompt = \"be helpful\"\ngit-excludes = [\".claude/\", \".direnv/\"]\n\n[env]\nFOO = \"bar\"\nBAZ = \"qux\"\n\n[hooks]\ncreate = \"npm install\"\nstop = \"just test\"\ndisallow-main-worktree = true\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	sf := doc.Data()
	if sf.SystemPrompt == nil || *sf.SystemPrompt != "be helpful" {
		t.Fatal("expected system-prompt 'be helpful'")
	}
	if len(sf.GitExcludes) != 2 {
		t.Fatalf("expected 2 git-excludes, got %d", len(sf.GitExcludes))
	}
	if sf.Env == nil {
		t.Fatal("expected non-nil Env map")
	}
	if sf.Env["FOO"] != "bar" {
		t.Fatalf("expected Env[FOO] = 'bar', got %q", sf.Env["FOO"])
	}
	if sf.Env["BAZ"] != "qux" {
		t.Fatalf("expected Env[BAZ] = 'qux', got %q", sf.Env["BAZ"])
	}
	if sf.Hooks == nil {
		t.Fatal("expected non-nil Hooks")
	}
	if sf.Hooks.Create == nil || *sf.Hooks.Create != "npm install" {
		t.Fatal("expected hooks.create 'npm install'")
	}
	if sf.Hooks.DisallowMainWorktree == nil || *sf.Hooks.DisallowMainWorktree != true {
		t.Fatal("expected hooks.disallow-main-worktree true")
	}
}

func TestRoundTripSweatfile(t *testing.T) {
	input := []byte("system-prompt = \"be helpful\"\ngit-excludes = [\".claude/\"]\n\n[env]\nFOO = \"bar\"\nBAZ = \"qux\"\n\n[hooks]\ncreate = \"npm install\"\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	sf := doc.Data()
	sf.Env["NEW_KEY"] = "new_val"

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	// Re-decode to verify the new key survived.
	doc2, err := DecodeSweatfile(out)
	if err != nil {
		t.Fatal(err)
	}
	sf2 := doc2.Data()
	if sf2.Env["FOO"] != "bar" {
		t.Fatalf("expected FOO preserved, got %q", sf2.Env["FOO"])
	}
	if sf2.Env["NEW_KEY"] != "new_val" {
		t.Fatalf("expected NEW_KEY = 'new_val', got %q", sf2.Env["NEW_KEY"])
	}
}

func TestEmptyMapNotAppended(t *testing.T) {
	// No [env] section in input — should not appear in output.
	input := []byte("system-prompt = \"hi\"\ngit-excludes = [\".claude/\"]\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	sf := doc.Data()
	if sf.Env != nil {
		t.Fatalf("expected nil Env, got %v", sf.Env)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestUndecodedEmpty(t *testing.T) {
	// All keys are known — Undecoded should return nothing.
	input := []byte("system-prompt = \"hi\"\ngit-excludes = [\".claude/\"]\n\n[hooks]\ncreate = \"npm install\"\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	undecoded := doc.Undecoded()
	if len(undecoded) != 0 {
		t.Fatalf("expected no undecoded keys, got %v", undecoded)
	}
}

func TestUndecodedTypo(t *testing.T) {
	// "sytem-prompt" is a typo — should appear in Undecoded.
	input := []byte("sytem-prompt = \"hi\"\ngit-excludes = [\".claude/\"]\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	undecoded := doc.Undecoded()
	if len(undecoded) != 1 {
		t.Fatalf("expected 1 undecoded key, got %v", undecoded)
	}
	if undecoded[0] != "sytem-prompt" {
		t.Fatalf("expected undecoded key 'sytem-prompt', got %q", undecoded[0])
	}
}

func TestUndecodedNestedTypo(t *testing.T) {
	// "creat" is a typo inside [hooks] — should appear as "hooks.creat".
	input := []byte("system-prompt = \"hi\"\n\n[hooks]\ncreat = \"npm install\"\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	undecoded := doc.Undecoded()
	if len(undecoded) != 1 {
		t.Fatalf("expected 1 undecoded key, got %v", undecoded)
	}
	if undecoded[0] != "hooks.creat" {
		t.Fatalf("expected undecoded key 'hooks.creat', got %q", undecoded[0])
	}
}

func TestUndecodedMapKeysAllConsumed(t *testing.T) {
	// [env] is a map[string]string — all keys under it should be consumed.
	input := []byte("system-prompt = \"hi\"\n\n[env]\nFOO = \"bar\"\nANYTHING = \"goes\"\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	undecoded := doc.Undecoded()
	if len(undecoded) != 0 {
		t.Fatalf("expected no undecoded keys (map accepts all), got %v", undecoded)
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

func TestIntegrationPointerStructEncode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/ptrstruct",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package ptrstruct

//go:generate tommy generate
type Sweatfile struct {
	SystemPrompt *string `+"`"+`toml:"system-prompt"`+"`"+`
	Hooks        *Hooks  `+"`"+`toml:"hooks"`+"`"+`
}

type Hooks struct {
	Create               *string `+"`"+`toml:"create"`+"`"+`
	Stop                 *string `+"`"+`toml:"stop"`+"`"+`
	DisallowMainWorktree *bool   `+"`"+`toml:"disallow-main-worktree"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "ptrstruct_test.go", `package ptrstruct

import (
	"strings"
	"testing"
)

func TestModifyPointerStructField(t *testing.T) {
	input := []byte("system-prompt = \"be helpful\"\n\n[hooks]\ncreate = \"npm install\"\nstop = \"just test\"\ndisallow-main-worktree = true\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	sf := doc.Data()
	newCreate := "composer install"
	sf.Hooks.Create = &newCreate

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	result := string(out)
	if !strings.Contains(result, "composer install") {
		t.Fatalf("expected modified create hook in output, got:\n%s", result)
	}
	if strings.Contains(result, "npm install") {
		t.Fatalf("expected old create hook replaced, got:\n%s", result)
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

func TestIntegrationTomlDash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/tomldash",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package tomldash

//go:generate tommy generate
type Config struct {
	Name     string `+"`"+`toml:"name"`+"`"+`
	Internal string `+"`"+`toml:"-"`+"`"+`
	Port     int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "dash_test.go", `package tomldash

import (
	"strings"
	"testing"
)

func TestDashFieldExcluded(t *testing.T) {
	input := []byte("name = \"app\"\nport = 8080\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Internal = "should not appear"

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(out), "Internal") || strings.Contains(string(out), "should not appear") {
		t.Fatalf("toml:\"-\" field leaked into output:\n%s", string(out))
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

	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationOmitempty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/omitempty",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package omitempty

//go:generate tommy generate
type Config struct {
	Name  string   `+"`"+`toml:"name"`+"`"+`
	Tags  []string `+"`"+`toml:"tags,omitempty"`+"`"+`
	Hooks *Hooks   `+"`"+`toml:"hooks,omitempty"`+"`"+`
}

type Hooks struct {
	Create *string `+"`"+`toml:"create"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "omitempty_test.go", `package omitempty

import (
	"strings"
	"testing"
)

func TestNilSliceOmitemptyNotWritten(t *testing.T) {
	// tags is nil and omitempty — should not appear in output.
	input := []byte("name = \"app\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Tags != nil {
		t.Fatal("expected nil Tags")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestNonEmptySliceOmitemptyWritten(t *testing.T) {
	// tags is set — should appear in output.
	input := []byte("name = \"app\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Tags = []string{"v1", "v2"}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "tags") {
		t.Fatalf("expected tags in output, got:\n%s", string(out))
	}
}

func TestNilPointerStructOmitemptyNotWritten(t *testing.T) {
	// hooks is nil and omitempty — should not appear in output.
	input := []byte("name = \"app\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Hooks != nil {
		t.Fatal("expected nil Hooks")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(out), "hooks") || strings.Contains(string(out), "create") {
		t.Fatalf("nil omitempty pointer struct leaked into output:\n%s", string(out))
	}
}

func TestExplicitSliceOmitemptyPreserved(t *testing.T) {
	// tags exists in TOML — should survive round-trip.
	input := []byte("name = \"app\"\ntags = [\"v1\"]\n")

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
		"go 1.26",
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

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


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
		"go 1.26",
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

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


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

	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestOmitemptyPrimitiveZeroDropped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/omitprim",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package omitprim

//go:generate tommy generate
type Config struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Verbose bool   `+"`"+`toml:"verbose,omitempty"`+"`"+`
	Retries int    `+"`"+`toml:"retries,omitempty"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "omitprim_test.go", `package omitprim

import (
	"strings"
	"testing"
)

func TestOmitemptyPrimitiveZeroNotWritten(t *testing.T) {
	// verbose and retries are in TOML but set to zero values.
	// With omitempty, setting them to zero should drop them on encode.
	input := []byte("name = \"app\"\nverbose = true\nretries = 3\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	// Set to zero values — omitempty should cause them to be dropped.
	doc.Data().Verbose = false
	doc.Data().Retries = 0

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(out), "verbose") {
		t.Fatalf("omitempty zero bool should be dropped, got:\n%s", string(out))
	}
	if strings.Contains(string(out), "retries") {
		t.Fatalf("omitempty zero int should be dropped, got:\n%s", string(out))
	}
}

func TestOmitemptyPrimitiveNonZeroPreserved(t *testing.T) {
	// Non-zero values with omitempty should be written normally.
	input := []byte("name = \"app\"\nverbose = true\nretries = 3\n")

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

	cmd2 := exec.Command("go", "test", "-v", "./...")
	cmd2.Dir = dir
	cmd2.Env = append(os.Environ(), "GOFLAGS=")
	output2, err := cmd2.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", output2)
	}
}

func TestIntegrationMultiline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/multiline",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package multiline

//go:generate tommy generate
type Config struct {
	Name   string `+"`"+`toml:"name"`+"`"+`
	Script string `+"`"+`toml:"script,multiline"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "multiline_test.go", `package multiline

import (
	"strings"
	"testing"
)

func TestMultilineRoundTrip(t *testing.T) {
	input := []byte("name = \"app\"\nscript = \"\"\"\necho hello\necho world\"\"\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Script != "echo hello\necho world" {
		t.Fatalf("unexpected script value: %q", doc.Data().Script)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestMultilineEncodeNewValue(t *testing.T) {
	// Start with a basic string, set a multiline value — should encode as """.
	input := []byte("name = \"app\"\nscript = \"old\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Script = "line1\nline2\nline3"

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	s := string(out)
	if !strings.Contains(s, "\"\"\"") {
		t.Fatalf("expected multiline basic string delimiters, got:\n%s", s)
	}
	if !strings.Contains(s, "line1\nline2\nline3") {
		t.Fatalf("expected literal newlines in multiline string, got:\n%s", s)
	}
}
`)

	cmdML := exec.Command("go", "test", "-v", "./...")
	cmdML.Dir = dir
	cmdML.Env = append(os.Environ(), "GOFLAGS=")
	outputML, err := cmdML.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", outputML)
	}
}

func TestIntegrationTextMarshaler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/textmarshal",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "types.go", `package textmarshal

import "fmt"

// URI is a custom scalar type implementing encoding.TextMarshaler/TextUnmarshaler.
type URI struct {
	value string
}

func NewURI(s string) URI { return URI{value: s} }
func (u URI) String() string { return u.value }

func (u URI) MarshalText() ([]byte, error) {
	return []byte(u.value), nil
}

func (u *URI) UnmarshalText(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty URI")
	}
	u.value = string(data)
	return nil
}
`)

	writeFixture(t, dir, "config.go", `package textmarshal

//go:generate tommy generate
type Config struct {
	Name     string `+"`"+`toml:"name"`+"`"+`
	Homepage URI    `+"`"+`toml:"homepage"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "textmarshal_test.go", `package textmarshal

import "testing"

func TestTextMarshalerRoundTrip(t *testing.T) {
	input := []byte("name = \"myapp\"\nhomepage = \"https://example.com\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Homepage.String() != "https://example.com" {
		t.Fatalf("expected homepage https://example.com, got %q", doc.Data().Homepage.String())
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestTextMarshalerModify(t *testing.T) {
	input := []byte("name = \"myapp\"\nhomepage = \"https://example.com\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Homepage = NewURI("https://new.example.com")

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "name = \"myapp\"\nhomepage = \"https://new.example.com\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmdTM := exec.Command("go", "test", "-v", "./...")
	cmdTM.Dir = dir
	cmdTM.Env = append(os.Environ(), "GOFLAGS=")
	outputTM, err := cmdTM.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", outputTM)
	}
}

func TestIntegrationEmbeddedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/embedded",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package embedded

type Common struct {
	Version string `+"`"+`toml:"version"`+"`"+`
	Debug   bool   `+"`"+`toml:"debug"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Common
	Name string `+"`"+`toml:"name"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "embedded_test.go", `package embedded

import "testing"

func TestEmbeddedStructRoundTrip(t *testing.T) {
	input := []byte("version = \"1.0\"\ndebug = true\nname = \"app\"\nport = 8080\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Version != "1.0" {
		t.Fatalf("expected version 1.0, got %q", cfg.Version)
	}
	if cfg.Debug != true {
		t.Fatal("expected debug true")
	}
	if cfg.Name != "app" {
		t.Fatalf("expected name app, got %q", cfg.Name)
	}
	if cfg.Port != 8080 {
		t.Fatalf("expected port 8080, got %d", cfg.Port)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestEmbeddedStructModify(t *testing.T) {
	input := []byte("version = \"1.0\"\ndebug = true\nname = \"app\"\nport = 8080\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Version = "2.0"
	doc.Data().Debug = false
	doc.Data().Port = 9090

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "version = \"2.0\"\ndebug = false\nname = \"app\"\nport = 9090\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmdEmb := exec.Command("go", "test", "-v", "./...")
	cmdEmb.Dir = dir
	cmdEmb.Env = append(os.Environ(), "GOFLAGS=")
	outputEmb, err := cmdEmb.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", outputEmb)
	}
}

func TestIntegrationMapStringStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/mapstruct",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package mapstruct

//go:generate tommy generate
type Config struct {
	Name    string                `+"`"+`toml:"name"`+"`"+`
	Actions map[string]ActionSpec `+"`"+`toml:"actions"`+"`"+`
}

type ActionSpec struct {
	Command string `+"`"+`toml:"command"`+"`"+`
	Timeout int    `+"`"+`toml:"timeout"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "mapstruct_test.go", `package mapstruct

import "testing"

func TestMapStringStructRoundTrip(t *testing.T) {
	input := []byte("name = \"app\"\n\n[actions.build]\ncommand = \"make\"\ntimeout = 30\n\n[actions.test]\ncommand = \"go test\"\ntimeout = 60\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "app" {
		t.Fatalf("expected name app, got %q", cfg.Name)
	}
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	build := cfg.Actions["build"]
	if build.Command != "make" || build.Timeout != 30 {
		t.Fatalf("unexpected build action: %+v", build)
	}
	test := cfg.Actions["test"]
	if test.Command != "go test" || test.Timeout != 60 {
		t.Fatalf("unexpected test action: %+v", test)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestMapStringStructModify(t *testing.T) {
	input := []byte("name = \"app\"\n\n[actions.build]\ncommand = \"make\"\ntimeout = 30\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	build := doc.Data().Actions["build"]
	build.Command = "cmake"
	build.Timeout = 45
	doc.Data().Actions["build"] = build

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "name = \"app\"\n\n[actions.build]\ncommand = \"cmake\"\ntimeout = 45\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmdMS := exec.Command("go", "test", "-v", "./...")
	cmdMS.Dir = dir
	cmdMS.Env = append(os.Environ(), "GOFLAGS=")
	outputMS, err := cmdMS.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", outputMS)
	}
}

func TestIntegrationSliceTextMarshaler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/slicetm",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "types.go", `package slicetm

import "fmt"

type KeyID struct {
	value string
}

func NewKeyID(s string) KeyID { return KeyID{value: s} }
func (k KeyID) String() string { return k.value }

func (k KeyID) MarshalText() ([]byte, error) {
	return []byte(k.value), nil
}

func (k *KeyID) UnmarshalText(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty KeyID")
	}
	k.value = string(data)
	return nil
}
`)

	writeFixture(t, dir, "config.go", `package slicetm

//go:generate tommy generate
type Config struct {
	Name       string  `+"`"+`toml:"name"`+"`"+`
	Encryption []KeyID `+"`"+`toml:"encryption"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "slicetm_test.go", `package slicetm

import "testing"

func TestSliceTextMarshalerRoundTrip(t *testing.T) {
	input := []byte("name = \"vault\"\nencryption = [\"key-abc\", \"key-def\"]\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if len(cfg.Encryption) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(cfg.Encryption))
	}
	if cfg.Encryption[0].String() != "key-abc" {
		t.Fatalf("expected key-abc, got %q", cfg.Encryption[0].String())
	}
	if cfg.Encryption[1].String() != "key-def" {
		t.Fatalf("expected key-def, got %q", cfg.Encryption[1].String())
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestSliceTextMarshalerModify(t *testing.T) {
	input := []byte("name = \"vault\"\nencryption = [\"key-abc\"]\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Encryption = append(doc.Data().Encryption, NewKeyID("key-xyz"))

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "name = \"vault\"\nencryption = [\"key-abc\", \"key-xyz\"]\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmdST := exec.Command("go", "test", "-v", "./...")
	cmdST.Dir = dir
	cmdST.Env = append(os.Environ(), "GOFLAGS=")
	outputST, err := cmdST.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", outputST)
	}
}

func TestIntegrationUint64(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/uint64test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package uint64test

//go:generate tommy generate
type SelectorConfig struct {
	Type        string `+"`"+`toml:"type"`+"`"+`
	MinBlobSize uint64 `+"`"+`toml:"min-blob-size"`+"`"+`
	MaxBlobSize uint64 `+"`"+`toml:"max-blob-size"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "uint64_test.go", `package uint64test

import "testing"

func TestUint64RoundTrip(t *testing.T) {
	input := []byte("type = \"size\"\nmin-blob-size = 1024\nmax-blob-size = 10485760\n")

	doc, err := DecodeSelectorConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Type != "size" {
		t.Fatalf("expected type size, got %q", cfg.Type)
	}
	if cfg.MinBlobSize != 1024 {
		t.Fatalf("expected min 1024, got %d", cfg.MinBlobSize)
	}
	if cfg.MaxBlobSize != 10485760 {
		t.Fatalf("expected max 10485760, got %d", cfg.MaxBlobSize)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestUint64Modify(t *testing.T) {
	input := []byte("type = \"size\"\nmin-blob-size = 1024\nmax-blob-size = 10485760\n")

	doc, err := DecodeSelectorConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().MinBlobSize = 2048
	doc.Data().MaxBlobSize = 20971520

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "type = \"size\"\nmin-blob-size = 2048\nmax-blob-size = 20971520\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmdU := exec.Command("go", "test", "-v", "./...")
	cmdU.Dir = dir
	cmdU.Env = append(os.Environ(), "GOFLAGS=")
	outputU, err := cmdU.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", outputU)
	}
}

func TestIntegrationNestedArrayOfTables(t *testing.T) {
	t.Skip("codegen for nested array-of-tables not yet implemented — see #6")
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/nestedaot",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package nestedaot

//go:generate tommy generate
type Config struct {
	Name    string   `+"`"+`toml:"name"`+"`"+`
	Servers []Server `+"`"+`toml:"servers"`+"`"+`
}

type Server struct {
	Host    string   `+"`"+`toml:"host"`+"`"+`
	Plugins []Plugin `+"`"+`toml:"plugins"`+"`"+`
}

type Plugin struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "nested_test.go", `package nestedaot

import "testing"

func TestNestedArrayOfTablesRoundTrip(t *testing.T) {
	input := []byte("name = \"app\"\n\n[[servers]]\nhost = \"alpha\"\n\n[[servers.plugins]]\nname = \"auth\"\nenabled = true\n\n[[servers.plugins]]\nname = \"cache\"\nenabled = false\n\n[[servers]]\nhost = \"beta\"\n\n[[servers.plugins]]\nname = \"log\"\nenabled = true\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "app" {
		t.Fatalf("expected name app, got %q", cfg.Name)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}

	// First server: alpha with 2 plugins
	if cfg.Servers[0].Host != "alpha" {
		t.Fatalf("expected host alpha, got %q", cfg.Servers[0].Host)
	}
	if len(cfg.Servers[0].Plugins) != 2 {
		t.Fatalf("expected 2 plugins for alpha, got %d", len(cfg.Servers[0].Plugins))
	}
	if cfg.Servers[0].Plugins[0].Name != "auth" {
		t.Fatalf("expected plugin auth, got %q", cfg.Servers[0].Plugins[0].Name)
	}
	if cfg.Servers[0].Plugins[1].Name != "cache" {
		t.Fatalf("expected plugin cache, got %q", cfg.Servers[0].Plugins[1].Name)
	}

	// Second server: beta with 1 plugin
	if cfg.Servers[1].Host != "beta" {
		t.Fatalf("expected host beta, got %q", cfg.Servers[1].Host)
	}
	if len(cfg.Servers[1].Plugins) != 1 {
		t.Fatalf("expected 1 plugin for beta, got %d", len(cfg.Servers[1].Plugins))
	}
	if cfg.Servers[1].Plugins[0].Name != "log" {
		t.Fatalf("expected plugin log, got %q", cfg.Servers[1].Plugins[0].Name)
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

	cmdN := exec.Command("go", "test", "-v", "./...")
	cmdN.Dir = dir
	cmdN.Env = append(os.Environ(), "GOFLAGS=")
	outputN, err := cmdN.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", outputN)
	}
}
