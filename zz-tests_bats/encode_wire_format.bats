#! /usr/bin/env bats
# bats file_tags=generate
#
# Wire-format regression coverage that round-trip tests structurally miss.
# Carries the `generate` tag so it runs under every codegen backend via the
# bats.nix matrix (jen/api/cst/legacy) — backend divergence in emission fails
# CI here. See #82, #83.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/common.bash"
  export output
  setup_tommy_proj
}

# Regression for #82 under the faithful nil/empty contract (#21): an EXPLICIT
# empty (non-nil) slice without omitempty must serialize as `key = []` (not be
# dropped), while a nil/absent slice is OMITTED — the two are distinct. Covers
# both the primitive-slice and TextMarshaler-slice encode paths (madder's
# `Encryption []markl.Id` is the latter).
function empty_non_omitempty_slices_emit_brackets { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat >config.go <<'GOEOF'
package batstest

//go:generate tommy generate
type Config struct {
	Name  string   `toml:"name"`
	Tags  []string `toml:"tags"`
	Marks []Mark   `toml:"marks"`
}

type Mark struct{ v string }

func (m Mark) MarshalText() ([]byte, error)  { return []byte(m.v), nil }
func (m *Mark) UnmarshalText(b []byte) error { m.v = string(b); return nil }
GOEOF

  run go generate ./...
  assert_success

  cat >wire_test.go <<'GOEOF'
package batstest

import (
	"strings"
	"testing"
)

func TestEmptyNonOmitemptySlicesEmitted(t *testing.T) {
	// An EXPLICIT empty (non-nil) slice must emit "key = []" (not be dropped).
	doc, err := DecodeConfig([]byte("name = \"app\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	doc.Data().Tags = []string{}
	doc.Data().Marks = []Mark{}
	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "tags = []") {
		t.Fatalf("primitive slice: want \"tags = []\" in output, got:\n%s", out)
	}
	if !strings.Contains(string(out), "marks = []") {
		t.Fatalf("text-marshaler slice: want \"marks = []\" in output, got:\n%s", out)
	}

	// A nil/absent slice, by contrast, is omitted.
	doc2, err := DecodeConfig([]byte("name = \"app\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	out2, err := doc2.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out2), "tags") || strings.Contains(string(out2), "marks") {
		t.Fatalf("nil slices should be omitted, got:\n%s", out2)
	}
}
GOEOF

  run go test -run TestEmptyNonOmitemptySlicesEmitted ./...
  assert_success
  assert_output --partial "ok"
}

# Regression for #89: a struct whose fields are ALL array-of-tables must not emit
# a bare parent [table] header (the array-table headers already imply it), and
# the headerless output must still round-trip — exercising the non-pointer
# struct's flat-key decode fallback.
function all_array_field_struct_omits_parent_table { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat >config.go <<'GOEOF'
package batstest

//go:generate tommy generate
type Outer struct {
	Section Section `toml:"section"`
}

type Section struct {
	Items []Item `toml:"items"`
}

type Item struct {
	Name string `toml:"name"`
}
GOEOF

  run go generate ./...
  assert_success

  cat >wire_test.go <<'GOEOF'
package batstest

import (
	"reflect"
	"strings"
	"testing"
)

func TestNoSpuriousSectionHeader(t *testing.T) {
	d, err := DecodeOuter([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	*d.Data() = Outer{Section: Section{Items: nil}}
	out, err := d.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "[section]") {
		t.Fatalf("empty all-array struct emitted a bare [section]:\n%q", string(out))
	}

	d2, err := DecodeOuter([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	want := Outer{Section: Section{Items: []Item{{Name: "a"}, {Name: "b"}}}}
	*d2.Data() = want
	out2, err := d2.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out2), "[section]") {
		t.Fatalf("non-empty all-array struct emitted a bare [section]:\n%s", out2)
	}
	d3, err := DecodeOuter(out2)
	if err != nil {
		t.Fatalf("re-decode: %v\n%s", err, out2)
	}
	if !reflect.DeepEqual(*d3.Data(), want) {
		t.Fatalf("round-trip mismatch\nwant %#v\ngot  %#v\n%s", want, *d3.Data(), out2)
	}
}
GOEOF

  run go test -run TestNoSpuriousSectionHeader ./...
  assert_success
  assert_output --partial "ok"
}

# Regression for #103: a map[string]Struct key that needs TOML quoting (here a
# dot) must serialize its sub-table header quoted ([servers."a.b"]) and decode
# back as a single map key, not nest as servers→a→b.
function map_struct_quoted_key_roundtrips { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat >config.go <<'GOEOF'
package batstest

//go:generate tommy generate
type Config struct {
	Servers map[string]Server `toml:"servers"`
}

type Server struct {
	Host string `toml:"host"`
}
GOEOF

  run go generate ./...
  assert_success

  cat >wire_test.go <<'GOEOF'
package batstest

import (
	"strings"
	"testing"
)

func TestMapStructQuotedKey(t *testing.T) {
	doc, err := DecodeConfig([]byte("[servers.\"a.b\"]\nhost = \"h1\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Data().Servers["a.b"]; got.Host != "h1" {
		t.Fatalf("servers[\"a.b\"] = %+v, want host=h1", got)
	}
	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "[servers.\"a.b\"]") {
		t.Fatalf("want quoted header [servers.\"a.b\"] in output, got:\n%s", out)
	}
}
GOEOF

  run go test -run TestMapStructQuotedKey ./...
  assert_success
  assert_output --partial "ok"
}
