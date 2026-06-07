#! /usr/bin/env bats
# bats file_tags=generate

setup() {
  load "$(dirname "$BATS_TEST_FILE")/common.bash"
  export output

  # Scaffold the synthetic downstream module (+ offline vendor in the nix
  # sandbox); shared with encode_wire_format.bats via common.bash.
  setup_tommy_proj

  cat > "$BATS_TEST_TMPDIR/proj/config.go" <<'GOEOF'
package batstest

//go:generate tommy generate
type Config struct {
	Name    string `toml:"name"`
	Port    int    `toml:"port"`
	Enabled bool   `toml:"enabled"`
}
GOEOF
}

function generate_creates_companion_file { # @test
  cd "$BATS_TEST_TMPDIR/proj"
  run go generate ./...
  assert_success
  assert [ -f config_tommy.go ]
}

# With stats-me opt-in (STATSD_* set) generation must still succeed: telemetry is
# fire-and-forget UDP, so a port with nothing listening must not perturb it.
function generate_with_stats_me_enabled_still_succeeds { # @test
  cd "$BATS_TEST_TMPDIR/proj"
  export STATSD_HOST=127.0.0.1
  export STATSD_PORT=18125
  run go generate ./...
  assert_success
  assert [ -f config_tommy.go ]
}

function generate_output_contains_decode_function { # @test
  cd "$BATS_TEST_TMPDIR/proj"
  run go generate ./...
  assert_success
  run grep -c "func DecodeConfig(" config_tommy.go
  assert_output "1"
}

function generate_output_contains_encode_method { # @test
  cd "$BATS_TEST_TMPDIR/proj"
  run go generate ./...
  assert_success
  run grep -c "func (d \*ConfigDocument) Encode" config_tommy.go
  assert_output "1"
}

function generate_output_compiles { # @test
  cd "$BATS_TEST_TMPDIR/proj"
  run go generate ./...
  assert_success
  run go build ./...
  assert_success
}

function generate_round_trip_preserves_comments { # @test
  cd "$BATS_TEST_TMPDIR/proj"
  go generate ./...

  cat > roundtrip_test.go <<'GOEOF'
package batstest

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	input := []byte("# app config\nname = \"myapp\"\nport = 8080\nenabled = true\n")
	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	data := doc.Data()
	data.Port = 9090
	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "# app config") {
		t.Fatal("comment lost")
	}
	if !strings.Contains(s, "9090") {
		t.Fatal("modified value missing")
	}
	if strings.Contains(s, "8080") {
		t.Fatal("old value still present")
	}
}
GOEOF

  run go test -v ./...
  assert_success
  assert_output --partial "PASS"
}

function generate_zero_value_not_appended { # @test
  cd "$BATS_TEST_TMPDIR/proj"
  go generate ./...

  cat > zeroval_test.go <<'GOEOF'
package batstest

import "testing"

func TestZeroValueSkip(t *testing.T) {
	input := []byte("name = \"app\"\nport = 8080\n")
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
GOEOF

  run go test -v -run TestZeroValueSkip ./...
  assert_success
  assert_output --partial "PASS"
}

function generate_is_idempotent { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  go generate ./...
  cp config_tommy.go "$BATS_TEST_TMPDIR/first.go"

  go generate ./...
  run diff "$BATS_TEST_TMPDIR/first.go" config_tommy.go
  assert_success
}

# Regression for #106: a map[string]string field written as an inline table
# (`k = { ... }`) must decode and be marked consumed, exactly like the
# sub-table form (`[parent.k]`). Covers the top-level map and a map nested
# under a parent struct (the spinclass [direnv.dotenv] case).
function generate_inline_table_map_string_string_decodes { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat > config.go <<'GOEOF'
package batstest

//go:generate tommy generate
type Config struct {
	Env    map[string]string `toml:"env"`
	Direnv *Direnv           `toml:"direnv"`
}

type Direnv struct {
	Dotenv map[string]string `toml:"dotenv"`
}
GOEOF

  run go generate ./...
  assert_success

  cat > inline_test.go <<'GOEOF'
package batstest

import "testing"

func TestInlineTopLevel(t *testing.T) {
	doc, err := DecodeConfig([]byte("env = { FOO = \"bar\" }\n"))
	if err != nil { t.Fatal(err) }
	if doc.Data().Env["FOO"] != "bar" { t.Fatalf("Env[FOO]=%q", doc.Data().Env["FOO"]) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

func TestInlineNested(t *testing.T) {
	doc, err := DecodeConfig([]byte("[direnv]\ndotenv = { FOO = \"bar\" }\n"))
	if err != nil { t.Fatal(err) }
	if doc.Data().Direnv == nil || doc.Data().Direnv.Dotenv["FOO"] != "bar" {
		t.Fatalf("Direnv=%+v", doc.Data().Direnv)
	}
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
GOEOF

  run go test -v ./...
  assert_success
  assert_output --partial "PASS"
}

# #108 axis 1: a nested struct / *struct field written as an inline table
# (`inner = { name = "a" }`) must decode and be consumed, like the sub-table form.
function generate_inline_table_struct_decodes { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat > config.go <<'GOEOF'
package batstest

//go:generate tommy generate
type Config struct {
	Val Inner  `toml:"val"`
	Ptr *Inner `toml:"ptr"`
}

type Inner struct {
	Name string `toml:"name"`
	Port int    `toml:"port"`
}
GOEOF

  run go generate ./...
  assert_success

  cat > inline_test.go <<'GOEOF'
package batstest

import "testing"

func TestInlineValueStruct(t *testing.T) {
	doc, err := DecodeConfig([]byte("val = { name = \"a\", port = 8080 }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Val.Name != "a" || d.Val.Port != 8080 { t.Fatalf("Val=%+v", d.Val) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

func TestInlinePtrStruct(t *testing.T) {
	doc, err := DecodeConfig([]byte("ptr = { name = \"b\", port = 90 }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Ptr == nil || d.Ptr.Name != "b" { t.Fatalf("Ptr=%+v", d.Ptr) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
GOEOF

  run go test -v ./...
  assert_success
  assert_output --partial "PASS"
}

# #108 axis 1: a map[string]struct field written as a nested inline table
# (`actions = { build = { command = "make" } }`) must decode and be consumed.
function generate_inline_table_map_struct_decodes { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat > config.go <<'GOEOF'
package batstest

//go:generate tommy generate
type Config struct {
	Actions map[string]ActionSpec `toml:"actions"`
}

type ActionSpec struct {
	Command string `toml:"command"`
	Timeout int    `toml:"timeout"`
}
GOEOF

  run go generate ./...
  assert_success

  cat > inline_test.go <<'GOEOF'
package batstest

import "testing"

func TestInlineMapStruct(t *testing.T) {
	doc, err := DecodeConfig([]byte("actions = { build = { command = \"make\", timeout = 30 } }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Actions["build"].Command != "make" || d.Actions["build"].Timeout != 30 { t.Fatalf("build=%+v", d.Actions["build"]) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
GOEOF

  run go test -v ./...
  assert_success
  assert_output --partial "PASS"
}

# #108 axis 1: a map[string]NamedMap field written as a nested inline table
# (`groups = { editors = { vim = "x" } }`) must decode and be consumed.
function generate_inline_table_map_map_decodes { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat > config.go <<'GOEOF'
package batstest

type Group map[string]string

//go:generate tommy generate
type Config struct {
	Groups map[string]Group `toml:"groups"`
}
GOEOF

  run go generate ./...
  assert_success

  cat > inline_test.go <<'GOEOF'
package batstest

import "testing"

func TestInlineMapMap(t *testing.T) {
	doc, err := DecodeConfig([]byte("groups = { editors = { vim = \"text/plain\" } }\n"))
	if err != nil { t.Fatal(err) }
	if doc.Data().Groups["editors"]["vim"] != "text/plain" { t.Fatalf("groups=%+v", doc.Data().Groups) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
GOEOF

  run go test -v ./...
  assert_success
  assert_output --partial "PASS"
}

# #108 axis 1: a FULLY-inline nested struct (`val = { name = "a", inner = { x = 5 } }`)
# must decode the nested struct field too — the inline body is decoded
# scope-relative so deeper inline tables resolve within it.
function generate_inline_table_nested_struct_decodes { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat > config.go <<'GOEOF'
package batstest

//go:generate tommy generate
type Config struct {
	Val Outer `toml:"val"`
}

type Outer struct {
	Name  string `toml:"name"`
	Inner Inner  `toml:"inner"`
}

type Inner struct {
	X int `toml:"x"`
}
GOEOF

  run go generate ./...
  assert_success

  cat > inline_test.go <<'GOEOF'
package batstest

import "testing"

func TestInlineNestedStruct(t *testing.T) {
	doc, err := DecodeConfig([]byte("val = { name = \"a\", inner = { x = 5 } }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Val.Name != "a" || d.Val.Inner.X != 5 { t.Fatalf("Val=%+v", d.Val) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
GOEOF

  run go test -v ./...
  assert_success
  assert_output --partial "PASS"
}

# Regression for #115: the "header-outer + inline-inner" spelling. A child field
# written as an inline table (`session = { ... }`) under the parent's own bare
# [exec] header is a NodeKeyValue child of the [exec] node, not the document
# root. The root-relative container renderers ran their inline fallback against
# the document root, so every inline-inner child was silently dropped. Covers a
# *struct (compNilGuard), a struct (compInTable), a map[string]struct
# (compMapStruct) and a map[string]NamedMap (compMapMap) in one shape.
function generate_inline_inner_under_header_decodes { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat > config.go <<'GOEOF'
package batstest

type Labels map[string]string

//go:generate tommy generate
type Config struct {
	Exec *Exec `toml:"exec"`
}

type Exec struct {
	Session *Session          `toml:"session"`
	Window  Window            `toml:"window"`
	Actions map[string]Action `toml:"actions"`
	Groups  map[string]Labels `toml:"groups"`
}

type Session struct {
	Name string `toml:"name"`
}

type Window struct {
	Title string `toml:"title"`
}

type Action struct {
	Command string `toml:"command"`
}
GOEOF

  run go generate ./...
  assert_success

  cat > inline_test.go <<'GOEOF'
package batstest

import "testing"

func TestInlineInnerUnderHeader(t *testing.T) {
	in := "[exec]\n" +
		"session = { name = \"sess\" }\n" +
		"window = { title = \"win\" }\n" +
		"actions = { build = { command = \"make\" } }\n" +
		"groups = { editors = { vim = \"on\" } }\n"
	doc, err := DecodeConfig([]byte(in))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Exec == nil || d.Exec.Session == nil || d.Exec.Session.Name != "sess" { t.Fatalf("Session=%+v", d.Exec) }
	if d.Exec.Window.Title != "win" { t.Fatalf("Window=%+v", d.Exec.Window) }
	if d.Exec.Actions["build"].Command != "make" { t.Fatalf("Actions=%+v", d.Exec.Actions) }
	if d.Exec.Groups["editors"]["vim"] != "on" { t.Fatalf("Groups=%+v", d.Exec.Groups) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
GOEOF

  run go test -v ./...
  assert_success
  assert_output --partial "PASS"
}

# Regression for #110: a key defined twice in the inline-table spelling
# (`sess = { ... }` twice, or a duplicate inner inline key) must be rejected
# like the header form (`[sess]` twice, #92) — the generated decoder runs
# cst.CheckNoDuplicateKeys after parse. Before this the second inline key was
# silently dropped.
function generate_duplicate_inline_key_rejected { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  cat > config.go <<'GOEOF'
package batstest

//go:generate tommy generate
type Config struct {
	Sess *Sess             `toml:"sess"`
	Env  map[string]string `toml:"env"`
}

type Sess struct {
	Name string `toml:"name"`
}
GOEOF

  run go generate ./...
  assert_success

  cat > inline_test.go <<'GOEOF'
package batstest

import (
	"strings"
	"testing"
)

func TestDuplicateInlineKeyRejected(t *testing.T) {
	if _, err := DecodeConfig([]byte("sess = { name = \"a\" }\nsess = { name = \"b\" }\n")); err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("outer: want duplicate key error, got %v", err)
	}
	if _, err := DecodeConfig([]byte("env = { FOO = \"a\", FOO = \"b\" }\n")); err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("inner: want duplicate key error, got %v", err)
	}
	doc, err := DecodeConfig([]byte("sess = { name = \"a\" }\nenv = { FOO = \"x\" }\n"))
	if err != nil { t.Fatal(err) }
	if doc.Data().Sess.Name != "a" || doc.Data().Env["FOO"] != "x" { t.Fatalf("data=%+v", doc.Data()) }
}
GOEOF

  run go test -v ./...
  assert_success
  assert_output --partial "PASS"
}
