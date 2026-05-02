# Migration guide

Rein intentionally keeps one-shot migration logic out of the core daemon repo. The current op-obsidian import path lives in the separate [`earchibald/rein-migrate-from-op-obsidian`](https://github.com/earchibald/rein-migrate-from-op-obsidian) repository.

## What the migration tool does today

According to the external tool's current README, it:

- discovers `Projects/*/STATUS.md` notes and issue notes under `ISSUES/` and `RESOLVED ISSUES/`
- writes rein project and issue records into the target `rein.db`
- writes an import manifest at `imports/op-obsidian-manifest.json`
- supports `--reset` so a failed import can be discarded and retried cleanly

The tool is intentionally standalone and discardable after a successful import.

## Scope limits

The current bootstrap migration is intentionally narrow:

- projects and issues are imported
- tasks, docs, and workflow-module notes stay in the vault for now
- the rein repo itself does not take on op-obsidian runtime dependencies

## Run the importer

Build or install the external tool from its repository, then point it at your vault and target rein instance:

```bash
rein-migrate-from-op-obsidian \
  --vault /path/to/Agent-Vault \
  --instance live \
  --state-home "$HOME/.local/state" \
  --reset
```

Useful optional flags from the current tool README:

- `--project <slug>` — repeat to limit the import to specific vault projects
- `--dry-run` — print what would be imported without writing files

## Verify the result with rein

After the import, inspect the target instance with the normal rein surfaces:

```bash
./bin/rein --instance live doctor
./bin/rein --instance live project list
./bin/rein --instance live issue list
./bin/rein --instance live tui
```

That gives you one JSON diagnostic view (`doctor`), one machine-readable inventory view (`project list` / `issue list`), and one operator-facing browse view (`tui`).

## Why the tool is external

This split is deliberate:

- `github.com/earchibald/rein/instance` exposes the canonical instance layout helpers
- `github.com/earchibald/rein/sqlite` exposes the migrated SQLite store used by external importers
- the rein daemon stays free of source-system-specific dependencies once the migration is done

If you need migration behavior beyond projects/issues, track that work in the external migration repo rather than assuming the core daemon already supports it.
