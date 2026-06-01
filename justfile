default: validate build test

# === aggregates ===

validate: validate-nix

build: build-nix

test: test-bats-nix test-go-generate-nix

clean: clean-go-cache clean-go-modcache

# === pre-build ===

# Schema-validate the flake and build every checks.${system}.* output.
[group('pre-build')]
validate-nix:
  # Forces tommyBin to build (its checkPhase runs the Go library
  # tests against the filtered go-pkgs-test artifact) and then builds
  # the bats lanes on top. --max-jobs 1 serializes lane builds: the
  # backend matrix's go-compiling bats tests (`go generate`/`go test`)
  # contend for CPU when several lanes build at once and blow the
  # per-test timeout. Sequential lanes each get full CPU and finish
  # fast. Time-over-throughput is fine here. See #83.
  nix flake check --keep-going --show-trace --print-build-logs --max-jobs 1

# === build ===

[group('build')]
build-nix:
  nix build --show-trace --print-build-logs

# === post-build ===

# Bats end-to-end tests in the nix sandbox.
[group('post-build')]
test-bats-nix:
  # Generator coverage lives here exclusively — the Go-side
  # ./generate/... tests scaffold synthetic modules and need
  # go/packages.Load network the nix sandbox can't provide.
  nix build .#bats-default --no-link --print-build-logs

# Filter to a single tagged lane (e.g. `just test-bats-nix-tag fmt`).
[group('post-build')]
test-bats-nix-tag tag:
  nix build .#bats-{{tag}} --no-link --print-build-logs

# Run the Go ./generate integration suite offline in the nix sandbox. The
# synthetic modules resolve from a pinned module cache with no network — the
# deep Go-level generator coverage (the bats lanes cover the end-to-end CLI).
# See #83.
[group('post-build')]
test-go-generate-nix:
  nix build .#go-generate --no-link --print-build-logs

# === maintenance ===

# Regenerate gomod2nix.toml. Run after changing go.mod.
[group('maintenance')]
update-gomod2nix:
  gomod2nix

[group('maintenance')]
clean-go-cache:
  go clean -cache

[group('maintenance')]
clean-go-modcache:
  go clean -modcache

# Bump the version in flake.nix
[group('maintenance')]
bump-version new_version:
  #!/usr/bin/env bash
  set -euo pipefail
  current=$(grep 'version = "' flake.nix | head -1 | sed 's/.*"\(.*\)".*/\1/')
  if [[ "$current" == "{{new_version}}" ]]; then
    echo "already at {{new_version}}" >&2
    exit 0
  fi
  sed -i.bak 's/version = "'"$current"'"/version = "{{new_version}}"/' flake.nix && rm flake.nix.bak
  echo "$current → {{new_version}}"

# Create a signed git tag for the current version and push it to origin
[group('maintenance')]
deploy-tag:
  #!/usr/bin/env bash
  set -euo pipefail
  version=$(grep 'version = "' flake.nix | head -1 | sed 's/.*"\(.*\)".*/\1/')
  tag="v${version}"
  if git rev-parse "$tag" >/dev/null 2>&1; then
    echo "tag $tag already exists" >&2
    exit 1
  fi
  git tag -s "$tag" -m "Release $tag"
  echo "created tag $tag"
  git push origin "$tag"
  echo "pushed tag $tag"

# Bump version, commit, push master, signed tag + push. Must be run from master.
[group('maintenance')]
deploy-release new_version:
  #!/usr/bin/env bash
  set -euo pipefail
  current_branch=$(git rev-parse --abbrev-ref HEAD)
  if [[ "$current_branch" != "master" ]]; then
    echo "just deploy-release must be run on master (currently on $current_branch)" >&2
    exit 1
  fi
  just bump-version {{new_version}}
  if ! git diff --quiet flake.nix; then
    git add flake.nix
    git commit -m "chore: release v{{new_version}}"
  fi
  git push origin master
  just deploy-tag

# === debug ===

[group('debug')]
debug-integration pattern='TestIntegration':
  go test -run '{{pattern}}' ./generate/ -v -count=1

[group('debug')]
debug-nesting pattern='TestNesting':
  go test -run '{{pattern}}' ./generate/ -v -count=1

[group('debug')]
debug-summary:
  #!/usr/bin/env bash
  set -euo pipefail
  out=$(go test -run 'TestIntegration' ./generate/ -v -count=1 2>&1 || true)
  pass=$(echo "$out" | grep -c 'PASS: TestIntegration' || true)
  fail=$(echo "$out" | grep -c 'FAIL: TestIntegration' || true)
  echo "pass: $pass  fail: $fail"
  echo ""
  if [ "$fail" -gt 0 ]; then
    echo ""
    echo "=== failing tests ==="
    echo "$out" | grep 'FAIL: TestIntegration' | sed 's/--- FAIL: /  /' | sed 's/ (.*//'
    echo ""
    echo "=== error patterns ==="
    echo "$out" | grep -E 'undefined:|cannot use|syntax error|gofmt:' | sort | uniq -c | sort -rn | head -15
  fi

[group('debug')]
debug-test test_name:
  go test -run '^{{test_name}}$' ./generate/ -v -count=1 || true

# Run a ./generate test under the offline env the nix go-generate check
# imposes (GOPROXY=off + TOMMY_TEST_OFFLINE against the already-populated local
# module cache). Quick way to reproduce an offline-resolution failure without a
# full `nix build .#go-generate`.
[group('debug')]
debug-offline-test test_name:
  GOPROXY=off GOFLAGS=-mod=mod GOSUMDB=off TOMMY_TEST_OFFLINE=1 \
    go test -run '^{{test_name}}$' ./generate/ -v -count=1

# Run the ./generate suite through the compositional renderer (#84) under the
# same offline env the nix go-generate check imposes. The TOMMY_COMP_RENDERER
# gate in RenderFile routes generation through comp_*.go. Parity-iteration loop
# for the Phase-1 cutover; drop once the compositional path is the only one.
[group('debug')]
debug-comp-test test_name='Test':
  GOPROXY=off GOFLAGS=-mod=mod GOSUMDB=off TOMMY_TEST_OFFLINE=1 TOMMY_COMP_RENDERER=1 \
    go test -run '{{test_name}}' ./generate/ -count=1

[group('debug')]
debug-nesting-gen test_name:
  #!/usr/bin/env bash
  set -euo pipefail
  dir=$(mktemp -d)
  trap 'rm -rf "$dir"' EXIT
  TOMMY_DEBUG_DIR="$dir" go test -run "^{{test_name}}$" ./generate/ -v -count=1 2>&1 | tail -5 || true
  for f in "$dir"/*_tommy.go; do
    [ -f "$f" ] && echo "=== $f ===" && cat "$f"
  done

[group('debug')]
debug-gen:
  #!/usr/bin/env bash
  set -euo pipefail
  dir=$(mktemp -d)
  trap 'rm -rf "$dir"' EXIT
  cat > "$dir/go.mod" << 'GOMOD'
  module example.com/jent
  go 1.26
  require github.com/amarbel-llc/tommy v0.0.0
  replace github.com/amarbel-llc/tommy => {{justfile_directory()}}
  GOMOD
  cat > "$dir/config.go" << 'GOEOF'
  package jent
  //go:generate tommy generate
  type Config struct {
  	Servers []Server `toml:"servers"`
  }
  type Server struct {
  	Name     string   `toml:"name"`
  	Settings Settings `toml:"settings"`
  }
  type Settings struct {
  	MaxConns int    `toml:"max_conns"`
  	Mode     string `toml:"mode"`
  }
  GOEOF
  cd "$dir" && go mod tidy 2>/dev/null
  cd "{{justfile_directory()}}" && go build -o "$dir/tommy" ./cmd/tommy
  cd "$dir" && GOFILE=config.go ./tommy generate
  cat "$dir/config_tommy.go"

[group('debug')]
debug-bench:
  go test -run TestBenchmarkBackends ./generate/ -v -count=1
