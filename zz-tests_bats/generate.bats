#! /usr/bin/env bats
# bats file_tags=generate

setup() {
  load "$(dirname "$BATS_TEST_FILE")/common.bash"
  export output

  # Point Go caches into the sandbox so batman doesn't block them.
  export GOPATH="$BATS_TEST_TMPDIR/gopath"
  export GOCACHE="$BATS_TEST_TMPDIR/gocache"
  export GOMODCACHE="$BATS_TEST_TMPDIR/gomodcache"

  # Scaffold a minimal Go package with a tommy-annotated struct.
  mkdir -p "$BATS_TEST_TMPDIR/proj"

  # In the nix sandbox, TOMMY_FIXTURE_DIR points at a staged tommy
  # source tree with a populated vendor/ (set by bats.nix as an
  # absolute /nix/store path). Locally, fall back to the live worktree.
  if [[ -n ${TOMMY_FIXTURE_DIR:-} ]]; then
    repo_root="$TOMMY_FIXTURE_DIR"
  else
    repo_root="$(cd "$(dirname "$BATS_TEST_FILE")/.." && pwd)"
  fi

  cat > "$BATS_TEST_TMPDIR/proj/go.mod" <<EOF
module example.com/batstest

go 1.26

require github.com/amarbel-llc/tommy v0.0.0

replace github.com/amarbel-llc/tommy => $repo_root
EOF

  cat > "$BATS_TEST_TMPDIR/proj/config.go" <<'GOEOF'
package batstest

//go:generate tommy generate
type Config struct {
	Name    string `toml:"name"`
	Port    int    `toml:"port"`
	Enabled bool   `toml:"enabled"`
}
GOEOF

  # Offline build path (nix sandbox): build the synthetic module's
  # vendor/ from the staged tommy fixture so `go build`/`go test`
  # resolve everything from disk under GOFLAGS=-mod=vendor (exported
  # by bats.nix). Vendor mode demands tommy ITSELF live in vendor/
  # under its import path — `replace` directives don't help once
  # -mod=vendor is set. Harmless to skip locally — the synthetic
  # module then falls back to network mode, which is what
  # `just test-bats` already does.
  if [[ -n ${TOMMY_FIXTURE_DIR:-} ]]; then
    proj_vendor="$BATS_TEST_TMPDIR/proj/vendor"
    cp -rL "$repo_root/vendor" "$proj_vendor"
    # Store-path copies inherit read-only mode; restore write access
    # so we can extend the tree below and so bats can clean up later.
    chmod -R u+w "$proj_vendor"
    # Copy tommy itself into vendor at its module path. `-mod=vendor`
    # requires the literal package path to exist in vendor/ — `replace`
    # directives don't apply once vendor mode is active.
    tommy_vendor_path="$proj_vendor/github.com/amarbel-llc/tommy"
    mkdir -p "$tommy_vendor_path"
    cp -L "$repo_root/go.mod" "$repo_root/go.sum" "$tommy_vendor_path/"
    for d in cmd generate internal pkg; do
      [ -d "$repo_root/$d" ] && cp -rL "$repo_root/$d" "$tommy_vendor_path/$d"
    done
    chmod -R u+w "$tommy_vendor_path"
  fi
}

function generate_creates_companion_file { # @test
  cd "$BATS_TEST_TMPDIR/proj"
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
