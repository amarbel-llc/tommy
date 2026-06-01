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

# Regression for #82: a slice field WITHOUT omitempty must serialize even when
# empty, as `key = []`. Round-trip stays byte-identical (empty -> omitted ->
# decodes back to empty), so only an explicit emission assertion catches the
# drop. Covers both the primitive-slice and TextMarshaler-slice encode paths
# (madder's `Encryption []markl.Id` is the latter).
function empty_non_omitempty_slices_emit_brackets { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat > config.go <<'GOEOF'
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

  cat > wire_test.go <<'GOEOF'
package batstest

import (
	"strings"
	"testing"
)

func TestEmptyNonOmitemptySlicesEmitted(t *testing.T) {
	// A document that never carried the keys must still emit them: neither
	// field has omitempty.
	doc, err := DecodeConfig([]byte("name = \"app\"\n"))
	if err != nil {
		t.Fatal(err)
	}
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
}
GOEOF

  run go test -run TestEmptyNonOmitemptySlicesEmitted ./...
  assert_success
  assert_output --partial "ok"
}
