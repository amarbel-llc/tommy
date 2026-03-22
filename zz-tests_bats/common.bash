bats_load_library bats-support
bats_load_library bats-assert
bats_load_library bats-assert-additions
bats_load_library bats-island
bats_load_library bats-emo

require_bin TOMMY_BIN tommy

# Put tommy's directory on PATH so `go generate` finds it.
if [[ -n ${TOMMY_BIN:-} ]]; then
  export PATH="$(dirname "$(realpath "$TOMMY_BIN")"):$PATH"
fi
