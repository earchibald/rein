# rein
Modular orchestrator daemon — successor to op-obsidian. Go daemon + plural HMIs (CLI/TUI) + plugin marketplace.

Rein is still early, but the current tree already ships a usable local daemon, a descriptor-driven CLI, a terminal UI, per-instance SQLite state management, backup/restore tooling, diagnostics, and opt-in OTLP telemetry with a bundled SigNoz dashboard pack.

## What ships today

- A single `rein` binary that can run the daemon, speak the canonical gRPC/CLI surface, and launch the TUI.
- Per-instance state under `~/.local/state/rein/instances/<name>/` (or `$XDG_STATE_HOME/rein/instances/<name>/`) with `rein backup` and `rein restore` for safe copies.
- `rein doctor` JSON diagnostics for instance layout, daemon reachability, repo-local adapter marketplace discovery/readiness, credential-provider readiness, and SQLite migration state.
- Opt-in OTLP/gRPC export for traces, metrics, and logs, plus a bundled `rein dashboards apply` workflow for SigNoz.
- A signed repository marketplace index plus bundled first-party plugins under `plugins/`.
- External seams for one-shot migration tooling and external coding adapters, without pulling those source-system dependencies into the core daemon.

## Quickstart

Build the current tree with Go 1.25:

```bash
go build -o bin/rein ./cmd/rein
```

Start the daemon for the default `live` instance from the repo root (or any child directory inside this repo so bundled adapters are discovered consistently):

```bash
./bin/rein daemon serve
```

In another terminal, inspect health and the current API surface from that same checkout (doctor reports repo-local marketplace discovery explicitly when you are outside a repo checkout):

```bash
./bin/rein doctor
./bin/rein project list
./bin/rein issue list
./bin/rein describe-as=cli
./bin/rein version
```

Launch the terminal UI against that same daemon:

```bash
./bin/rein tui
```

Checkpoint the selected instance before risky changes:

```bash
./bin/rein backup ./backups/live
```

Apply the bundled SigNoz dashboards from the repo root (or any child directory inside this repo):

```bash
SIGNOZ_BASE_URL=http://localhost:3301 \
SIGNOZ_API_KEY=replace-me \
./bin/rein dashboards apply
```

Re-running the command skips unchanged bundled dashboards and reports `createdIds`, `updatedIds`, and `skippedIds` in the JSON output.

## Documentation

Longer user guides live under `docs/` and can be read directly in the repo or served as an mdBook:

- [Docs overview](docs/src/README.md)
- [CLI quickstart](docs/src/cli-quickstart.md)
- [TUI quickstart](docs/src/tui-quickstart.md)
- [Metrics, logs, and OTLP](docs/src/metrics-logs-otlp.md)
- [Plugin author guide](docs/src/plugin-author-guide.md)
- [Migration guide](docs/src/migration-guide.md)
- [Docs summary / table of contents](docs/src/SUMMARY.md)

To browse the same content as a docs site locally:

```bash
cargo install mdbook --locked
mdbook serve docs
```

## Releases

- Pushing a CalVer tag in the form `YYYY.MM.PATCH` triggers the GitHub release workflow, which reruns CI, builds release archives, publishes a GitHub Release, and records GitHub artifact attestations for the uploaded archives.
- The release pipeline does not attempt platform-native code signing or notarization. Those remain future follow-ups that require Apple/Windows signing material and any associated notarization credentials.

## Current limitations to keep in mind

- The repository marketplace can advertise remote adapters today, but remote managed execution is still an explicit stub until the external adapter bridge lands.
- `rein dashboards apply` currently installs bundled local dashboard plugins; remote dashboard bootstrap is an explicit follow-up.
- Looking-glass tail support is surfaced in the TUI and execution inspection, but the daemon does not stream live tails yet.
- One-shot imports from op-obsidian live in the separate [`earchibald/rein-migrate-from-op-obsidian`](https://github.com/earchibald/rein-migrate-from-op-obsidian) repo.

## Instance state layout

- Rein keeps per-instance state under `~/.local/state/rein/instances/<name>/` by default, or under `$XDG_STATE_HOME/rein/instances/<name>/` when `XDG_STATE_HOME` is set.
- The active instance is selected with `--instance <name>` or `REIN_INSTANCE=<name>`; when neither is set, rein uses the `live` instance.
- Only the `live` instance is eligible for future auto-start behavior. Other instances must be started explicitly.
- The current reserved layout is:
  - `grpc.sock` — default unix gRPC listener socket for that instance.
  - `daemon.pid` — advisory pid/lock file while the selected daemon is running.
  - `rein.db` — canonical SQLite path reserved for that instance's persisted state.
- `rein doctor` emits JSON diagnostics for daemon reachability, canonical instance layout, adapter registry compatibility, credential provider readiness, and SQLite migration state.
- `rein backup <destination>` checkpoints SQLite WAL with `PRAGMA wal_checkpoint(TRUNCATE)` and atomically copies the selected instance state directory into `<destination>` while the daemon may still be running.
- `rein backup --stop <destination>` is the paranoid fallback when you want the selected daemon stopped before copying.
- `rein restore <source>` atomically replaces the selected instance state directory from `<source>`; stop the daemon first or pass `--stop` so rein can stop the selected instance before restoring.
- `rein daemon serve` accepts optional OTLP/gRPC export flags (`--otlp-endpoint`, `--otlp-headers`, `--otlp-insecure`) and also honors `OTEL_EXPORTER_OTLP_*`, `OTEL_SERVICE_NAME`, and `OTEL_RESOURCE_ATTRIBUTES`.

## CLI surface

- `rein` is the canonical gRPC client CLI. By default it connects to the selected instance over that instance's unix socket.
- Use `rein daemon serve` to start the daemon for the selected instance; when you run from a rein checkout, the bundled adapter marketplace is discovered from the repo root even in child directories.
- Use `rein backup <destination>` to checkpoint and copy the selected instance state directory.
- Use `rein restore <source>` to swap the selected instance state directory back from a backup copy.
- Use `rein doctor` to inspect the selected instance and emit machine-parseable JSON readiness diagnostics, including whether repo-local adapter marketplace discovery succeeded.
- Use `rein dashboards apply` to push the bundled `rein-dashboards` SigNoz reference dashboards into a SigNoz workspace over the HTTP API.
- Use `rein describe-as=cli` for a manual-style, machine-consumable description of the CLI/gRPC surface, service-group help, utility commands, and reachable protobuf schemas.
- Use `rein describe-as=mcp` for a compact stable YAML description of commands, help entry points, gateway stub routes, and a schema index suitable for wrapper/skill tooling.
- Use `rein describe-as=mcp-full` when you need the exhaustive YAML schema dump behind that compact MCP surface.
- Use `rein tui` for the terminal-native HMI over that same daemon surface.
- Use `rein version` to print the current CalVer, commit/build metadata, and local-vs-release provenance.
- Service commands mirror the protobuf API: `rein project|issue|execution|workflow|adapter <verb>`.
- Use `rein <service> --help` to discover verbs for a service group, then `rein <service> <verb> --help` for request flags.
- Top-level request flags map 1:1 to top-level protobuf request fields and use the protobuf field names (for example `--project_id`).
- Scalar fields take plain values; message, repeated, and map fields take JSON blobs.
- Nested JSON payloads are decoded with protobuf JSON field names (for example `displayName` inside a `Project` payload).
- Responses are emitted as JSON using protobuf field names.

## Adapter registry

- The repo ships a signed marketplace index at `.claude-plugin/marketplace.json` for built-in and remote adapter discovery, and repo-local commands walk upward so child directories resolve the same checkout root.
- Repository-local trusted public keys live at `.claude-plugin/trusted-keys.json`; `rein doctor` reports signature presence and verification status.
- The daemon's local discovery path tolerates unsigned marketplace indexes for local development, but the committed repository index should remain signed.
- `messaging-null` is a no-op notification adapter that advertises `messaging.post` so workflows can stay launchable until Slack and Discord adapters land.
- The in-tree `muxiterm` multiplexer adapter manifest lives at `plugins/muxiterm/.claude-plugin/plugin.json` and advertises the bundled mux capability surface.
- The in-tree `tracker-github` manifest lives at `plugins/tracker-github/.claude-plugin/plugin.json` and advertises worktree/PR/tracker capabilities.

## OTLP telemetry

- OTLP export is opt-in. When no OTLP endpoint is configured, the daemon keeps its existing no-op behavior for traces, metrics, and OTLP log export.
- The daemon still writes structured text logs to stdout even when OTLP export is disabled.
- When configured, the daemon exports traces, logs, and bounded custom metrics for daemon starts, daemon liveness, gRPC request counts, gRPC error counts, and gRPC request duration.
- Current metric names are `rein.daemon.starts`, `rein.daemon.running`, `rein.rpc.requests`, `rein.rpc.errors`, and `rein.rpc.duration`.
- Default OTLP resource attributes include `service.name=rein-daemon`, `rein.instance=<instance>`, and `service.instance.id=<instance>`.
- `rein dashboards apply` installs the bundled SigNoz dashboard JSON at `plugins/rein-dashboards/signoz/rein-daemon-otlp.json`.

## Dashboards marketplace

- The reference dashboards ship from a dedicated marketplace index at `.claude-plugin/dashboards-marketplace.json`.
- The bundled local plugin lives at `plugins/rein-dashboards/.claude-plugin/plugin.json` and currently targets SigNoz with `plugins/rein-dashboards/signoz/rein-daemon-otlp.json`.
- `rein dashboards apply` resolves that marketplace entry from the repository root, then creates, updates, or skips dashboards in SigNoz using the `SIGNOZ-API-KEY` header.
- The loader accepts the same future-facing local/GitHub/URL source shape as the adapter marketplace, but remote dashboard bootstrap remains an explicit follow-up.

## TUI

- `rein tui` reads projects, issues, executions, workflow drilldown, and adapter capability state over the canonical gRPC API.
- Execution drilldown includes workflow steps, side effects, execution metadata, and looking-glass status for any adapter that advertises `capabilities.tail=true`.
- The overview/drilldown pane is scrollable with visible overflow indicators, and auto-refresh keeps selected execution detail live without reloading every dataset every five seconds.
- Looking-glass tailing is capability-gated: when adapters advertise tail support but the daemon cannot stream it yet, the TUI shows that state instead of assuming live tail availability.

## Marketplace bootstrap

- Rein discovers adapters from `.claude-plugin/marketplace.json` at the repository root.
- RN-23 boots the Claude Code coding-agent adapter as a remote GitHub marketplace entry (`earchibald/rein-adapter-claude-code`) instead of a local plugin checkout.
- The registry and adapter APIs surface that remote entry immediately; managed execution still reports an explicit “not wired yet” stub until the external adapter bridge lands.

## External migration tooling

- One-shot migration/import tools live in separate repos rather than pulling legacy source-system dependencies into `rein`.
- `github.com/earchibald/rein/instance` exposes the canonical instance layout helpers used to resolve `grpc.sock` and `rein.db` paths for external tools.
- `github.com/earchibald/rein/sqlite` exposes the migrated locked-entity SQLite store needed to create and populate rein state from those external tools.
- RN-27 boots the op-obsidian migration CLI as the separate repo [`earchibald/rein-migrate-from-op-obsidian`](https://github.com/earchibald/rein-migrate-from-op-obsidian).

## Development

- Go 1.25 is the repository baseline.
- `just proto` lints the Buf workspace and regenerates the committed Go protobuf/gRPC stubs.
- `just build` builds `./cmd/rein` into `bin/rein`.
- `just test` is the standard test entrypoint and runs the suite via `gotestsum`.
- `just test-race` keeps the same runner while enabling the Go race detector.
- `just test-cover` writes `coverage.out` and prints `go tool cover -func` output for local review.
- `just lint` runs golangci-lint v2 (`go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.1`).

## Test harness

- `internal/server` keeps a reusable bufconn-backed in-process gRPC harness alongside its runtime tests.
- `internal/storage/sqlite` exposes migrated in-memory SQLite helpers so tests stay hermetic and fast.
- Coverage thresholds are tracked per harness-heavy package: keep `internal/server` at or above 75% statement coverage and `internal/storage/sqlite` at or above 70%, and treat any drop in those packages as a regression to fix before merge.
