# Plugin author guide

This guide describes the plugin and marketplace surface that exists in the repository today. It is intentionally conservative: rein can already discover, diagnose, and list adapters from marketplace metadata, but remote managed execution is still a staged follow-up for some adapter classes.

## The files that matter

Repository-level marketplace metadata lives in:

- `.claude-plugin/marketplace.json`
- `.claude-plugin/trusted-keys.json`

Bundled local plugins live under `plugins/<name>/`, with a manifest at:

- `plugins/<name>/.claude-plugin/plugin.json`

Examples in this repo:

- `plugins/messaging-null/.claude-plugin/plugin.json`
- `plugins/muxiterm/.claude-plugin/plugin.json`
- `plugins/tracker-github/.claude-plugin/plugin.json`
- `plugins/rein-dashboards/.claude-plugin/plugin.json`

## Marketplace entry shape

A local adapter can be declared with the shorthand path form:

```json
{
  "name": "muxiterm",
  "source": "./plugins/muxiterm",
  "version": "0.9.0",
  "description": "First-party mux adapter backed by the muxiterm JSON CLI.",
  "category": "mux",
  "daemonApiVersion": "rein.v1"
}
```

The marketplace loader also understands object-form sources for:

- `github`
- `url`
- `git-subdir`
- `npm`

Current behavior is important:

- local entries can load and overlay a local manifest immediately
- remote entries are visible through registry and diagnostic surfaces today
- remote managed execution is still an explicit stub for the external coding-adapter path

## Local manifest shape

A bundled manifest lives at `.claude-plugin/plugin.json` inside the plugin root.

Minimal example:

```json
{
  "name": "example-mux",
  "version": "0.1.0",
  "description": "Example mux adapter",
  "category": "mux",
  "daemonApiVersion": "rein.v1",
  "capabilities": {
    "session.attach": "true"
  }
}
```

Useful optional fields:

- `capabilities` — free-form string map advertised to the daemon and UIs
- `tail` — boolean shorthand for `capabilities.tail`
- `requires` — list of required capability strings; rein encodes this into `capabilities.requires`

Supported marketplace categories in the current daemon are:

- `codingAgent`
- `mux`
- `tracker`
- `messaging`
- `projection`

Use the canonical spellings above in committed manifests even though the loader normalizes a few aliases.

## Precedence and `strict`

For local plugins, rein combines marketplace metadata with the local manifest.

- default behavior is `strict: true`, which means the local manifest overlays marketplace metadata
- if a marketplace entry sets `"strict": false`, marketplace values win over the local manifest for overlapping fields

That makes the common case simple: keep stable discovery metadata in the marketplace and let the plugin manifest own the concrete local adapter details.

## Path and source rules

Local source paths must:

- start with `./`
- stay within the repository root
- point at a plugin root that may contain `.claude-plugin/plugin.json`

`git-subdir` sources must also stay within the remote repository root.

## Signatures and trusted keys

The repo ships a signed `.claude-plugin/marketplace.json` and verifier keys in `.claude-plugin/trusted-keys.json`.

Important nuance for local development:

- `rein doctor` reports whether the marketplace signature is present and verified
- the daemon's local discovery path tolerates unsigned marketplace indexes for local iteration
- committed repository metadata should still stay signed

This lets contributors iterate locally without blocking on a signing step while preserving a signed canonical index in the repo.

## Validation loop

Use the current runtime surfaces to validate your work:

```bash
./bin/rein doctor
./bin/rein adapter list
./bin/rein adapter get --id muxiterm
```

`rein doctor` is especially useful because it reports:

- marketplace presence
- signature status
- manifest presence for local plugins
- daemon API compatibility
- the first load error that would block the registry

## Dashboard plugins vs adapter plugins

Dashboard plugins use a parallel marketplace file, `.claude-plugin/dashboards-marketplace.json`, plus a local manifest shape under `plugins/rein-dashboards/.claude-plugin/plugin.json`.

Today:

- adapter marketplace entries can be local or remote metadata
- dashboard marketplace entries can be described as local/GitHub/URL sources
- `rein dashboards apply` only installs bundled local dashboard plugins

If you are documenting or shipping a plugin, be explicit about which parts are discovery metadata versus actually executable bootstrap today.
