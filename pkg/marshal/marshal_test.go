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
