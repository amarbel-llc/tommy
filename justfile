default: build test

build: build-go

build-go:
  go build -o build/tommy ./cmd/tommy

test: test-go test-bats

test-go:
  tap-dancer go-test -skip-empty -v ./...

test-bats: build
  just zz-tests_bats/test

clean: clean-go

clean-go-cache:
  go clean -cache

clean-go-modcache:
  go clean -modcache

clean-go: clean-go-cache clean-go-modcache
