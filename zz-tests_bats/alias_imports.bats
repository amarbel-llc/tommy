#! /usr/bin/env bats
# bats file_tags=generate
#
# Codegen import-path coverage for aliases re-exported over internal/ packages
# (#81). Carries the `generate` tag so it runs under every codegen backend via
# the bats.nix matrix. See #81, #83.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/common.bash"
  export output
  setup_tommy_proj
}

# Regression for #81: a field whose type is an alias re-exported from a public
# facade (pkgs/values) over an internal/ package must generate code importing
# the FACADE, not the underlying internal definition. A single module suffices
# to assert on the emitted import line — the consumer's own pkgs/ and internal/
# subpackages resolve locally, so only tommy needs vendoring. (Cross-module
# compile-illegality is covered by the Go suite; here we assert the import.)
function alias_field_imports_facade_not_internal { # @test
  cd "$BATS_TEST_TMPDIR/proj"

  mkdir -p internal/charlie/values pkgs/values

  cat >internal/charlie/values/values.go <<'GOEOF'
package values

type IntSlice []int
GOEOF

  cat >pkgs/values/main.go <<'GOEOF'
package values

import internal "example.com/batstest/internal/charlie/values"

type IntSlice = internal.IntSlice
GOEOF

  cat >config.go <<'GOEOF'
package batstest

import "example.com/batstest/pkgs/values"

//go:generate tommy generate
type Config struct {
	HashBuckets values.IntSlice `toml:"hash-buckets"`
}
GOEOF

  run go generate ./...
  assert_success

  run cat config_tommy.go
  assert_success
  assert_output --partial 'example.com/batstest/pkgs/values'
  refute_output --partial 'internal/charlie/values'
}
