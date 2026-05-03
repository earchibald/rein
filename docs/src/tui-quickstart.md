# TUI quickstart

`rein tui` is the terminal-native view over the same daemon API that the CLI uses. It is best when you want to browse projects, issues, executions, workflow drilldown, and adapter capability state without manually issuing several list/get commands.

If this is your very first rein session, start with the [first-run operator walkthrough](first-run-operator-walkthrough.md) so the daemon, instance, and sample data are already in place before you open the TUI.

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
- `pgup` / `pgdown` — scroll the right-side overview/drilldown pane when it overflows
- `home` / `end` — jump to the top or bottom of the right-side overview/drilldown pane
- `enter` — toggle compact vs expanded execution drilldown
- `r` — refresh immediately
- `q` or `ctrl+c` — quit

The TUI keeps the selected execution drilldown fresh on a short interval, while broader project/issue/execution list refreshes run on a longer cadence so non-trivial instances do not pay for a full reload every five seconds.

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

When the right-side pane has more content than fits, the TUI shows `↑ … above` / `↓ … more` indicators so it is obvious when additional drilldown content is available off-screen.

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

## Related docs

- [Overview](README.md)
- [CLI quickstart](cli-quickstart.md)
- [Metrics, logs, and OTLP](metrics-logs-otlp.md)
- [Plugin author guide](plugin-author-guide.md)
- [Migration guide](migration-guide.md)
- [Docs summary](SUMMARY.md)

<div hidden>

[[README|Overview]]
[[cli-quickstart|CLI quickstart]]
[[metrics-logs-otlp|Metrics, logs, and OTLP]]
[[plugin-author-guide|Plugin author guide]]
[[migration-guide|Migration guide]]
[[SUMMARY|Docs summary]]

</div>
