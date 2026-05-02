# rein
Modular orchestrator daemon — successor to op-obsidian. Go daemon + plural HMIs (CLI/TUI) + plugin marketplace.

## Development

- `just proto` lints the Buf workspace and regenerates the committed Go protobuf/gRPC stubs.
- `just test` is the standard test entrypoint and runs the suite via `gotestsum`.
- `just test-race` keeps the same runner while enabling the Go race detector.
- `just test-cover` writes `coverage.out` and prints `go tool cover -func` output for local review.

## Test harness

- `internal/server` keeps a reusable bufconn-backed in-process gRPC harness alongside its runtime tests.
- `internal/storage/sqlite` exposes migrated in-memory SQLite helpers so tests stay hermetic and fast.
- Coverage thresholds are tracked per harness-heavy package: keep `internal/server` at or above 75% statement coverage and `internal/storage/sqlite` at or above 70%, and treat any drop in those packages as a regression to fix before merge.
