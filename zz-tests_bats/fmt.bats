#! /usr/bin/env bats

setup() {
  load "$(dirname "$BATS_TEST_FILE")/common.bash"
  export output
}

function fmt_normalizes_whitespace_around_equals { # @test
  echo 'key   =   "value"' > "$BATS_TEST_TMPDIR/test.toml"
  run tommy fmt "$BATS_TEST_TMPDIR/test.toml"
  assert_success
  run cat "$BATS_TEST_TMPDIR/test.toml"
  assert_output 'key = "value"'
}

function fmt_check_exits_nonzero_for_unformatted { # @test
  echo 'key   =   "value"' > "$BATS_TEST_TMPDIR/test.toml"
  run tommy fmt --check "$BATS_TEST_TMPDIR/test.toml"
  assert_failure
}

function fmt_check_exits_zero_for_formatted { # @test
  echo 'key = "value"' > "$BATS_TEST_TMPDIR/test.toml"
  run tommy fmt --check "$BATS_TEST_TMPDIR/test.toml"
  assert_success
}

function fmt_preserves_comments { # @test
  cat > "$BATS_TEST_TMPDIR/test.toml" <<'EOF'
# important comment
key = "value"
EOF
  run tommy fmt "$BATS_TEST_TMPDIR/test.toml"
  assert_success
  run cat "$BATS_TEST_TMPDIR/test.toml"
  assert_line --index 0 "# important comment"
  assert_line --index 1 'key = "value"'
}

function fmt_stdin_to_stdout { # @test
  run bash -c 'echo "key   =   \"value\"" | tommy fmt -'
  assert_success
  assert_output 'key = "value"'
}
