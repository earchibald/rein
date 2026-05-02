# TUI quickstart

`rein tui` is the terminal-native view over the same daemon API that the CLI uses. It is best when you want to browse projects, issues, executions, workflow drilldown, and adapter capability state without manually issuing several list/get commands.

## 1. Start the daemon first

The TUI is a client, not a standalone store browser, so it needs a running daemon:

```bash
go build -o bin/rein ./cmd/rein
./bin/rein daemon serve
```

If the selected instance is empty, the TUI still launches — you will just see empty lists until you create or import data.

## 2. Launch the TUI

In another terminal:

```bash
./bin/rein tui
```

Use `--instance <name>` first if you want to browse a non-default instance:

```bash
./bin/rein --instance scratch tui
```

## 3. Learn the keymap

The shipped keymap is intentionally small:

- `tab` / `shift+tab` — move focus between **Projects**, **Issues**, and **Executions**
- `up` / `down` — move the selected row in the focused list
- `enter` — toggle compact vs expanded execution drilldown
- `r` — refresh immediately
- `q` or `ctrl+c` — quit

The TUI also auto-refreshes on a short interval, so you can leave it open while another terminal mutates daemon state.

## 4. What the layout shows

The left side is a navigator with three linked lists:

1. **Projects**
2. **Issues** filtered to the selected project
3. **Executions** filtered to the selected issue

The right side is the overview/drilldown panel. Depending on what is selected, it shows:

- overall counts and last refresh time
- the selected project summary
- the selected issue status, priority, assignee, and workflow reference
- workflow step status for the related workflow
- execution drilldown, including task steps, side effects, metadata, and looking-glass state

## 5. Looking glass expectations

The TUI surfaces looking-glass state conservatively:

- **disabled** — no adapter in the execution advertises `tail=true`
- **gated** — adapters advertise tail support, but the daemon does not expose looking-glass streaming yet
- **available** — reserved for the future fully available streaming state

Today, adapters can advertise tail support and the TUI will display that fact, but live tail streaming is not implemented yet.

## 6. Practical workflow

A typical loop is:

1. use the [CLI quickstart](cli-quickstart.md) to start the daemon and seed or import data
2. launch `rein tui`
3. browse projects/issues/executions in one terminal
4. keep a second terminal open for `rein doctor`, `rein issue update`, `rein execution inspect`, or `rein dashboards apply`

## 7. When to fall back to the CLI

Prefer the CLI when you need:

- machine-readable JSON output
- precise filters or scripted automation
- `describe-as=cli` / `describe-as=mcp` / `describe-as=mcp-full`
- backup/restore operations
- SigNoz dashboard installation

The TUI is for inspection and drilldown; the CLI remains the canonical automation surface.
