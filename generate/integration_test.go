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

func TestIntegrationNestedMapStringStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/nestedmap",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package nestedmap

//go:generate tommy generate
type Config struct {
	Outer OuterConfig `+"`"+`toml:"outer,omitempty"`+"`"+`
}

type OuterConfig struct {
	Name     string                 `+"`"+`toml:"name"`+"`"+`
	Mappings map[string]EntryConfig `+"`"+`toml:"mappings,omitempty"`+"`"+`
}

type EntryConfig struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "nestedmap_test.go", `package nestedmap

import (
	"strings"
	"testing"
)

func TestNestedMapDecode(t *testing.T) {
	input := []byte("[outer]\nname = \"test\"\n\n[outer.mappings.key1]\nvalue = \"hello\"\n\n[outer.mappings.key2]\nvalue = \"world\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Outer.Name != "test" {
		t.Fatalf("expected name test, got %q", cfg.Outer.Name)
	}
	if len(cfg.Outer.Mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(cfg.Outer.Mappings))
	}
	if cfg.Outer.Mappings["key1"].Value != "hello" {
		t.Fatalf("expected key1=hello, got %q", cfg.Outer.Mappings["key1"].Value)
	}
	if cfg.Outer.Mappings["key2"].Value != "world" {
		t.Fatalf("expected key2=world, got %q", cfg.Outer.Mappings["key2"].Value)
	}
}

func TestNestedMapEncode(t *testing.T) {
	doc, err := DecodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	cfg.Outer.Name = "test"
	cfg.Outer.Mappings = map[string]EntryConfig{
		"key1": {Value: "hello"},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	output := string(out)
	if !strings.Contains(output, "[outer.mappings.key1]") {
		t.Fatalf("expected [outer.mappings.key1] in output, got:\n%s", output)
	}
	if strings.Contains(output, "\n[mappings.key1]") {
		t.Fatalf("mappings should be nested under outer, got:\n%s", output)
	}
}

func TestNestedMapRoundTrip(t *testing.T) {
	input := []byte("[outer]\nname = \"test\"\n\n[outer.mappings.key1]\nvalue = \"hello\"\n")

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

func TestIntegrationCrossPackageEmbedded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Base package with a struct
	baseDir := filepath.Join(dir, "base")
	writeFixture(t, baseDir, "go.mod", "module example.com/test/base\n\ngo 1.26\n")
	writeFixture(t, baseDir, "base.go", `package base

type Config struct {
	Name   string `+"`"+`toml:"name"`+"`"+`
	Script string `+"`"+`toml:"script,omitempty"`+"`"+`
}
`)

	// Consumer package that embeds cross-package struct
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/base v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/base => ../base",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/base"

//go:generate tommy generate
type Extended struct {
	base.Config
	Extra string `+"`"+`toml:"extra"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestCrossPackageEmbeddedRoundTrip(t *testing.T) {
	input := []byte("name = \"hello\"\nscript = \"echo hi\"\nextra = \"world\"\n")

	doc, err := DecodeExtended(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "hello" {
		t.Fatalf("Name = %q, want \"hello\"", cfg.Name)
	}
	if cfg.Extra != "world" {
		t.Fatalf("Extra = %q, want \"world\"", cfg.Extra)
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
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationCrossPackagePrimitiveWrapper(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Package with a named type wrapping int
	typesDir := filepath.Join(dir, "types")
	writeFixture(t, typesDir, "go.mod", "module example.com/test/types\n\ngo 1.26\n")
	writeFixture(t, typesDir, "types.go", `package types

type Version int
`)

	// Consumer using the type via embedded struct
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/types v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/types => ../types",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/types"

type Common struct {
	Version types.Version `+"`"+`toml:"version"`+"`"+`
	Name    string        `+"`"+`toml:"name"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Common
	Extra string `+"`"+`toml:"extra"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import (
	"bytes"
	"testing"
)

func TestDecodeIntWrapper(t *testing.T) {
	input := []byte("version = 14\nname = \"test\"\nextra = \"ok\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if int(doc.Data().Version) != 14 {
		t.Errorf("Version = %d, want 14", doc.Data().Version)
	}
	if doc.Data().Name != "test" {
		t.Errorf("Name = %q, want \"test\"", doc.Data().Name)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	// Round-trip: version should still be integer, not quoted string
	if !bytes.Contains(out, []byte("version = 14")) {
		t.Errorf("encoded output should contain integer: %s", out)
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

func TestIntegrationBlankIdentifierField(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/blankfield",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package blankfield

type Common struct {
	_    string `+"`"+`toml:"repo-type"`+"`"+`
	Name string `+"`"+`toml:"name"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Common
	Extra string `+"`"+`toml:"extra"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "blank_test.go", `package blankfield

import "testing"

func TestBlankFieldRoundTrip(t *testing.T) {
	input := []byte("repo-type = \"legacy\"\nname = \"hello\"\nextra = \"world\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "hello" {
		t.Fatalf("Name = %q, want \"hello\"", cfg.Name)
	}
	if cfg.Extra != "world" {
		t.Fatalf("Extra = %q, want \"world\"", cfg.Extra)
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

func TestIntegrationSliceTextMarshalerCrossPackageImport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Package with a TextMarshaler type
	typesDir := filepath.Join(dir, "types")
	writeFixture(t, typesDir, "go.mod", "module example.com/test/types\n\ngo 1.26\n")
	writeFixture(t, typesDir, "types.go", `package types

type Tag struct{ value string }

func (t Tag) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *Tag) UnmarshalText(b []byte) error { t.value = string(b); return nil }
`)

	// Consumer with []types.Tag field
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/types v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/types => ../types",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/types"

//go:generate tommy generate
type Config struct {
	Name string      `+"`"+`toml:"name"`+"`"+`
	Tags []types.Tag `+"`"+`toml:"tags"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify the import was added to the generated file
	generated, err := os.ReadFile(filepath.Join(consumerDir, "consumer_tommy.go"))
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	if !strings.Contains(string(generated), `"example.com/test/types"`) {
		t.Error("generated file should import the cross-package type's package")
	}

	// Write a round-trip test to verify compilation and runtime behavior
	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestSliceCrossPackageRoundTrip(t *testing.T) {
	input := []byte("name = \"test\"\ntags = [\"foo\", \"bar\"]\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Name != "test" {
		t.Fatalf("Name = %q, want \"test\"", doc.Data().Name)
	}
	if len(doc.Data().Tags) != 2 {
		t.Fatalf("Tags len = %d, want 2", len(doc.Data().Tags))
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
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression test for #28: struct with both scalar (ids.TypeStruct) and slice
// ([]ids.TagStruct) of cross-package TextMarshaler types within the same module.
// The generated file must import the cross-package and compile.
func TestIntegrationSliceTextMarshalerCrossPackageImportMixed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Single module with two packages (like dodder's ids + repo_configs)
	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	// Cross-package TextMarshaler types (same module, different package)
	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "ids.go", `package ids

type TagStruct struct{ value string }
func (t TagStruct) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *TagStruct) UnmarshalText(b []byte) error { t.value = string(b); return nil }

type TypeStruct struct{ value string }
func (t TypeStruct) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *TypeStruct) UnmarshalText(b []byte) error { t.value = string(b); return nil }
`)

	// Same-module struct using both scalar and slice of cross-package TextMarshaler
	configDir := filepath.Join(dir, "config")
	writeFixture(t, configDir, "config.go", `package config

import "example.com/test/ids"

//go:generate tommy generate
type Defaults struct {
	Typ  ids.TypeStruct  `+"`"+`toml:"typ"`+"`"+`
	Tags []ids.TagStruct `+"`"+`toml:"tags"`+"`"+`
}
`)

	if err := Generate(configDir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify the import was added to the generated file
	generated, err := os.ReadFile(filepath.Join(configDir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	if !strings.Contains(string(generated), `"example.com/test/ids"`) {
		t.Errorf("generated file missing import for cross-package type.\nGenerated:\n%s", string(generated))
	}

	// Write a round-trip test
	writeFixture(t, configDir, "config_test.go", `package config

import "testing"

func TestMixedCrossPackageRoundTrip(t *testing.T) {
	input := []byte("typ = \"mytype\"\ntags = [\"foo\", \"bar\"]\n")

	doc, err := DecodeDefaults(input)
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

	cmdMixed := exec.Command("go", "test", "-v", "./...")
	cmdMixed.Dir = dir
	cmdMixed.Env = append(os.Environ(), "GOFLAGS=")
	outputMixed, err := cmdMixed.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", outputMixed)
	}
}

// Regression test for #29: FieldTextMarshaler fields should NOT produce imports
// because the generated code accesses them via promoted field methods
// (d.data.Key.UnmarshalText), not by qualified type name. Only
// FieldSliceTextMarshaler and primitive wrappers need imports.
func TestIntegrationNoUnusedImportsForTextMarshalerFields(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Single module with cross-package TextMarshaler type
	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	extDir := filepath.Join(dir, "ext")
	writeFixture(t, extDir, "ext.go", `package ext

type Id struct{ value string }
func (i Id) MarshalText() ([]byte, error)  { return []byte(i.value), nil }
func (i *Id) UnmarshalText(b []byte) error { i.value = string(b); return nil }
`)

	configDir := filepath.Join(dir, "config")
	writeFixture(t, configDir, "config.go", `package config

import "example.com/test/ext"

//go:generate tommy generate
type Config struct {
	Key  ext.Id `+"`"+`toml:"key"`+"`"+`
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)

	if err := Generate(configDir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Generated file must compile (no unused imports)
	cmdBuild := exec.Command("go", "build", ".")
	cmdBuild.Dir = configDir
	cmdBuild.Env = append(os.Environ(), "GOFLAGS=")
	buildOutput, err := cmdBuild.CombinedOutput()
	if err != nil {
		t.Fatalf("generated file does not compile (unused import?):\n%s", buildOutput)
	}
}

// Regression test for #28: type aliases (type TagStruct = tagStruct) cause
// obj.Type().(*types.Named) to fail because aliases resolve to the target type.
// The import path must still be extracted for []pkg.AliasType fields.
func TestIntegrationSliceTextMarshalerTypeAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	// Cross-package types using aliases to unexported structs (dodder pattern)
	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "ids.go", `package ids

type tagStruct struct{ value string }
func (t tagStruct) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *tagStruct) UnmarshalText(b []byte) error { t.value = string(b); return nil }

type TagStruct = tagStruct

type typeStruct struct{ value string }
func (t typeStruct) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *typeStruct) UnmarshalText(b []byte) error { t.value = string(b); return nil }

type TypeStruct = typeStruct
`)

	configDir := filepath.Join(dir, "config")
	writeFixture(t, configDir, "config.go", `package config

import "example.com/test/ids"

//go:generate tommy generate
type Defaults struct {
	Typ       ids.TypeStruct  `+"`"+`toml:"typ"`+"`"+`
	Etiketten []ids.TagStruct `+"`"+`toml:"etiketten"`+"`"+`
}
`)

	if err := Generate(configDir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	generated, err := os.ReadFile(filepath.Join(configDir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	if !strings.Contains(string(generated), `"example.com/test/ids"`) {
		t.Errorf("generated file missing import for cross-package type alias.\nGenerated:\n%s", string(generated))
	}

	// Must compile
	cmdBuild := exec.Command("go", "build", ".")
	cmdBuild.Dir = configDir
	cmdBuild.Env = append(os.Environ(), "GOFLAGS=")
	buildOutput, err := cmdBuild.CombinedOutput()
	if err != nil {
		t.Fatalf("generated file does not compile:\n%s", buildOutput)
	}
}

// Sub-case 1: Cross-package named struct fields (#22)
func TestIntegrationCrossPackageNamedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "other" package with a simple struct (also tommy-generated for delegation)
	otherDir := filepath.Join(dir, "other")
	writeFixture(t, otherDir, "go.mod", strings.Join([]string{
		"module example.com/test/other",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, otherDir, "config.go", `package other

//go:generate tommy generate
type Config struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(otherDir, "config.go"); err != nil {
		t.Fatalf("Generate other: %v", err)
	}

	// Consumer using other.Config as a named field
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/other v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/other => ../other",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/other"

//go:generate tommy generate
type Server struct {
	Name     string       `+"`"+`toml:"name"`+"`"+`
	Settings other.Config `+"`"+`toml:"settings"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestCrossPackageNamedStructRoundTrip(t *testing.T) {
	input := []byte("name = \"web\"\n\n[settings]\nhost = \"localhost\"\nport = 8080\n")

	doc, err := DecodeServer(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "web" {
		t.Fatalf("Name = %q, want \"web\"", cfg.Name)
	}
	if cfg.Settings.Host != "localhost" {
		t.Fatalf("Settings.Host = %q, want \"localhost\"", cfg.Settings.Host)
	}
	if cfg.Settings.Port != 8080 {
		t.Fatalf("Settings.Port = %d, want 8080", cfg.Settings.Port)
	}

	cfg.Settings.Port = 9090

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeServer(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if d2.Settings.Port != 9090 {
		t.Fatalf("re-decoded Port = %d, want 9090", d2.Settings.Port)
	}
	if d2.Settings.Host != "localhost" {
		t.Fatalf("re-decoded Host = %q, want \"localhost\"", d2.Settings.Host)
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

// Sub-case 2: map[string]CrossPackageStruct (#22)
func TestIntegrationMapStringCrossPackageStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "other" package with a struct (must also be generated for delegation)
	otherDir := filepath.Join(dir, "other")
	writeFixture(t, otherDir, "go.mod", strings.Join([]string{
		"module example.com/test/other",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, otherDir, "action.go", `package other

//go:generate tommy generate
type Action struct {
	Command string `+"`"+`toml:"command"`+"`"+`
	Timeout int    `+"`"+`toml:"timeout"`+"`"+`
}
`)

	if err := Generate(otherDir, "action.go"); err != nil {
		t.Fatalf("Generate other: %v", err)
	}

	// Consumer using map[string]other.Action
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/other v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/other => ../other",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/other"

//go:generate tommy generate
type Config struct {
	Name    string                    `+"`"+`toml:"name"`+"`"+`
	Actions map[string]other.Action   `+"`"+`toml:"actions,omitempty"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestMapCrossPackageStructRoundTrip(t *testing.T) {
	input := []byte("name = \"myapp\"\n\n[actions.build]\ncommand = \"make\"\ntimeout = 30\n\n[actions.test]\ncommand = \"go test\"\ntimeout = 60\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "myapp" {
		t.Fatalf("Name = %q, want \"myapp\"", cfg.Name)
	}
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	build := cfg.Actions["build"]
	if build.Command != "make" {
		t.Fatalf("Actions[build].Command = %q, want \"make\"", build.Command)
	}
	if build.Timeout != 30 {
		t.Fatalf("Actions[build].Timeout = %d, want 30", build.Timeout)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Actions) != 2 {
		t.Fatalf("re-decoded actions count = %d, want 2", len(d2.Actions))
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

// Regression: *types.Alias not unwrapped in cross-package recursive struct field resolution (#32)
func TestIntegrationCrossPackageTypeAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "other" package with a type alias to an unexported struct (also tommy-generated)
	otherDir := filepath.Join(dir, "other")
	writeFixture(t, otherDir, "go.mod", strings.Join([]string{
		"module example.com/test/other",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, otherDir, "types.go", `package other

type inner struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}

type Alias = inner

//go:generate tommy generate
type Wrapper struct {
	Item Alias `+"`"+`toml:"item"`+"`"+`
}
`)

	if err := Generate(otherDir, "types.go"); err != nil {
		t.Fatalf("Generate other: %v", err)
	}

	// Consumer using other.Wrapper (which contains an alias field)
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/other v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/other => ../other",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/other"

//go:generate tommy generate
type Config struct {
	Name    string        `+"`"+`toml:"name"`+"`"+`
	Wrapper other.Wrapper `+"`"+`toml:"wrapper"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestCrossPackageTypeAliasRoundTrip(t *testing.T) {
	input := []byte("name = \"app\"\n\n[wrapper]\n\n[wrapper.item]\nvalue = \"hello\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "app" {
		t.Fatalf("Name = %q, want \"app\"", cfg.Name)
	}
	if cfg.Wrapper.Item.Value != "hello" {
		t.Fatalf("Wrapper.Item.Value = %q, want \"hello\"", cfg.Wrapper.Item.Value)
	}

	cfg.Wrapper.Item.Value = "world"

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if doc2.Data().Wrapper.Item.Value != "world" {
		t.Fatalf("re-decoded Value = %q, want \"world\"", doc2.Data().Wrapper.Item.Value)
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

// Regression: map[string]NamedMapType fails when value type is a named map[string]string (#33)
func TestIntegrationMapStringNamedMapType(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/namedmap",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package namedmap

type UTIGroup map[string]string

//go:generate tommy generate
type Config struct {
	Name   string                `+"`"+`toml:"name"`+"`"+`
	Groups map[string]UTIGroup   `+"`"+`toml:"groups"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package namedmap

import "testing"

func TestMapStringNamedMapTypeRoundTrip(t *testing.T) {
	input := []byte("name = \"types\"\n\n[groups.editors]\nvim = \"text/plain\"\nemacs = \"text/plain\"\n\n[groups.compilers]\ngcc = \"application/x-executable\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "types" {
		t.Fatalf("Name = %q, want \"types\"", cfg.Name)
	}
	if len(cfg.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(cfg.Groups))
	}
	editors := cfg.Groups["editors"]
	if editors["vim"] != "text/plain" {
		t.Fatalf("Groups[editors][vim] = %q, want \"text/plain\"", editors["vim"])
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if len(doc2.Data().Groups) != 2 {
		t.Fatalf("re-decoded groups count = %d, want 2", len(doc2.Data().Groups))
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

// Regression: []*Struct (slice of pointer-to-struct) not supported (#34)
func TestIntegrationSlicePointerToStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/sliceptr",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package sliceptr

type Inner struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Title string    `+"`"+`toml:"title"`+"`"+`
	Items []*Inner  `+"`"+`toml:"items"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package sliceptr

import "testing"

func TestSlicePointerToStructRoundTrip(t *testing.T) {
	input := []byte("title = \"test\"\n\n[[items]]\nname = \"first\"\n\n[[items]]\nname = \"second\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Title != "test" {
		t.Fatalf("Title = %q, want \"test\"", cfg.Title)
	}
	if len(cfg.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(cfg.Items))
	}
	if cfg.Items[0] == nil || cfg.Items[0].Name != "first" {
		t.Fatalf("Items[0].Name = %q, want \"first\"", cfg.Items[0].Name)
	}
	if cfg.Items[1] == nil || cfg.Items[1].Name != "second" {
		t.Fatalf("Items[1].Name = %q, want \"second\"", cfg.Items[1].Name)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Items) != 2 {
		t.Fatalf("re-decoded items count = %d, want 2", len(d2.Items))
	}
	if d2.Items[1].Name != "second" {
		t.Fatalf("re-decoded Items[1].Name = %q, want \"second\"", d2.Items[1].Name)
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

// Sub-case 3: Cross-package slice alias and non-TextMarshaler struct (#22)
func TestIntegrationCrossPackageSliceAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "other" package with a slice alias and a non-TextMarshaler struct
	otherDir := filepath.Join(dir, "other")
	writeFixture(t, otherDir, "go.mod", "module example.com/test/other\n\ngo 1.26\n")
	writeFixture(t, otherDir, "types.go", `package other

type IntSlice []int
`)

	// Consumer using other.IntSlice
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/other v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/other => ../other",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/other"

//go:generate tommy generate
type Config struct {
	Name    string          `+"`"+`toml:"name"`+"`"+`
	Buckets other.IntSlice  `+"`"+`toml:"buckets"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestCrossPackageSliceAliasRoundTrip(t *testing.T) {
	input := []byte("name = \"store\"\nbuckets = [1, 2, 4, 8]\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "store" {
		t.Fatalf("Name = %q, want \"store\"", cfg.Name)
	}
	if len(cfg.Buckets) != 4 {
		t.Fatalf("expected 4 buckets, got %d", len(cfg.Buckets))
	}
	if cfg.Buckets[0] != 1 || cfg.Buckets[3] != 8 {
		t.Fatalf("unexpected bucket values: %v", cfg.Buckets)
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
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

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

	cmdVal := exec.Command("go", "test", "-v", "./...")
	cmdVal.Dir = dir
	cmdVal.Env = append(os.Environ(), "GOFLAGS=")
	if output, err := cmdVal.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

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

// Core #35 test: cross-package struct with unexported nested type, delegated via DecodeInto/EncodeFrom
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

// Regression #38: pointer-to-cross-package-struct delegation should not produce **T
func TestIntegrationPointerCrossPackageDelegation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// External package with a struct
	extDir := filepath.Join(dir, "scriptcfg")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/scriptcfg",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, extDir, "script.go", `package scriptcfg

//go:generate tommy generate
type ScriptConfig struct {
	Description string            `+"`"+`toml:"description"`+"`"+`
	Shell       []string          `+"`"+`toml:"shell,omitempty"`+"`"+`
	Script      string            `+"`"+`toml:"script,omitempty,multiline"`+"`"+`
	Env         map[string]string `+"`"+`toml:"env,omitempty"`+"`"+`
}
`)

	if err := Generate(extDir, "script.go"); err != nil {
		t.Fatalf("Generate scriptcfg: %v", err)
	}

	// Consumer with *scriptcfg.ScriptConfig field
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/scriptcfg v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/scriptcfg => ../scriptcfg",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "blob.go", `package consumer

import "example.com/test/scriptcfg"

//go:generate tommy generate
type Blob struct {
	Name        string                      `+"`"+`toml:"name"`+"`"+`
	ExecCommand *scriptcfg.ScriptConfig     `+"`"+`toml:"exec-command,omitempty"`+"`"+`
}
`)

	if err := Generate(consumerDir, "blob.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	writeFixture(t, consumerDir, "blob_test.go", `package consumer

import "testing"

func TestPointerDelegationRoundTrip(t *testing.T) {
	input := []byte("name = \"mybuild\"\n\n[exec-command]\ndescription = \"run build\"\nscript = \"make all\"\n")

	doc, err := DecodeBlob(input)
	if err != nil {
		t.Fatalf("DecodeBlob: %v", err)
	}

	cfg := doc.Data()
	if cfg.Name != "mybuild" {
		t.Fatalf("Name = %q, want \"mybuild\"", cfg.Name)
	}
	if cfg.ExecCommand == nil {
		t.Fatal("ExecCommand should not be nil")
	}
	if cfg.ExecCommand.Description != "run build" {
		t.Fatalf("Description = %q, want \"run build\"", cfg.ExecCommand.Description)
	}
	if cfg.ExecCommand.Script != "make all" {
		t.Fatalf("Script = %q, want \"make all\"", cfg.ExecCommand.Script)
	}

	cfg.ExecCommand.Script = "make test"

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeBlob(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if doc2.Data().ExecCommand.Script != "make test" {
		t.Fatalf("re-decoded Script = %q, want \"make test\"", doc2.Data().ExecCommand.Script)
	}
}

func TestPointerDelegationNilOmitted(t *testing.T) {
	input := []byte("name = \"simple\"\n")

	doc, err := DecodeBlob(input)
	if err != nil {
		t.Fatalf("DecodeBlob: %v", err)
	}

	cfg := doc.Data()
	if cfg.ExecCommand != nil {
		t.Fatal("ExecCommand should be nil")
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

// Regression #39: delegation should not emit unused imports from delegated struct's inner fields
func TestIntegrationDelegationNoUnusedImports(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "ids" package with a TextMarshaler type
	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "go.mod", "module example.com/test/ids\n\ngo 1.26\n")
	writeFixture(t, idsDir, "tag.go", `package ids

type tagStruct struct{ value string }
type TagStruct = tagStruct

func (t tagStruct) MarshalText() ([]byte, error) { return []byte(t.value), nil }
func (t *tagStruct) UnmarshalText(b []byte) error { t.value = string(b); return nil }
`)

	// "defaults" package that uses ids.TagStruct
	defaultsDir := filepath.Join(dir, "defaults")
	writeFixture(t, defaultsDir, "go.mod", strings.Join([]string{
		"module example.com/test/defaults",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/ids v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/ids => ../ids",
		")",
		"",
	}, "\n"))
	writeFixture(t, defaultsDir, "defaults.go", `package defaults

import "example.com/test/ids"

//go:generate tommy generate
type Defaults struct {
	Tags []ids.TagStruct `+"`"+`toml:"tags,omitempty"`+"`"+`
}
`)

	if err := Generate(defaultsDir, "defaults.go"); err != nil {
		t.Fatalf("Generate defaults: %v", err)
	}

	// Consumer that delegates to defaults — should NOT import "ids"
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/defaults v0.0.0",
		"\texample.com/test/ids v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/defaults => ../defaults",
		"\texample.com/test/ids => ../ids",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/test/defaults"

//go:generate tommy generate
type Config struct {
	Name     string            `+"`"+`toml:"name"`+"`"+`
	Defaults defaults.Defaults `+"`"+`toml:"defaults"`+"`"+`
}
`)

	if err := Generate(consumerDir, "config.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	// The generated file should compile without "imported and not used" errors
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed (likely unused import):\n%s", output)
	}
}

// Regression #40: regeneration over existing _tommy.go files must be idempotent
func TestIntegrationRegenerateOverExistingTommyFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Leaf package
	leafDir := filepath.Join(dir, "leaf")
	writeFixture(t, leafDir, "go.mod", strings.Join([]string{
		"module example.com/test/leaf",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, leafDir, "config.go", `package leaf

//go:generate tommy generate
type Config struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	// Generate once
	if err := Generate(leafDir, "config.go"); err != nil {
		t.Fatalf("Generate leaf (first): %v", err)
	}

	firstOutput, err := os.ReadFile(filepath.Join(leafDir, "config_tommy.go"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(firstOutput), "func DecodeConfigInto(") {
		t.Fatal("first generation missing DecodeConfigInto")
	}

	// Generate again over existing _tommy.go (simulates go generate ./... re-run)
	if err := Generate(leafDir, "config.go"); err != nil {
		t.Fatalf("Generate leaf (second): %v", err)
	}

	secondOutput, err := os.ReadFile(filepath.Join(leafDir, "config_tommy.go"))
	if err != nil {
		t.Fatal(err)
	}

	if string(firstOutput) != string(secondOutput) {
		t.Fatalf("regeneration over existing _tommy.go produced different output.\nFirst length: %d\nSecond length: %d", len(firstOutput), len(secondOutput))
	}
}

// Regression #40: multi-package regeneration via go generate ./...
func TestIntegrationGoGenerateAllMultiPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Monorepo with two packages: leaf and consumer
	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	leafDir := filepath.Join(dir, "leaf")
	writeFixture(t, leafDir, "config.go", `package leaf

//go:generate tommy generate
type Config struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "app.go", `package consumer

import "example.com/test/leaf"

//go:generate tommy generate
type App struct {
	Name   string      `+"`"+`toml:"name"`+"`"+`
	Config leaf.Config `+"`"+`toml:"config"`+"`"+`
}
`)

	// Generate individually in dependency order
	if err := Generate(leafDir, "config.go"); err != nil {
		t.Fatalf("Generate leaf: %v", err)
	}
	if err := Generate(consumerDir, "app.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	leafIndividual, _ := os.ReadFile(filepath.Join(leafDir, "config_tommy.go"))
	consumerIndividual, _ := os.ReadFile(filepath.Join(consumerDir, "app_tommy.go"))

	// Build tommy binary from current source
	tommyBin := filepath.Join(t.TempDir(), "tommy")
	buildCmd := exec.Command("go", "build", "-o", tommyBin, "./cmd/tommy")
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(os.Environ(), "GOFLAGS=")
	if buildOut, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build tommy: %v\n%s", err, buildOut)
	}

	// Regenerate via go generate ./...
	cmd := exec.Command("go", "generate", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "PATH="+filepath.Dir(tommyBin)+":"+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go generate ./... failed: %v\n%s", err, output)
	}

	leafAll, _ := os.ReadFile(filepath.Join(leafDir, "config_tommy.go"))
	consumerAll, _ := os.ReadFile(filepath.Join(consumerDir, "app_tommy.go"))

	if !strings.Contains(string(leafAll), "func DecodeConfigInto(") {
		t.Fatalf("go generate ./... dropped leaf DecodeConfigInto.\nOutput:\n%s", string(leafAll))
	}
	if !strings.Contains(string(consumerAll), "func DecodeAppInto(") {
		t.Fatalf("go generate ./... dropped consumer DecodeAppInto.\nOutput:\n%s", string(consumerAll))
	}

	if string(leafIndividual) != string(leafAll) {
		t.Fatalf("leaf: go generate ./... produced different output than individual generate")
	}
	if string(consumerIndividual) != string(consumerAll) {
		t.Fatalf("consumer: go generate ./... produced different output than individual generate")
	}

	// Verify the full project compiles and tests pass
	writeFixture(t, consumerDir, "app_test.go", `package consumer

import "testing"

func TestAppRoundTrip(t *testing.T) {
	input := []byte("name = \"myapp\"\n\n[config]\nhost = \"localhost\"\nport = 8080\n")
	doc, err := DecodeApp(input)
	if err != nil {
		t.Fatal(err)
	}
	cfg := doc.Data()
	if cfg.Config.Host != "localhost" {
		t.Fatalf("Host = %q, want \"localhost\"", cfg.Config.Host)
	}
}
`)

	testCmd := exec.Command("go", "test", "./...")
	testCmd.Dir = dir
	testCmd.Env = append(os.Environ(), "GOFLAGS=")
	if testOut, err := testCmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", testOut)
	}
}

func TestIntegrationCommentAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/commentapi",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package commentapi

//go:generate tommy generate
type Config struct {
	Name string `+"`"+`toml:"name"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "comment_test.go", `package commentapi

import (
	"strings"
	"testing"
)

func TestCommentGetSet(t *testing.T) {
	input := []byte("# Server name\nname = \"myapp\"\nport = 8080 # default port\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	// Read above-key comment
	if got := doc.Comment("name"); got != "# Server name" {
		t.Fatalf("Comment(name) = %q, want %q", got, "# Server name")
	}

	// Read inline comment
	if got := doc.InlineComment("port"); got != "# default port" {
		t.Fatalf("InlineComment(port) = %q, want %q", got, "# default port")
	}

	// Set a new above-key comment
	doc.SetComment("port", "# HTTP port")

	// Set a new inline comment
	doc.SetInlineComment("name", "# app identifier")

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	result := string(out)

	if !strings.Contains(result, "# HTTP port") {
		t.Fatalf("SetComment not reflected in output:\n%s", result)
	}
	if !strings.Contains(result, "# app identifier") {
		t.Fatalf("SetInlineComment not reflected in output:\n%s", result)
	}

	// Round-trip: decode the output and verify comments persist
	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc2.Comment("port"); got != "# HTTP port" {
		t.Fatalf("after round-trip Comment(port) = %q, want %q", got, "# HTTP port")
	}
	if got := doc2.InlineComment("name"); got != "# app identifier" {
		t.Fatalf("after round-trip InlineComment(name) = %q, want %q", got, "# app identifier")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
	t.Log(string(out))
}

func TestIntegrationEncodeFromEmptyDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/emptydoc",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package emptydoc

//go:generate tommy generate
type AppConfig struct {
	Name     string   `+"`"+`toml:"name"`+"`"+`
	Tags     []string `+"`"+`toml:"tags"`+"`"+`
	Defaults Defaults `+"`"+`toml:"defaults"`+"`"+`
	Output   Output   `+"`"+`toml:"output"`+"`"+`
}

type Defaults struct {
	Type    string `+"`"+`toml:"type"`+"`"+`
	Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}

type Output struct {
	Format  string `+"`"+`toml:"format"`+"`"+`
	Verbose bool   `+"`"+`toml:"verbose"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "emptydoc_test.go", `package emptydoc

import (
	"strings"
	"testing"
)

func TestEncodeFromEmptyDocumentPreservesNestedTables(t *testing.T) {
	// Issue #42: Encoding a struct into a document parsed from empty bytes
	// silently drops all nested [table] sections because FindTable returns nil
	// when no table exists in the CST.
	doc, err := DecodeAppConfig([]byte{})
	if err != nil {
		t.Fatalf("DecodeAppConfig(empty): %v", err)
	}

	*doc.Data() = AppConfig{
		Name: "myapp",
		Tags: []string{"alpha", "beta"},
		Defaults: Defaults{
			Type:    "!md",
			Enabled: true,
		},
		Output: Output{
			Format:  "json",
			Verbose: true,
		},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)

	// Top-level fields must be present.
	if !strings.Contains(result, "name = \"myapp\"") {
		t.Fatalf("top-level 'name' missing from output:\n%s", result)
	}

	// Nested [defaults] table must be present — this is the core of #42.
	if !strings.Contains(result, "[defaults]") {
		t.Fatalf("[defaults] table silently dropped when encoding from empty document:\n%s", result)
	}
	if !strings.Contains(result, "type = \"!md\"") {
		t.Fatalf("defaults.type field missing from output:\n%s", result)
	}
	if !strings.Contains(result, "enabled = true") {
		t.Fatalf("defaults.enabled field missing from output:\n%s", result)
	}

	// Second nested table [output] must also be present.
	if !strings.Contains(result, "[output]") {
		t.Fatalf("[output] table silently dropped when encoding from empty document:\n%s", result)
	}
	if !strings.Contains(result, "format = \"json\"") {
		t.Fatalf("output.format field missing from output:\n%s", result)
	}
	if !strings.Contains(result, "verbose = true") {
		t.Fatalf("output.verbose field missing from output:\n%s", result)
	}

	// Verify the output can be decoded back correctly.
	doc2, err := DecodeAppConfig(out)
	if err != nil {
		t.Fatalf("re-decode failed: %v", err)
	}
	d := doc2.Data()
	if d.Name != "myapp" {
		t.Fatalf("re-decoded Name = %q, want \"myapp\"", d.Name)
	}
	if d.Defaults.Type != "!md" {
		t.Fatalf("re-decoded Defaults.Type = %q, want \"!md\"", d.Defaults.Type)
	}
	if d.Defaults.Enabled != true {
		t.Fatal("re-decoded Defaults.Enabled = false, want true")
	}
	if d.Output.Format != "json" {
		t.Fatalf("re-decoded Output.Format = %q, want \"json\"", d.Output.Format)
	}
	if d.Output.Verbose != true {
		t.Fatal("re-decoded Output.Verbose = false, want true")
	}
}

func TestEncodeFromEmptyDocumentPointerStruct(t *testing.T) {
	// Same issue but for *Struct fields — FieldPointerStruct uses
	// FindTableInContainer which also returns nil on empty documents.
	doc, err := DecodeAppConfig([]byte("name = \"x\"\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Set nested struct fields — the document has no [defaults] or [output] tables.
	d := doc.Data()
	d.Defaults = Defaults{Type: "txt", Enabled: true}
	d.Output = Output{Format: "yaml", Verbose: false}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)
	if !strings.Contains(result, "[defaults]") {
		t.Fatalf("[defaults] table dropped when not in original input:\n%s", result)
	}
	if !strings.Contains(result, "type = \"txt\"") {
		t.Fatalf("defaults.type missing:\n%s", result)
	}
	if !strings.Contains(result, "[output]") {
		t.Fatalf("[output] table dropped when not in original input:\n%s", result)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromEmpty", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromEmptyPointerStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/emptyptr",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package emptyptr

//go:generate tommy generate
type Config struct {
	Name  string  `+"`"+`toml:"name"`+"`"+`
	Hooks *Hooks  `+"`"+`toml:"hooks"`+"`"+`
}

type Hooks struct {
	Create *string `+"`"+`toml:"create"`+"`"+`
	Stop   *string `+"`"+`toml:"stop"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "emptyptr_test.go", `package emptyptr

import (
	"strings"
	"testing"
)

func TestEncodePointerStructFromEmptyDocument(t *testing.T) {
	// Issue #42: pointer-struct variant. When the document is empty,
	// FindTableInContainer returns nil and the entire *Hooks is dropped.
	doc, err := DecodeConfig([]byte{})
	if err != nil {
		t.Fatalf("DecodeConfig(empty): %v", err)
	}

	create := "npm install"
	stop := "just test"
	*doc.Data() = Config{
		Name: "myapp",
		Hooks: &Hooks{
			Create: &create,
			Stop:   &stop,
		},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)
	if !strings.Contains(result, "[hooks]") {
		t.Fatalf("[hooks] table silently dropped when encoding *Hooks from empty document:\n%s", result)
	}
	if !strings.Contains(result, "create = \"npm install\"") {
		t.Fatalf("hooks.create missing from output:\n%s", result)
	}
	if !strings.Contains(result, "stop = \"just test\"") {
		t.Fatalf("hooks.stop missing from output:\n%s", result)
	}

	// Round-trip verification.
	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode failed: %v", err)
	}
	d := doc2.Data()
	if d.Hooks == nil {
		t.Fatal("re-decoded Hooks is nil")
	}
	if d.Hooks.Create == nil || *d.Hooks.Create != "npm install" {
		t.Fatalf("re-decoded Hooks.Create = %v, want \"npm install\"", d.Hooks.Create)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodePointerStruct", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromEmptyDelegatedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// External package with a struct
	extDir := filepath.Join(dir, "dbcfg")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/dbcfg",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, extDir, "db.go", `package dbcfg

//go:generate tommy generate
type Database struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(extDir, "db.go"); err != nil {
		t.Fatalf("Generate dbcfg: %v", err)
	}

	// Consumer with dbcfg.Database (non-pointer, delegated)
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/dbcfg v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/dbcfg => ../dbcfg",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "app.go", `package consumer

import "example.com/test/dbcfg"

//go:generate tommy generate
type App struct {
	Name string          `+"`"+`toml:"name"`+"`"+`
	DB   dbcfg.Database  `+"`"+`toml:"database"`+"`"+`
}
`)

	if err := Generate(consumerDir, "app.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	writeFixture(t, consumerDir, "app_test.go", `package consumer

import (
	"strings"
	"testing"
	"example.com/test/dbcfg"
)

func TestDelegatedStructFromEmptyDocument(t *testing.T) {
	// Issue #42: FieldDelegatedStruct uses FindTable which returns nil
	// on an empty document, silently dropping the delegated table.
	doc, err := DecodeApp([]byte{})
	if err != nil {
		t.Fatalf("DecodeApp(empty): %v", err)
	}

	*doc.Data() = App{
		Name: "webapp",
		DB: dbcfg.Database{
			Host: "db.example.com",
			Port: 5432,
		},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)
	if !strings.Contains(result, "[database]") {
		t.Fatalf("[database] table silently dropped for delegated struct from empty document:\n%s", result)
	}
	if !strings.Contains(result, "host = \"db.example.com\"") {
		t.Fatalf("database.host missing:\n%s", result)
	}
	if !strings.Contains(result, "port = 5432") {
		t.Fatalf("database.port missing:\n%s", result)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestDelegatedStruct", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromEmptyPointerDelegatedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// External package
	extDir := filepath.Join(dir, "logcfg")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/logcfg",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, extDir, "log.go", `package logcfg

//go:generate tommy generate
type LogConfig struct {
	Level  string `+"`"+`toml:"level"`+"`"+`
	Format string `+"`"+`toml:"format"`+"`"+`
}
`)

	if err := Generate(extDir, "log.go"); err != nil {
		t.Fatalf("Generate logcfg: %v", err)
	}

	// Consumer with *logcfg.LogConfig (pointer, delegated)
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/logcfg v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/logcfg => ../logcfg",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "svc.go", `package consumer

import "example.com/test/logcfg"

//go:generate tommy generate
type Service struct {
	Name    string             `+"`"+`toml:"name"`+"`"+`
	Logging *logcfg.LogConfig  `+"`"+`toml:"logging"`+"`"+`
}
`)

	if err := Generate(consumerDir, "svc.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	writeFixture(t, consumerDir, "svc_test.go", `package consumer

import (
	"strings"
	"testing"
	"example.com/test/logcfg"
)

func TestPointerDelegatedStructFromEmptyDocument(t *testing.T) {
	// Issue #42: FieldPointerDelegatedStruct uses FindTableInContainer
	// which returns nil on an empty document.
	doc, err := DecodeService([]byte{})
	if err != nil {
		t.Fatalf("DecodeService(empty): %v", err)
	}

	*doc.Data() = Service{
		Name: "api",
		Logging: &logcfg.LogConfig{
			Level:  "debug",
			Format: "json",
		},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)
	if !strings.Contains(result, "[logging]") {
		t.Fatalf("[logging] table silently dropped for pointer-delegated struct from empty document:\n%s", result)
	}
	if !strings.Contains(result, "level = \"debug\"") {
		t.Fatalf("logging.level missing:\n%s", result)
	}
	if !strings.Contains(result, "format = \"json\"") {
		t.Fatalf("logging.format missing:\n%s", result)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestPointerDelegated", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromEmptyNestedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/nested",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package nested

//go:generate tommy generate
type Root struct {
	Name  string `+"`"+`toml:"name"`+"`"+`
	Mid   Middle `+"`"+`toml:"mid"`+"`"+`
}

type Middle struct {
	Label string `+"`"+`toml:"label"`+"`"+`
	Inner Leaf   `+"`"+`toml:"inner"`+"`"+`
}

type Leaf struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}


	writeFixture(t, dir, "nested_test.go", `package nested

import (
	"strings"
	"testing"
)

func TestDeeplyNestedStructFromEmptyDocument(t *testing.T) {
	// Issue #42: struct-within-struct-within-struct from empty document.
	// The outer FindTable returns nil, so the inner struct is also unreachable.
	doc, err := DecodeRoot([]byte{})
	if err != nil {
		t.Fatalf("DecodeRoot(empty): %v", err)
	}

	*doc.Data() = Root{
		Name: "root",
		Mid: Middle{
			Label: "middle",
			Inner: Leaf{
				Value: "deep",
			},
		},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)
	if !strings.Contains(result, "[mid]") {
		t.Fatalf("[mid] table dropped from empty document:\n%s", result)
	}
	if !strings.Contains(result, "label = \"middle\"") {
		t.Fatalf("mid.label missing:\n%s", result)
	}
	if !strings.Contains(result, "[mid.inner]") {
		t.Fatalf("[mid.inner] nested table dropped from empty document:\n%s", result)
	}
	if !strings.Contains(result, "value = \"deep\"") {
		t.Fatalf("mid.inner.value missing:\n%s", result)
	}

	// Round-trip.
	doc2, err := DecodeRoot(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d := doc2.Data()
	if d.Mid.Inner.Value != "deep" {
		t.Fatalf("re-decoded Mid.Inner.Value = %q, want \"deep\"", d.Mid.Inner.Value)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestDeeplyNested", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()

	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Issue #43: cross-package slice-of-structs where the external struct has a
// pointer-to-unexported nested struct.  FieldSliceStruct inlines inner field
// decode/encode, so the generated consumer code must NOT reference unexported
// types from the external package.
func TestIntegrationSliceOfCrossPackageStructWithUnexportedNested(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// External package with an exported struct containing *unexported
	extDir := filepath.Join(dir, "options_print")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/options_print",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, extDir, "options.go", `package options_print

//go:generate tommy generate
type abbreviationsV1 struct {
	ZettelIds *bool `+"`"+`toml:"zettel-ids"`+"`"+`
	ShaIds    *bool `+"`"+`toml:"shas"`+"`"+`
}

//go:generate tommy generate
type V1 struct {
	Abbreviations *abbreviationsV1 `+"`"+`toml:"abbreviations"`+"`"+`
	PrintColors   *bool            `+"`"+`toml:"print-colors"`+"`"+`
}
`)

	if err := Generate(extDir, "options.go"); err != nil {
		t.Fatalf("Generate options_print: %v", err)
	}

	// Consumer package using []options_print.V1 (slice triggers inlined inner field code)
	consumerDir := filepath.Join(dir, "repo_configs")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/repo_configs",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/options_print v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/options_print => ../options_print",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "config.go", `package repo_configs

import "example.com/test/options_print"

//go:generate tommy generate
type V0 struct {
	Outputs []options_print.V1 `+"`"+`toml:"outputs"`+"`"+`
}
`)

	// Generation may succeed but produce code referencing unexported types
	if err := Generate(consumerDir, "config.go"); err != nil {
		t.Fatalf("Generate repo_configs: %v", err)
	}

	// Verify the generated file does NOT reference the unexported type
	genPath := filepath.Join(consumerDir, "config_tommy.go")
	genBytes, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	genCode := string(genBytes)
	if strings.Contains(genCode, "abbreviationsV1") {
		t.Fatalf("generated consumer code references unexported type abbreviationsV1:\n%s", genCode)
	}

	// The generated code should compile and the round-trip should work
	writeFixture(t, consumerDir, "consumer_test.go", `package repo_configs

import "testing"

func TestSliceCrossPackageUnexportedRoundTrip(t *testing.T) {
	input := []byte("[[outputs]]\nprint-colors = true\n\n[outputs.abbreviations]\nzettel-ids = true\nshas = false\n\n[[outputs]]\nprint-colors = false\n")

	doc, err := DecodeV0(input)
	if err != nil {
		t.Fatalf("DecodeV0: %v", err)
	}

	cfg := doc.Data()
	if len(cfg.Outputs) != 2 {
		t.Fatalf("len(Outputs) = %d, want 2", len(cfg.Outputs))
	}
	if cfg.Outputs[0].Abbreviations == nil {
		t.Fatal("Outputs[0].Abbreviations should not be nil")
	}
	if cfg.Outputs[0].Abbreviations.ZettelIds == nil || !*cfg.Outputs[0].Abbreviations.ZettelIds {
		t.Fatal("ZettelIds should be true")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeV0(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Outputs) != 2 {
		t.Fatalf("re-decoded len(Outputs) = %d, want 2", len(d2.Outputs))
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

// Issue #44: codegen fails on slice fields whose element type is a type alias
// to an unexported struct from another package.
// Variant A: the struct with the slice is generated directly (AST path).
func TestIntegrationSliceOfAliasedUnexportedStructDirect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Package with type alias to unexported struct
	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "go.mod", "module example.com/test/ids\n\ngo 1.26\n\n"+
		"require github.com/amarbel-llc/tommy v0.0.0\n\n"+
		"replace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, idsDir, "ids.go", `package ids

type tagStruct struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}

// TagStruct is an exported alias for the unexported tagStruct.
type TagStruct = tagStruct

//go:generate tommy generate
type TagWrapper struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}
`)

	if err := Generate(idsDir, "ids.go"); err != nil {
		t.Fatalf("Generate ids: %v", err)
	}

	// Consumer package that directly has a []ids.TagStruct field
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/ids v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/ids => ../ids",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/test/ids"

//go:generate tommy generate
type Config struct {
	Name string          `+"`"+`toml:"name"`+"`"+`
	Tags []ids.TagStruct `+"`"+`toml:"tags"`+"`"+`
}
`)

	// This should not error — the alias should be followed to the underlying struct
	if err := Generate(consumerDir, "config.go"); err != nil {
		t.Fatalf("Generate consumer (direct slice of aliased type): %v", err)
	}

	// The generated code should compile and round-trip
	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestSliceAliasRoundTrip(t *testing.T) {
	input := []byte("name = \"test\"\n\n[[tags]]\nvalue = \"hello\"\n\n[[tags]]\nvalue = \"world\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	cfg := doc.Data()
	if cfg.Name != "test" {
		t.Fatalf("Name = %q, want \"test\"", cfg.Name)
	}
	if len(cfg.Tags) != 2 {
		t.Fatalf("len(Tags) = %d, want 2", len(cfg.Tags))
	}
	if cfg.Tags[0].Value != "hello" {
		t.Fatalf("Tags[0].Value = %q, want \"hello\"", cfg.Tags[0].Value)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Tags) != 2 || d2.Tags[1].Value != "world" {
		t.Fatalf("re-decoded Tags mismatch: %+v", d2.Tags)
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

func TestIntegrationEmbeddedNonStructWithTomlIgnoreTag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/embeddedskip",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	// An interface type embedded in a struct with toml:"-" should be
	// silently skipped. Before the fix, resolveEmbeddedFields is called
	// before the tag is checked, causing "not a struct" error.
	writeFixture(t, dir, "config.go", `package embeddedskip

type Pool interface {
	Acquire() error
	Release()
}

//go:generate tommy generate
type Config struct {
	Pool `+"`"+`toml:"-"`+"`"+`
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate should succeed when embedded non-struct has toml:\"-\" tag, got: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}
}

// Regression #47 case 2: map[string]CrossPackageStruct where the struct has
// unexported nested fields should delegate to DecodeInto/EncodeFrom rather than
// inlining fields (which would fail because the consumer can't access unexported types).
func TestIntegrationMapStringCrossPackageStructWithUnexported(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// pkga: struct with unexported nested type
	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkga",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

type outputFormat struct {
	Extension string `+"`"+`toml:"extension"`+"`"+`
	MimeType  string `+"`"+`toml:"mime_type"`+"`"+`
}

//go:generate tommy generate
type ScriptConfig struct {
	Command string        `+"`"+`toml:"command"`+"`"+`
	Output  *outputFormat `+"`"+`toml:"output,omitempty"`+"`"+`
}
`)

	if err := Generate(pkgaDir, "pkga.go"); err != nil {
		t.Fatalf("Generate pkga: %v", err)
	}

	// pkgb: consumer with map[string]pkga.ScriptConfig
	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/pkga v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/pkga => ../pkga",
		")",
		"",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Config struct {
	Actions map[string]pkga.ScriptConfig `+"`"+`toml:"actions,omitempty"`+"`"+`
}
`)

	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import "testing"

func TestMapCrossPackageUnexportedRoundTrip(t *testing.T) {
	input := []byte("[actions.build]\ncommand = \"make\"\n\n[actions.build.output]\nextension = \".bin\"\nmime_type = \"application/octet-stream\"\n\n[actions.test]\ncommand = \"go test\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	cfg := doc.Data()
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	build := cfg.Actions["build"]
	if build.Command != "make" {
		t.Fatalf("build.Command = %q, want \"make\"", build.Command)
	}
	if build.Output == nil {
		t.Fatal("build.Output should not be nil")
	}
	if build.Output.Extension != ".bin" {
		t.Fatalf("build.Output.Extension = %q, want \".bin\"", build.Output.Extension)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Actions) != 2 {
		t.Fatalf("re-decoded actions count = %d, want 2", len(d2.Actions))
	}
	if d2.Actions["build"].Output == nil || d2.Actions["build"].Output.Extension != ".bin" {
		t.Fatal("re-decoded build.Output.Extension mismatch")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression #47 case 3a: cross-package named slice type (type IntSlice []int)
// should be unwrapped to its underlying []int and classified as FieldSlicePrimitive.
func TestIntegrationCrossPackageSliceNamedType(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// pkga: external package with a slice type alias
	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", "module example.com/test/pkga\n\ngo 1.26\n")
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

type IntSlice []int
`)

	// pkgb: consumer using pkga.IntSlice
	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/pkga v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/pkga => ../pkga",
		")",
		"",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Stats struct {
	Buckets pkga.IntSlice `+"`"+`toml:"buckets"`+"`"+`
}
`)

	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import "testing"

func TestCrossPackageSliceAliasRoundTrip(t *testing.T) {
	input := []byte("buckets = [10, 20, 30, 40]\n")

	doc, err := DecodeStats(input)
	if err != nil {
		t.Fatalf("DecodeStats: %v", err)
	}

	s := doc.Data()
	if len(s.Buckets) != 4 {
		t.Fatalf("Buckets length = %d, want 4", len(s.Buckets))
	}
	if s.Buckets[0] != 10 || s.Buckets[3] != 40 {
		t.Fatalf("Buckets = %v, want [10 20 30 40]", s.Buckets)
	}

	s.Buckets = append(s.Buckets, 50)
	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeStats(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Buckets) != 5 {
		t.Fatalf("re-decoded Buckets length = %d, want 5", len(d2.Buckets))
	}
	if d2.Buckets[4] != 50 {
		t.Fatalf("re-decoded Buckets[4] = %d, want 50", d2.Buckets[4])
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression #47: map[string]CrossPackageStruct with all-exported fields should
// delegate to DecodeInto/EncodeFrom. This test verifies delegation compiles and
// round-trips correctly when the struct has a Validate() method (which would cause
// the generated code to reference the method if inlined incorrectly).
func TestIntegrationMapStringCrossPackageStructDelegation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// pkga: struct with Validate()
	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkga",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

import "fmt"

//go:generate tommy generate
type Action struct {
	Command string `+"`"+`toml:"command"`+"`"+`
	Timeout int    `+"`"+`toml:"timeout"`+"`"+`
}

func (a Action) Validate() error {
	if a.Command == "" {
		return fmt.Errorf("command must not be empty")
	}
	return nil
}
`)

	if err := Generate(pkgaDir, "pkga.go"); err != nil {
		t.Fatalf("Generate pkga: %v", err)
	}

	// pkgb: consumer with map[string]pkga.Action
	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/pkga v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/pkga => ../pkga",
		")",
		"",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Config struct {
	Actions map[string]pkga.Action `+"`"+`toml:"actions,omitempty"`+"`"+`
}
`)

	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import "testing"

func TestMapCrossPackageDelegationRoundTrip(t *testing.T) {
	input := []byte("[actions.build]\ncommand = \"make\"\ntimeout = 30\n\n[actions.test]\ncommand = \"go test\"\ntimeout = 60\n")
	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	cfg := doc.Data()
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	build := cfg.Actions["build"]
	if build.Command != "make" || build.Timeout != 30 {
		t.Fatalf("build = %+v, want {make 30}", build)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if len(doc2.Data().Actions) != 2 {
		t.Fatalf("re-decoded actions count = %d, want 2", len(doc2.Data().Actions))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression #47: cross-package named map type alias (type ScriptMap map[string]Struct)
// used as a direct field goes through classifySelectorExpr → classifyFromType →
// *types.Map, which has no struct value handling.
func TestIntegrationCrossPackageMapTypeAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// pkga: named map type alias
	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkga",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

//go:generate tommy generate
type Script struct {
	Command string `+"`"+`toml:"command"`+"`"+`
}

type ScriptMap map[string]Script
`)

	if err := Generate(pkgaDir, "pkga.go"); err != nil {
		t.Fatalf("Generate pkga: %v", err)
	}

	// pkgb: consumer using pkga.ScriptMap as a field type
	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/pkga v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/pkga => ../pkga",
		")",
		"",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Config struct {
	Actions pkga.ScriptMap `+"`"+`toml:"actions,omitempty"`+"`"+`
}
`)

	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import "testing"

func TestCrossPackageMapTypeAliasRoundTrip(t *testing.T) {
	input := []byte("[actions.build]\ncommand = \"make\"\n\n[actions.test]\ncommand = \"go test\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	cfg := doc.Data()
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	if cfg.Actions["build"].Command != "make" {
		t.Fatalf("Actions[build].Command = %q, want \"make\"", cfg.Actions["build"].Command)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if len(doc2.Data().Actions) != 2 {
		t.Fatalf("re-decoded actions count = %d, want 2", len(doc2.Data().Actions))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}


// gh#48: omitempty lost when inlining cross-package struct fields.
// When Inner has a TextMarshaler field with omitempty and the zero value,
// the encoder should omit the key. Instead it writes an empty string,
// which then fails on decode via UnmarshalText("").
func TestIntegrationCrossPackageOmitemptyTextMarshaler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "inner" package: struct with a TextMarshaler field tagged omitempty
	innerDir := filepath.Join(dir, "inner")
	writeFixture(t, innerDir, "go.mod", strings.Join([]string{
		"module example.com/test/inner",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, innerDir, "inner.go", `package inner

import "fmt"

//go:generate tommy generate
type Inner struct {
	Name CustomType `+"`"+`toml:"name,omitempty"`+"`"+`
}

type CustomType struct{ Val string }

func (c CustomType) MarshalText() ([]byte, error)  { return []byte(c.Val), nil }
func (c *CustomType) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		return fmt.Errorf("empty value not allowed")
	}
	c.Val = string(b)
	return nil
}
`)

	if err := Generate(innerDir, "inner.go"); err != nil {
		t.Fatalf("Generate inner: %v", err)
	}

	// "outer" package: uses inner.Inner as a named field
	outerDir := filepath.Join(dir, "outer")
	writeFixture(t, outerDir, "go.mod", strings.Join([]string{
		"module example.com/test/outer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/inner v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/inner => ../inner",
		")",
		"",
	}, "\n"))
	writeFixture(t, outerDir, "outer.go", `package outer

import "example.com/test/inner"

//go:generate tommy generate
type Outer struct {
	Label string      `+"`"+`toml:"label"`+"`"+`
	Data  inner.Inner `+"`"+`toml:"data"`+"`"+`
}
`)

	if err := Generate(outerDir, "outer.go"); err != nil {
		t.Fatalf("Generate outer: %v", err)
	}

	writeFixture(t, outerDir, "outer_test.go", `package outer

import (
	"strings"
	"testing"
)

func TestCrossPackageOmitemptyZeroValue(t *testing.T) {
	// Only label is set; data.name is zero-valued and omitempty.
	// The encoder should omit data.name entirely.
	input := []byte("label = \"hello\"\n\n[data]\n")

	doc, err := DecodeOuter(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Label != "hello" {
		t.Fatalf("Label = %q, want \"hello\"", doc.Data().Label)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if strings.Contains(string(out), "name") {
		t.Fatalf("zero-value omitempty TextMarshaler field leaked into output:\n%s", string(out))
	}

	// Round-trip: re-decode should succeed (no UnmarshalText("") error)
	doc2, err := DecodeOuter(out)
	if err != nil {
		t.Fatalf("re-decode failed (omitempty not respected): %v", err)
	}
	if doc2.Data().Label != "hello" {
		t.Fatalf("re-decoded Label = %q, want \"hello\"", doc2.Data().Label)
	}
}

func TestCrossPackageOmitemptyNonZeroValue(t *testing.T) {
	// data.name is set — should survive round-trip.
	input := []byte("label = \"hello\"\n\n[data]\nname = \"world\"\n")

	doc, err := DecodeOuter(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Data.Name.Val != "world" {
		t.Fatalf("Data.Name.Val = %q, want \"world\"", doc.Data().Data.Name.Val)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "name = \"world\"") {
		t.Fatalf("expected name in output, got:\n%s", string(out))
	}

	doc2, err := DecodeOuter(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if doc2.Data().Data.Name.Val != "world" {
		t.Fatalf("re-decoded Name.Val = %q, want \"world\"", doc2.Data().Data.Name.Val)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = outerDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// gh#48: same-package TextMarshaler with omitempty.
// Proves the bug is in emitEncodeField, not the cross-package delegation path.
func TestIntegrationSamePackageOmitemptyTextMarshaler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/test/samepkg",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, dir, "config.go", `package samepkg

import "fmt"

//go:generate tommy generate
type Config struct {
	Label string     `+"`"+`toml:"label"`+"`"+`
	Kind  CustomType `+"`"+`toml:"kind,omitempty"`+"`"+`
}

type CustomType struct{ Val string }

func (c CustomType) MarshalText() ([]byte, error)  { return []byte(c.Val), nil }
func (c *CustomType) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		return fmt.Errorf("empty value not allowed")
	}
	c.Val = string(b)
	return nil
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package samepkg

import (
	"strings"
	"testing"
)

func TestOmitemptyTextMarshalerZeroValue(t *testing.T) {
	// kind is zero-valued and omitempty — should not appear in output.
	input := []byte("label = \"hello\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if strings.Contains(string(out), "kind") {
		t.Fatalf("zero-value omitempty TextMarshaler field leaked into output:\n%s", string(out))
	}

	// Round-trip: re-decode should succeed (no UnmarshalText("") error)
	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode failed (omitempty not respected): %v", err)
	}
	if doc2.Data().Label != "hello" {
		t.Fatalf("re-decoded Label = %q, want \"hello\"", doc2.Data().Label)
	}
}

func TestOmitemptyTextMarshalerNonZeroValue(t *testing.T) {
	// kind is set — should survive round-trip.
	input := []byte("label = \"hello\"\nkind = \"widget\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Kind.Val != "widget" {
		t.Fatalf("Kind.Val = %q, want \"widget\"", doc.Data().Kind.Val)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "kind = \"widget\"") {
		t.Fatalf("expected kind in output, got:\n%s", string(out))
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if doc2.Data().Kind.Val != "widget" {
		t.Fatalf("re-decoded Kind.Val = %q, want \"widget\"", doc2.Data().Kind.Val)
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

func TestIntegrationEncodeFromNilPointerStruct(t *testing.T) {
	// Issue #49: pointer-to-struct fields silently dropped when encoding from
	// scratch (nil input). Verifies that DecodeX(nil) followed by populating
	// *SubStruct and Encode() round-trips correctly.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/issue49",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "v2.go", `package issue49

//go:generate tommy generate
type V2 struct {
	Abbreviations    *abbreviationsV2 `+"`"+`toml:"abbreviations"`+"`"+`
	PrintBlobDigests *bool            `+"`"+`toml:"print-blob_digests"`+"`"+`
}

type abbreviationsV2 struct {
	ZettelIds *bool `+"`"+`toml:"zettel_ids"`+"`"+`
	MarklIds  *bool `+"`"+`toml:"merkle_ids"`+"`"+`
}
`)

	if err := Generate(dir, "v2.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "issue49_test.go", `package issue49

import (
	"testing"
)

func TestEncodeFromScratchPointerToStruct(t *testing.T) {
	// Issue #49: Decode(nil) creates an empty document. Populating a
	// pointer-to-struct field and encoding should produce the sub-table,
	// not silently drop it.
	doc, err := DecodeV2(nil)
	if err != nil {
		t.Fatalf("DecodeV2(nil): %v", err)
	}

	zettelIds := true
	marklIds := true
	printDigests := false
	doc.Data().Abbreviations = &abbreviationsV2{
		ZettelIds: &zettelIds,
		MarklIds:  &marklIds,
	}
	doc.Data().PrintBlobDigests = &printDigests

	encoded, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Round-trip verification
	doc2, err := DecodeV2(encoded)
	if err != nil {
		t.Fatalf("re-decode failed: %v", err)
	}

	d := doc2.Data()
	if d.Abbreviations == nil {
		t.Fatal("Abbreviations lost during encode from scratch")
	}
	if d.Abbreviations.ZettelIds == nil || !*d.Abbreviations.ZettelIds {
		t.Fatal("ZettelIds lost or wrong")
	}
	if d.Abbreviations.MarklIds == nil || !*d.Abbreviations.MarklIds {
		t.Fatal("MarklIds lost or wrong")
	}
	if d.PrintBlobDigests == nil {
		t.Fatal("PrintBlobDigests pointer lost during encode from scratch")
	}
	if *d.PrintBlobDigests != false {
		t.Fatalf("PrintBlobDigests wrong: got %v, want false", *d.PrintBlobDigests)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromScratch", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromScratchPrimitiveAfterStruct(t *testing.T) {
	// Root-level primitive fields placed after a non-pointer struct field
	// should not end up inside the struct's table section.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/primafterstruct",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package primafterstruct

//go:generate tommy generate
type Config struct {
	Defaults Defaults `+"`"+`toml:"defaults"`+"`"+`
	Name     string   `+"`"+`toml:"name"`+"`"+`
	Version  int      `+"`"+`toml:"version"`+"`"+`
}

type Defaults struct {
	Type    string `+"`"+`toml:"type"`+"`"+`
	Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package primafterstruct

import (
	"testing"
)

func TestEncodeFromScratchPrimitiveAfterStruct(t *testing.T) {
	doc, err := DecodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	d := doc.Data()
	d.Defaults = Defaults{Type: "txt", Enabled: true}
	d.Name = "myapp"
	d.Version = 3

	encoded, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(encoded)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}

	d2 := doc2.Data()
	if d2.Name != "myapp" {
		t.Fatalf("Name = %q, want \"myapp\" (may have landed inside [defaults] table)", d2.Name)
	}
	if d2.Version != 3 {
		t.Fatalf("Version = %d, want 3 (may have landed inside [defaults] table)", d2.Version)
	}
	if d2.Defaults.Type != "txt" {
		t.Fatalf("Defaults.Type = %q, want \"txt\"", d2.Defaults.Type)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromScratch", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromScratchPrimitiveAfterSliceStruct(t *testing.T) {
	// Root-level primitive after an array-of-tables (slice of structs).
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/primafterslice",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package primafterslice

//go:generate tommy generate
type Config struct {
	Servers []Server `+"`"+`toml:"servers"`+"`"+`
	Owner   string   `+"`"+`toml:"owner"`+"`"+`
}

type Server struct {
	Name string `+"`"+`toml:"name"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package primafterslice

import (
	"testing"
)

func TestEncodeFromScratchPrimitiveAfterSliceStruct(t *testing.T) {
	doc, err := DecodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	d := doc.Data()
	d.Servers = []Server{{Name: "alpha", Port: 8080}}
	d.Owner = "admin"

	encoded, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(encoded)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}

	if doc2.Data().Owner != "admin" {
		t.Fatalf("Owner = %q, want \"admin\" (may have landed inside [[servers]])", doc2.Data().Owner)
	}
	if len(doc2.Data().Servers) != 1 || doc2.Data().Servers[0].Name != "alpha" {
		t.Fatalf("Servers lost or corrupted: %+v", doc2.Data().Servers)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromScratch", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromScratchMultipleStructsThenPrimitive(t *testing.T) {
	// Two struct fields followed by a primitive — primitive must not land
	// inside the second struct's table.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/multistructprim",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package multistructprim

//go:generate tommy generate
type Config struct {
	Database Database `+"`"+`toml:"database"`+"`"+`
	Logging  *Logging `+"`"+`toml:"logging"`+"`"+`
	Debug    bool     `+"`"+`toml:"debug"`+"`"+`
}

type Database struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}

type Logging struct {
	Level  string `+"`"+`toml:"level"`+"`"+`
	Format string `+"`"+`toml:"format"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package multistructprim

import (
	"testing"
)

func TestEncodeFromScratchMultipleStructsThenPrimitive(t *testing.T) {
	doc, err := DecodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	d := doc.Data()
	d.Database = Database{Host: "localhost", Port: 5432}
	d.Logging = &Logging{Level: "debug", Format: "json"}
	d.Debug = true

	encoded, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(encoded)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}

	d2 := doc2.Data()
	if !d2.Debug {
		t.Fatal("Debug lost (may have landed inside [logging] table)")
	}
	if d2.Database.Host != "localhost" {
		t.Fatalf("Database.Host = %q, want \"localhost\"", d2.Database.Host)
	}
	if d2.Logging == nil || d2.Logging.Level != "debug" {
		t.Fatal("Logging lost or corrupted")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromScratch", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromScratchNestedSubTableOrdering(t *testing.T) {
	// Nested struct inside a struct: inner key-values must not leak into a
	// sub-sub-table section.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/nestedorder",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package nestedorder

//go:generate tommy generate
type Config struct {
	Server Server `+"`"+`toml:"server"`+"`"+`
}

type Server struct {
	TLS  TLS    `+"`"+`toml:"tls"`+"`"+`
	Name string `+"`"+`toml:"name"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}

type TLS struct {
	Cert string `+"`"+`toml:"cert"`+"`"+`
	Key  string `+"`"+`toml:"key"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package nestedorder

import (
	"testing"
)

func TestEncodeFromScratchNestedSubTableOrdering(t *testing.T) {
	doc, err := DecodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	d := doc.Data()
	d.Server = Server{
		TLS:  TLS{Cert: "/path/cert.pem", Key: "/path/key.pem"},
		Name: "prod",
		Port: 443,
	}

	encoded, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(encoded)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}

	d2 := doc2.Data()
	if d2.Server.Name != "prod" {
		t.Fatalf("Server.Name = %q, want \"prod\" (may have landed inside [server.tls])", d2.Server.Name)
	}
	if d2.Server.Port != 443 {
		t.Fatalf("Server.Port = %d, want 443 (may have landed inside [server.tls])", d2.Server.Port)
	}
	if d2.Server.TLS.Cert != "/path/cert.pem" {
		t.Fatalf("Server.TLS.Cert = %q, want \"/path/cert.pem\"", d2.Server.TLS.Cert)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromScratch", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}
