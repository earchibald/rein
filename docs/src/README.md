# rein docs

These pages are the longer-form companion to the repository [README](../../README.md). The goal is to keep the README useful as a landing page while giving operators, plugin authors, and migrators a place for the current details that matter.

## What rein is today

Rein currently ships:

- a Go daemon with a canonical gRPC surface
- a descriptor-driven CLI in the same `rein` binary
- a terminal UI (`rein tui`) over that daemon surface
- per-instance SQLite state with backup/restore commands
- `rein doctor` JSON diagnostics
- opt-in OTLP export for traces, metrics, and logs
- a bundled SigNoz dashboard plugin and marketplace metadata

It also deliberately keeps some work out of tree:

- op-obsidian migration lives in [`earchibald/rein-migrate-from-op-obsidian`](https://github.com/earchibald/rein-migrate-from-op-obsidian)
- the Claude Code coding-agent adapter bootstrap lives in [`earchibald/rein-adapter-claude-code`](https://github.com/earchibald/rein-adapter-claude-code)

## Start here

- New operator? Read the [CLI quickstart](cli-quickstart.md).
- Prefer a terminal UI? Read the [TUI quickstart](tui-quickstart.md).
- Wiring observability? Read [Metrics, logs, and OTLP](metrics-logs-otlp.md).
- Adding marketplace entries or local manifests? Read the [Plugin author guide](plugin-author-guide.md).
- Moving data over from op-obsidian? Read the [Migration guide](migration-guide.md).
- Need the full table of contents? See the [Docs summary](SUMMARY.md).

<div hidden>

[[cli-quickstart|CLI quickstart]]
[[tui-quickstart|TUI quickstart]]
[[metrics-logs-otlp|Metrics, logs, and OTLP]]
[[plugin-author-guide|Plugin author guide]]
[[migration-guide|Migration guide]]
[[SUMMARY|Docs summary]]

</div>

## Serving the docs site locally

The docs are structured as an mdBook rooted at `docs/`:

```bash
cargo install mdbook --locked
mdbook serve docs
```

If you do not want to install mdBook, you can read the Markdown files directly in GitHub or in your editor.

## Scope notes

These docs intentionally describe the shipped behavior in this tree today:

- remote adapters can be discovered through the marketplace, but remote managed execution is still a stub
- remote dashboard plugin sources are parsed by the loader, but `rein dashboards apply` currently installs bundled local plugins only
- looking-glass tail support is surfaced in CLI/TUI status, but the daemon does not yet stream live tails
