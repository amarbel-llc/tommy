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

# setup_tommy_proj scaffolds a synthetic downstream Go module under
# $BATS_TEST_TMPDIR/proj that replaces tommy with the staged fixture (nix
# sandbox) or the live worktree (local). In the sandbox it also vendors tommy
# + its deps so `go build`/`go test` resolve everything offline under
# GOFLAGS=-mod=vendor (exported by bats.nix). The caller writes config.go and
# any *_test.go, then runs `go generate`/`go test` from $BATS_TEST_TMPDIR/proj.
#
# Shared by generate.bats and encode_wire_format.bats — the offline-vendor
# dance is fiddly and must not drift between callers.
setup_tommy_proj() {
  # Point Go caches into the sandbox so batman doesn't block them.
  export GOPATH="$BATS_TEST_TMPDIR/gopath"
  export GOCACHE="$BATS_TEST_TMPDIR/gocache"
  export GOMODCACHE="$BATS_TEST_TMPDIR/gomodcache"

  mkdir -p "$BATS_TEST_TMPDIR/proj"

  # TOMMY_FIXTURE_DIR (set by bats.nix) points at a staged tommy source tree
  # with a populated vendor/. Locally, fall back to the live worktree.
  local repo_root
  if [[ -n ${TOMMY_FIXTURE_DIR:-} ]]; then
    repo_root="$TOMMY_FIXTURE_DIR"
  else
    repo_root="$(cd "$(dirname "$BATS_TEST_FILE")/.." && pwd)"
  fi

  cat >"$BATS_TEST_TMPDIR/proj/go.mod" <<EOF
module example.com/batstest

go 1.26

require github.com/amarbel-llc/tommy v0.0.0

replace github.com/amarbel-llc/tommy => $repo_root
EOF

  # Offline build path (nix sandbox): build the synthetic module's vendor/
  # from the staged tommy fixture. Vendor mode demands tommy ITSELF live in
  # vendor/ under its import path — `replace` directives don't help once
  # -mod=vendor is set. Harmless to skip locally (falls back to network mode).
  if [[ -n ${TOMMY_FIXTURE_DIR:-} ]]; then
    local proj_vendor="$BATS_TEST_TMPDIR/proj/vendor"
    cp -rL "$repo_root/vendor" "$proj_vendor"
    chmod -R u+w "$proj_vendor"
    local tommy_vendor_path="$proj_vendor/github.com/amarbel-llc/tommy"
    mkdir -p "$tommy_vendor_path"
    cp -L "$repo_root/go.mod" "$repo_root/go.sum" "$tommy_vendor_path/"
    local d
    for d in cmd generate internal pkg; do
      [ -d "$repo_root/$d" ] && cp -rL "$repo_root/$d" "$tommy_vendor_path/$d"
    done
    chmod -R u+w "$tommy_vendor_path"
  fi
}
