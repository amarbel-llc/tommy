default: build test

build: build-go

build-go:
  go build -o build/tommy ./cmd/tommy

test: test-go test-bats

test-go:
  go test -v ./...

test-bats: build
  cd zz-tests_bats && TOMMY_BIN=../build/tommy BATS_TEST_TIMEOUT=30 bats --tap *.bats

clean: clean-go

clean-go-cache:
  go clean -cache

clean-go-modcache:
  go clean -modcache

clean-go: clean-go-cache clean-go-modcache
