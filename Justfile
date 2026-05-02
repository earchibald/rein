# rein — project work-surface
# Run `just` or `just --list` to see available recipes.

buf := "go run github.com/bufbuild/buf/cmd/buf@v1.59.0"
gotestsum := "go run gotest.tools/gotestsum@v1.10.0"
golangci_lint := "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.1"

default:
    @just --list

# Lint and generate protobuf/gRPC code
proto:
    rm -rf gen/go
    {{buf}} lint
    {{buf}} generate

# Verify protobuf generation is current
proto-check:
    just proto
    git --no-pager diff --exit-code -- gen/go

# Enforce buf breaking policy against main
proto-breaking:
    @if git rev-parse --verify --quiet origin/main >/dev/null 2>&1; then \
        against='.git#ref=origin/main,subdir=proto'; \
    elif git rev-parse --verify --quiet main >/dev/null 2>&1; then \
        against='.git#branch=main,subdir=proto'; \
    else \
        echo 'buf breaking requires origin/main or main to exist locally' >&2; \
        exit 1; \
    fi; \
    echo "buf breaking against $against"; \
    {{buf}} breaking --against "$against"

# Build the rein binary
build:
    go build -o bin/rein ./cmd/rein

# Build every package the same way CI does
build-all:
    go build ./...

# Run tests
test:
    {{gotestsum}} --format pkgname -- ./...

# Run tests with race detector
test-race:
    {{gotestsum}} --format pkgname -- -race ./...

# Run tests with coverage output
test-cover:
    {{gotestsum}} --format pkgname -- -coverprofile=coverage.out -covermode=atomic ./...
    go tool cover -func=coverage.out

# Run golangci-lint
lint:
    GOLANGCI_LINT_CACHE="$PWD/.golangci-lint-cache" {{golangci_lint}} run ./...

# Validate docs render as an mdBook
docs:
    mdbook build docs

# Run go vet
vet:
    go vet ./...

# Format Go code
fmt:
    gofmt -w .
    goimports -w . 2>/dev/null || true

# Tidy go.mod / go.sum
tidy:
    go mod tidy

# Clean build artifacts
clean:
    rm -rf bin/

# Run proto, build, vet, lint, race tests, breaking checks, and docs (CI equivalent)
ci: proto-check build-all vet lint test-race proto-breaking docs

# Run the macOS-targeted CI coverage for daemon listener/auth paths
ci-macos: build-all vet
    {{gotestsum}} --format pkgname -- -race ./cmd/rein ./internal/instance ./internal/server

# Install development tools
install-tools:
    go install gotest.tools/gotestsum@v1.10.0
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.1
    go install golang.org/x/tools/cmd/goimports@latest
    cargo install mdbook --locked
