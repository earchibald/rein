# First-run operator walkthrough

This is the best place to start if you are totally fresh to `rein` and want a repeatable, low-risk first run. It keeps state inside the checkout, uses a disposable `demo` instance instead of the default `live` instance, and walks through the shipped operator surfaces in the order they make sense today.

Rein currently ships two operator HMIs:

- the canonical CLI
- the terminal UI (`rein tui`)

It does **not** ship a browser UI yet. The future HTTP/SSE gateway is still a stub, so first-time operator training should stop at CLI + TUI for now.

## 1. Open a clean checkout and isolate the state home

From the repo root:

```bash
export XDG_STATE_HOME="$PWD/.tmp/rein-state"
go build -o bin/rein ./cmd/rein
```

Using a repo-local `XDG_STATE_HOME` makes teardown easy: once you stop the daemon, removing `.tmp/rein-state` returns you to a clean slate.

## 2. Start a disposable daemon instance

Keep this terminal open:

```bash
export XDG_STATE_HOME="$PWD/.tmp/rein-state"
./bin/rein --instance demo daemon serve
```

The `demo` instance is intentional. It keeps first-run exploration separate from the default `live` instance and makes the walkthrough safe to repeat.

## 3. Explore the CLI from a second terminal

In another terminal, from the same checkout:

```bash
export XDG_STATE_HOME="$PWD/.tmp/rein-state"

./bin/rein --instance demo doctor
./bin/rein --instance demo version
./bin/rein --instance demo project list
./bin/rein --instance demo issue list
./bin/rein --instance demo describe-as=cli
./bin/rein --instance demo project --help
./bin/rein --instance demo issue create --help
```

That sequence shows the core first-run checkpoints:

1. `doctor` proves the instance layout, socket, SQLite database, and bundled adapter discovery are healthy.
2. `version` confirms which tree you built.
3. `project list` and `issue list` show that the fresh instance is empty.
4. `describe-as=cli` exposes the authoritative shipped command surface.
5. subgroup help confirms how request payloads are passed.

For deeper CLI details after this pass, continue with the [CLI quickstart](cli-quickstart.md).

## 4. Seed a tiny repeatable training dataset

Create one project and one issue so the lists and TUI have something real to show:

```bash
export XDG_STATE_HOME="$PWD/.tmp/rein-state"

./bin/rein --instance demo project create --project '{
  "id": "rein-demo",
  "slug": "rein-demo",
  "displayName": "rein-demo",
  "summary": "Repeatable first-run training project",
  "status": "PROJECT_STATUS_ACTIVE"
}'

./bin/rein --instance demo issue create --issue '{
  "id": "RD-1",
  "projectId": "rein-demo",
  "title": "Scaffold the demo project with an agent prompt",
  "summary": "Seed the repeatable training loop for the lightweight Rust demo repository.",
  "status": "ISSUE_STATUS_OPEN",
  "priority": "ISSUE_PRIORITY_HIGH",
  "assignee": "operator"
}'

./bin/rein --instance demo project list
./bin/rein --instance demo issue list --project_id rein-demo
./bin/rein --instance demo issue get --id RD-1
```

This gives you a stable first issue for repeated operator training: `RD-1` is always the bootstrap issue for the demo project.

## 5. Launch the TUI against the same instance

Still from the same checkout:

```bash
export XDG_STATE_HOME="$PWD/.tmp/rein-state"
./bin/rein --instance demo tui
```

At this point you should be able to browse:

1. the `rein-demo` project
2. the `RD-1` issue
3. an otherwise clean execution list

Use the [TUI quickstart](tui-quickstart.md) for the keymap and drilldown behavior once the screen is open.

## 6. Know where the walkthrough stops today

Current operator training should include these boundaries explicitly:

- the CLI is the canonical automation surface
- the TUI is the shipped inspection surface
- the browser/web experience is **not** shipped yet
- `describe-as=mcp` and related output can describe the planned gateway shape, but the HTTP/SSE gateway itself is still not implemented

That means first-time training should focus on build, daemon health, CLI discovery, sample data, and TUI browsing.

## 7. Bootstrap the repeatable `rein-demo` training loop

For the cross-tool training loop, the base demo should stay as small as possible. The repo now ships a helper that uses a tiny Rust binary project as the seed and mirrors it into rein with a fixed `RD-*` issue set.

From the rein repo root, after the `demo` daemon is already running:

```bash
export XDG_STATE_HOME="$PWD/.tmp/rein-state"

./scripts/bootstrap-rein-demo.sh \
  --state-home "$XDG_STATE_HOME" \
  --instance demo \
  --repo-dir /Users/earchibald/Projects/rein-demo
```

That command:

1. creates or reuses `/Users/earchibald/Projects/rein-demo`
2. uses `cargo new --bin` as the lightweight Rust seed when the repo does not exist yet
3. seeds rein project `rein-demo`
4. seeds the fixed repeatable issue set `RD-1` through `RD-5`

If you also want the matching GitHub repository created or reused, add `--github`:

```bash
./scripts/bootstrap-rein-demo.sh \
  --state-home "$XDG_STATE_HOME" \
  --instance demo \
  --repo-dir /Users/earchibald/Projects/rein-demo \
  --github
```

The bootstrap defaults to a private GitHub repo. Pass `--github-visibility public` if you intentionally want the demo repository to be public.

## 8. Fixed repeatable issue set

The seeded backlog is intentionally small and stable:

1. `RD-1` — scaffold the demo project with an agent prompt
2. `RD-2` — replace the hello-world print with a reusable greet helper
3. `RD-3` — accept a name argument for a personalized greeting
4. `RD-4` — add a README quickstart for build and run
5. `RD-5` — add a unit test for the greeting helper

This keeps the training loop lightweight enough to tear down and rebuild whenever the docs or operator flow change.
