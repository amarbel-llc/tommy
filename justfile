default: build

build: build-go

build-go:
  go build ./cmd/tommy

test: test-go

test-go:
  go test -v ./...

clean: clean-go

clean-go-cache:
  go clean -cache

clean-go-modcache:
  go clean -modcache

clean-go: clean-go-cache clean-go-modcache
