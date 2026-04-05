default: build test

build: build-go

build-go:
  go build -o build/tommy ./cmd/tommy

build-nix: gomod2nix
  nix build --show-trace

gomod2nix:
  gomod2nix

test: test-go test-bats

test-go:
  tap-dancer go-test --skip-empty ./...

test-bats: build
  cd zz-tests_bats && TOMMY_BIN=../build/tommy BATS_TEST_TIMEOUT=30 bats --tap --jobs {{num_cpus()}} *.bats

clean: clean-go

clean-go-cache:
  go clean -cache

clean-go-modcache:
  go clean -modcache

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
debug-all-backends pattern='TestIntegration':
  @echo "=== jen (default) ==="
  go test -run '{{pattern}}' ./generate/ -count=1
  @echo "=== api ==="
  TOMMY_CODEGEN_IR=api go test -run '{{pattern}}' ./generate/ -count=1
  @echo "=== cst ==="
  TOMMY_CODEGEN_IR=cst go test -run '{{pattern}}' ./generate/ -count=1
  @echo "=== legacy ==="
  TOMMY_CODEGEN_IR=legacy go test -run '{{pattern}}' ./generate/ -count=1

[group('debug')]
debug-bench:
  go test -run TestBenchmarkBackends ./generate/ -v -count=1

clean-go: clean-go-cache clean-go-modcache
