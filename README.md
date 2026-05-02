# rein
Modular orchestrator daemon — successor to op-obsidian. Go daemon + plural HMIs (CLI/TUI) + plugin marketplace.

## Instance state layout

- Rein keeps per-instance state under `~/.local/state/rein/instances/<name>/` by default, or under `$XDG_STATE_HOME/rein/instances/<name>/` when `XDG_STATE_HOME` is set.
- The active instance is selected with `--instance <name>` or `REIN_INSTANCE=<name>`; when neither is set, rein uses the `live` instance.
- Only the `live` instance is eligible for future auto-start behavior. Other instances must be started explicitly.
- The current reserved layout is:
  - `grpc.sock` — default unix gRPC listener socket for that instance.
  - `rein.db` — canonical SQLite path reserved for that instance's persisted state.
- `rein doctor` will validate that commands and on-disk state follow this canonical layout in a future issue.

## Development

- `just proto` lints the Buf workspace and regenerates the committed Go protobuf/gRPC stubs.
- `just test` is the standard test entrypoint and runs the suite via `gotestsum`.
- `just test-race` keeps the same runner while enabling the Go race detector.
- `just test-cover` writes `coverage.out` and prints `go tool cover -func` output for local review.

## Test harness

- `internal/server` keeps a reusable bufconn-backed in-process gRPC harness alongside its runtime tests.
- `internal/storage/sqlite` exposes migrated in-memory SQLite helpers so tests stay hermetic and fast.
- Coverage thresholds are tracked per harness-heavy package: keep `internal/server` at or above 75% statement coverage and `internal/storage/sqlite` at or above 70%, and treat any drop in those packages as a regression to fix before merge.
