package marshal

import (
	"testing"
)

type Config struct {
	Name    string `toml:"name"`
	Port    int    `toml:"port"`
	Enabled bool   `toml:"enabled"`
}

type StorageConfig struct {
	HashBuckets []int  `toml:"hash_buckets"`
	BasePath    string `toml:"base_path"`
}

type FullConfig struct {
	Storage StorageConfig `toml:"storage"`
}

type AllTypesConfig struct {
	Name    string   `toml:"name"`
	Port    int      `toml:"port"`
	BigNum  int64    `toml:"big_num"`
	Rate    float64  `toml:"rate"`
	Enabled bool     `toml:"enabled"`
	Buckets []int    `toml:"buckets"`
	Tags    []string `toml:"tags"`
}

func TestRoundTripPreservesComments(t *testing.T) {
	input := []byte("# Server config\nname = \"myapp\"  # the app name\nport = 8080\nenabled = true\n")
	var cfg Config
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "myapp" {
		t.Fatalf("expected Name %q, got %q", "myapp", cfg.Name)
	}
	if cfg.Port != 8080 {
		t.Fatalf("expected Port 8080, got %d", cfg.Port)
	}
	if cfg.Enabled != true {
		t.Fatalf("expected Enabled true, got %v", cfg.Enabled)
	}

	cfg.Port = 9090
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := "# Server config\nname = \"myapp\"  # the app name\nport = 9090\nenabled = true\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestRoundTripNestedTable(t *testing.T) {
	input := []byte("[storage]\n# Hash bucket sizes\nhash_buckets = [2, 4]\nbase_path = \"/data\"  # data directory\n")
	var cfg FullConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Storage.HashBuckets) != 2 || cfg.Storage.HashBuckets[0] != 2 || cfg.Storage.HashBuckets[1] != 4 {
		t.Fatalf("expected HashBuckets [2, 4], got %v", cfg.Storage.HashBuckets)
	}
	if cfg.Storage.BasePath != "/data" {
		t.Fatalf("expected BasePath %q, got %q", "/data", cfg.Storage.BasePath)
	}

	cfg.Storage.HashBuckets = []int{2, 4, 8}
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := "[storage]\n# Hash bucket sizes\nhash_buckets = [2, 4, 8]\nbase_path = \"/data\"  # data directory\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestUnmarshalDecodesAllTypes(t *testing.T) {
	input := []byte("name = \"test\"\nport = 42\nbig_num = 9999999999\nrate = 3.14\nenabled = true\nbuckets = [1, 2, 3]\ntags = [\"a\", \"b\"]\n")
	var cfg AllTypesConfig
	_, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name %q, got %q", "test", cfg.Name)
	}
	if cfg.Port != 42 {
		t.Fatalf("expected Port 42, got %d", cfg.Port)
	}
	if cfg.BigNum != 9999999999 {
		t.Fatalf("expected BigNum 9999999999, got %d", cfg.BigNum)
	}
	if cfg.Rate != 3.14 {
		t.Fatalf("expected Rate 3.14, got %f", cfg.Rate)
	}
	if cfg.Enabled != true {
		t.Fatalf("expected Enabled true, got %v", cfg.Enabled)
	}
	expectedBuckets := []int{1, 2, 3}
	if len(cfg.Buckets) != len(expectedBuckets) {
		t.Fatalf("expected Buckets %v, got %v", expectedBuckets, cfg.Buckets)
	}
	for i := range expectedBuckets {
		if cfg.Buckets[i] != expectedBuckets[i] {
			t.Fatalf("expected Buckets %v, got %v", expectedBuckets, cfg.Buckets)
		}
	}
	expectedTags := []string{"a", "b"}
	if len(cfg.Tags) != len(expectedTags) {
		t.Fatalf("expected Tags %v, got %v", expectedTags, cfg.Tags)
	}
	for i := range expectedTags {
		if cfg.Tags[i] != expectedTags[i] {
			t.Fatalf("expected Tags %v, got %v", expectedTags, cfg.Tags)
		}
	}
}

func TestMarshalNoChangesPreservesExactly(t *testing.T) {
	input := []byte("# A comment\nname = \"test\"  # inline comment\nport = 8080\nenabled = false\n")
	var cfg Config
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Fatalf("expected byte-for-byte identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestUnmarshalRequiresPointer(t *testing.T) {
	input := []byte("name = \"test\"\n")
	var cfg Config
	_, err := UnmarshalDocument(input, cfg)
	if err == nil {
		t.Fatal("expected error when passing non-pointer")
	}
}

type Server struct {
	Name    string `toml:"name"`
	Command string `toml:"command"`
}

type ServerWithOptional struct {
	Name         string `toml:"name"`
	Command      string `toml:"command"`
	ReadOnlyHint bool   `toml:"readOnlyHint"`
}

type ServersOptionalConfig struct {
	Servers []ServerWithOptional `toml:"servers"`
}

type ServersConfig struct {
	Title   string   `toml:"title"`
	Servers []Server `toml:"servers"`
}

func TestUnmarshalArrayOfTables(t *testing.T) {
	input := []byte("title = \"config\"\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n")
	var cfg ServersConfig
	_, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Title != "config" {
		t.Fatalf("expected Title %q, got %q", "config", cfg.Title)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Name != "grit" {
		t.Fatalf("expected Servers[0].Name %q, got %q", "grit", cfg.Servers[0].Name)
	}
	if cfg.Servers[0].Command != "grit mcp" {
		t.Fatalf("expected Servers[0].Command %q, got %q", "grit mcp", cfg.Servers[0].Command)
	}
	if cfg.Servers[1].Name != "lux" {
		t.Fatalf("expected Servers[1].Name %q, got %q", "lux", cfg.Servers[1].Name)
	}
	if cfg.Servers[1].Command != "lux serve" {
		t.Fatalf("expected Servers[1].Command %q, got %q", "lux serve", cfg.Servers[1].Command)
	}
}

func TestRoundTripArrayOfTables(t *testing.T) {
	input := []byte("# my servers\ntitle = \"config\"\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n")
	var cfg ServersConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Modify existing entry
	cfg.Servers[1].Command = "lux mcp"
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := "# my servers\ntitle = \"config\"\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux mcp\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestRoundTripArrayOfTablesAppend(t *testing.T) {
	input := []byte("# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")
	var cfg ServersConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	cfg.Servers = append(cfg.Servers, Server{Name: "lux", Command: "lux serve"})
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := "# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestRoundTripArrayOfTablesRemove(t *testing.T) {
	input := []byte("[[servers]]\nname = \"a\"\ncommand = \"a\"\n\n[[servers]]\nname = \"b\"\ncommand = \"b\"\n\n[[servers]]\nname = \"c\"\ncommand = \"c\"\n")
	var cfg ServersConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Remove middle entry
	cfg.Servers = []Server{cfg.Servers[0], cfg.Servers[2]}
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := "[[servers]]\nname = \"a\"\ncommand = \"a\"\n\n[[servers]]\nname = \"c\"\ncommand = \"c\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestRoundTripArrayOfTablesNoChanges(t *testing.T) {
	input := []byte("# preserved\n\n[[servers]]\nname = \"grit\"  # inline\ncommand = \"grit mcp\"\n")
	var cfg ServersConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Fatalf("expected byte-for-byte identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestUnmarshalArrayOfTablesSkipsMissingKeys(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")
	var cfg ServersOptionalConfig
	_, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Name != "grit" {
		t.Fatalf("expected Name %q, got %q", "grit", cfg.Servers[0].Name)
	}
	if cfg.Servers[0].Command != "grit mcp" {
		t.Fatalf("expected Command %q, got %q", "grit mcp", cfg.Servers[0].Command)
	}
	if cfg.Servers[0].ReadOnlyHint != false {
		t.Fatalf("expected ReadOnlyHint false, got %v", cfg.Servers[0].ReadOnlyHint)
	}
}

type HooksConfig struct {
	Hooks struct {
		Create string `toml:"create"`
	} `toml:"hooks"`
}

func TestUnmarshalMultilineBasicString(t *testing.T) {
	input := []byte("[hooks]\ncreate = \"\"\"\necho hello\n\necho world\n\"\"\"\n")
	var cfg HooksConfig
	_, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := "echo hello\n\necho world\n"
	if cfg.Hooks.Create != expected {
		t.Fatalf("expected %q, got %q", expected, cfg.Hooks.Create)
	}
}

func TestRoundTripMultilineBasicString(t *testing.T) {
	input := []byte("[hooks]\ncreate = \"\"\"\necho hello\n\necho world\n\"\"\"\n")
	var cfg HooksConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Fatalf("round-trip failed.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestMarshalNestedNoChangesPreserves(t *testing.T) {
	input := []byte("[storage]\n# comment\nhash_buckets = [2, 4]\nbase_path = \"/data\"  # path\n")
	var cfg FullConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Fatalf("expected byte-for-byte identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

// Migrating decode onto cst.Decompose (ADR 2026-06-07) makes the reflection
// unmarshal accept every TOML spelling, not just the canonical one.
type SpellConfig struct {
	Storage StorageConfig `toml:"storage"`
	Servers []SpellServer `toml:"servers"`
}

type SpellServer struct {
	Name string `toml:"name"`
	Port int    `toml:"port"`
}

func TestUnmarshalAcceptsAllSpellings(t *testing.T) {
	cases := map[string]string{
		"sub-table":       "[storage]\nbase_path = \"/data\"\nhash_buckets = [1, 2]\n",
		"inline-table":    "storage = { base_path = \"/data\", hash_buckets = [1, 2] }\n",
		"dotted-key":      "storage.base_path = \"/data\"\nstorage.hash_buckets = [1, 2]\n",
		"array-of-tables": "[[servers]]\nname = \"a\"\nport = 1\n[[servers]]\nname = \"b\"\nport = 2\n",
		"inline-array":    "servers = [ { name = \"a\", port = 1 }, { name = \"b\", port = 2 } ]\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			var cfg SpellConfig
			if _, err := UnmarshalDocument([]byte(input), &cfg); err != nil {
				t.Fatalf("UnmarshalDocument: %v", err)
			}
			switch name {
			case "array-of-tables", "inline-array":
				if len(cfg.Servers) != 2 || cfg.Servers[0].Name != "a" || cfg.Servers[1].Port != 2 {
					t.Fatalf("servers=%+v", cfg.Servers)
				}
			default:
				if cfg.Storage.BasePath != "/data" || len(cfg.Storage.HashBuckets) != 2 || cfg.Storage.HashBuckets[1] != 2 {
					t.Fatalf("storage=%+v", cfg.Storage)
				}
			}
		})
	}
}

func TestUnmarshalRejectsDuplicateKeys(t *testing.T) {
	var cfg Config
	if _, err := UnmarshalDocument([]byte("name = \"a\"\nname = \"b\"\n"), &cfg); err == nil {
		t.Fatal("expected duplicate-key error")
	}
}

// An explicit empty array `servers = []` for a []struct field decodes to an
// empty (non-nil) slice without error: Decompose keeps the empty array a leaf
// (it can't tell an empty array-of-tables from an empty scalar array), so the
// struct-slice decoder must accept the empty-array leaf rather than reject it
// as a non-array-of-tables (#94, was a regression vs the CST-based decoder).
func TestUnmarshalEmptyStructSlice(t *testing.T) {
	var cfg ServersConfig
	if _, err := UnmarshalDocument([]byte("title = \"config\"\nservers = []\n"), &cfg); err != nil {
		t.Fatalf("UnmarshalDocument: %v", err)
	}
	if cfg.Servers == nil {
		t.Fatal("Servers = nil, want empty non-nil slice")
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("len(Servers) = %d, want 0", len(cfg.Servers))
	}
}
