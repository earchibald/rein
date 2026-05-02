# rein — project work-surface
# Run `just` or `just --list` to see available recipes.

default:
    @just --list

# Build the rein binary
build:
    go build -o bin/rein ./cmd/rein

# Run tests
test:
    go test ./...

# Run tests with race detector
test-race:
    go test -race ./...

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

# Run build + lint + test (CI equivalent)
ci: build lint test

# Install development tools
install-tools:
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
    go install golang.org/x/tools/cmd/goimports@latest
