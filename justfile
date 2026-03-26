default: build test

build: build-go

build-go:
  go build -o build/tommy ./cmd/tommy

test: test-go test-bats

test-go:
  tap-dancer go-test ./...

test-bats: build
  cd zz-tests_bats && TOMMY_BIN=../build/tommy BATS_TEST_TIMEOUT=30 bats --tap --jobs {{num_cpus()}} *.bats

clean: clean-go

clean-go-cache:
  go clean -cache

clean-go-modcache:
  go clean -modcache

clean-go: clean-go-cache clean-go-modcache
