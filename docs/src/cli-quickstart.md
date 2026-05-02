# CLI quickstart

This guide assumes you are working from a checkout of `earchibald/rein` and want to exercise the current daemon + CLI flow without guessing at the request shape.

## 1. Build the binary

Rein currently builds from source with Go 1.25:

```bash
go build -o bin/rein ./cmd/rein
```

All examples below use `./bin/rein`, but the same commands work if you place the binary on your `PATH`.

## 2. Start the daemon

By default, rein talks to the `live` instance over that instance's unix socket. When you run from a rein checkout, the daemon also walks upward to the repo root so bundled adapters stay visible from child directories:

```bash
./bin/rein daemon serve
```

Useful global flags:

- `--instance <name>` — switch to a different instance directory
- `--grpc-network tcp|unix` — override the client transport
- `--grpc-address <addr>` — override the client address

For example, to run a second instance explicitly:

```bash
./bin/rein --instance scratch daemon serve
```

## 3. Confirm local health

`rein doctor` is the fastest way to confirm the selected instance is wired correctly. It also reports whether repo-local adapter marketplace discovery succeeded for the current working directory:

```bash
./bin/rein doctor
```

The JSON output includes:

- instance paths (`rootDir`, `socketPath`, `databasePath`)
- daemon reachability and a real RPC probe
- adapter marketplace + signature diagnostics
- credential-provider readiness
- SQLite migration readiness

## 4. Explore the shipped surface

The CLI surface is descriptor-driven. The built-in describe modes are useful when you want authoritative command metadata:

```bash
./bin/rein describe-as=cli
./bin/rein describe-as=mcp
./bin/rein describe-as=mcp-full
```

For help discovery, use subgroup help before drilling into a verb:

```bash
./bin/rein project --help
./bin/rein project list --help
```

The common top-level service commands are:

```bash
./bin/rein project list
./bin/rein issue list
./bin/rein workflow list
./bin/rein execution list
./bin/rein adapter list
```

## 5. Understand the flag conventions

Rein follows two slightly different naming conventions on purpose:

- top-level request flags use the protobuf field names, so filters look like `--project_id`, `--page`, or `--status`
- nested JSON payloads are decoded as protobuf JSON, so embedded objects use JSON names like `displayName`, `projectId`, and `createdTime`

Responses are emitted with protobuf field names (snake_case).

## 6. Create some data

Create a project:

```bash
./bin/rein project create --project '{
  "id": "rein",
  "slug": "rein",
  "displayName": "rein",
  "summary": "Local rein project",
  "status": "PROJECT_STATUS_ACTIVE"
}'
```

Create an issue in that project:

```bash
./bin/rein issue create --issue '{
  "id": "RN-28",
  "projectId": "rein",
  "title": "User docs",
  "summary": "README, quickstarts, telemetry, and migration docs",
  "status": "ISSUE_STATUS_IN_PROGRESS",
  "priority": "ISSUE_PRIORITY_HIGH",
  "assignee": "copilot"
}'
```

Filter the lists back down:

```bash
./bin/rein issue list --project_id rein --status ISSUE_STATUS_IN_PROGRESS
./bin/rein project get --id rein
./bin/rein issue get --id RN-28
```

## 7. Use backup and restore intentionally

Checkpoint the selected instance state into a new destination directory:

```bash
./bin/rein backup ./backups/live
```

If you want rein to stop the selected daemon before copying, add `--stop`:

```bash
./bin/rein backup --stop ./backups/live-stopped
```

Restore from a backup copy:

```bash
./bin/rein restore --stop ./backups/live
```

Notes:

- `rein backup` checkpoints SQLite WAL before copying
- runtime artifacts (`grpc.sock`, `daemon.pid`) are skipped
- `rein restore` refuses to replace live state while the daemon is still running unless you pass `--stop`

## 8. Install the bundled dashboards

The repo ships a dedicated dashboards marketplace plus the local `rein-dashboards` plugin. From the repo root (or any child directory inside it), run:

```bash
SIGNOZ_BASE_URL=http://localhost:3301 \
SIGNOZ_API_KEY=replace-me \
./bin/rein dashboards apply
```

Equivalent flags are also available:

```bash
./bin/rein dashboards apply \
  --signoz-url http://localhost:3301 \
  --signoz-api-key replace-me
```

The JSON response reports which dashboards were `createdIds`, `updatedIds`, or `skippedIds`; unchanged bundled dashboards are skipped on repeat runs. Today this command installs the bundled local dashboards plugin. Remote dashboard bootstrap is not wired into the CLI yet.

## 9. When to switch to the TUI

Once the daemon has enough project/issue/execution state to browse, launch:

```bash
./bin/rein tui
```

See the [TUI quickstart](tui-quickstart.md) for the keymap and drilldown behavior.
