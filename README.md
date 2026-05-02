# rein
Modular orchestrator daemon — successor to op-obsidian. Go daemon + plural HMIs (CLI/TUI) + plugin marketplace.

## Instance state layout

- Rein keeps per-instance state under `~/.local/state/rein/instances/<name>/` by default, or under `$XDG_STATE_HOME/rein/instances/<name>/` when `XDG_STATE_HOME` is set.
- The active instance is selected with `--instance <name>` or `REIN_INSTANCE=<name>`; when neither is set, rein uses the `live` instance.
- Only the `live` instance is eligible for future auto-start behavior. Other instances must be started explicitly.
- The current reserved layout is:
  - `grpc.sock` — default unix gRPC listener socket for that instance.
  - `rein.db` — canonical SQLite path reserved for that instance's persisted state.
- `rein doctor` emits JSON diagnostics for daemon reachability, canonical instance layout, adapter registry compatibility, credential provider readiness, and SQLite migration state.

## CLI surface

- `rein` is the canonical gRPC client CLI. By default it connects to the selected instance over that instance's unix socket.
- Use `rein daemon serve` to start the daemon for the selected instance.
- Use `rein doctor` to inspect the selected instance and emit machine-parseable JSON readiness diagnostics.
- Use `rein describe-as=cli` for a manual-style, machine-consumable description of the CLI/gRPC surface and reachable protobuf schemas.
- Use `rein describe-as=mcp` for a stable YAML description of commands, flags, gateway stub routes, and schemas suitable for wrapper/skill tooling.
- Service commands mirror the protobuf API: `rein project|issue|execution|workflow|adapter <verb>`.
- Request flags map 1:1 to top-level gRPC request fields. Scalar fields take plain values; message, repeated, and map fields take JSON blobs.
- Responses are emitted as JSON using protobuf field names.

## Development

- `just proto` lints the Buf workspace and regenerates the committed Go protobuf/gRPC stubs.
- `just test` is the standard test entrypoint and runs the suite via `gotestsum`.
- `just test-race` keeps the same runner while enabling the Go race detector.
- `just test-cover` writes `coverage.out` and prints `go tool cover -func` output for local review.

## Test harness

- `internal/server` keeps a reusable bufconn-backed in-process gRPC harness alongside its runtime tests.
- `internal/storage/sqlite` exposes migrated in-memory SQLite helpers so tests stay hermetic and fast.
- Coverage thresholds are tracked per harness-heavy package: keep `internal/server` at or above 75% statement coverage and `internal/storage/sqlite` at or above 70%, and treat any drop in those packages as a regression to fix before merge.
