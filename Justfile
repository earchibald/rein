# rein — project work-surface
# Run `just` or `just --list` to see available recipes.

buf := "go run github.com/bufbuild/buf/cmd/buf@v1.59.0"
gotestsum := "go run gotest.tools/gotestsum@v1.10.0"

default:
    @just --list

# Lint and generate protobuf/gRPC code
proto:
    rm -rf gen/go
    {{buf}} lint
    {{buf}} generate

# Build the rein binary
build:
    go build -o bin/rein ./cmd/rein

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
    golangci-lint run ./...

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

# Run build + vet + lint + race-tested suite (CI equivalent)
ci: build vet lint test-race

# Install development tools
install-tools:
    go install gotest.tools/gotestsum@v1.10.0
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
    go install golang.org/x/tools/cmd/goimports@latest
