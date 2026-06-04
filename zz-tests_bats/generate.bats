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
