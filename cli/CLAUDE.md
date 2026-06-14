# cli/

Standalone Orva CLI codebase. Builds into a slim `orva` binary (~20 MB
stripped — `orva chat`/`orva docs` pull in glamour/chroma for terminal
markdown) that ships on Linux, macOS, and Windows × amd64/arm64 from
every release.

## Layout

```
cli/
├── cmd/orva/main.go      # slim CLI entry point (this is THE binary)
└── commands/             # Cobra subcommand library, package `commands`
    ├── root.go           # NewRoot() + RegisterClient(root) + Version var + global flags
    ├── helpers.go        # getClient(cmd), checkResponse, etc.
    ├── output.go         # shared output framework (stdout/stderr split, table|json, color, confirm)
    ├── activity.go       # `orva activity`
    ├── backup.go         # `orva backup download/restore`
    ├── channels.go       # `orva channels …`
    ├── chat.go           # `orva chat` (interactive AI REPL + one-shot -p, SSE)
    ├── completion.go     # `orva completion {bash|zsh|fish|powershell}`
    ├── completions.go    # dynamic shell completions (fn names, runtimes, models)
    ├── cron.go           # `orva cron …`
    ├── deploy.go         # `orva deploy <path> [--follow]` (--watch = deprecated alias)
    ├── deployments.go    # `orva deployments list/get/logs`
    ├── diff.go           # `orva diff <function>` (unified diff between deployments)
    ├── dns.go            # `orva dns get/set`
    ├── docs.go           # `orva docs` (renders embedded docs/reference.md)
    ├── executions.go     # `orva executions list/get/logs/delete/prune/replay`
    ├── firewall.go       # `orva firewall list/add/enable/disable/delete/resolve`
    ├── fixtures.go       # `orva fixtures list/get/save/delete/test`
    ├── functions.go      # `orva functions …`
    ├── invoke.go         # `orva invoke <name>` (--body/--stream/--route/-H/-X)
    ├── jobs.go           # `orva jobs …`
    ├── keys.go           # `orva keys …`
    ├── kv.go             # `orva kv list/get/put/delete/incr/cas`
    ├── login.go          # `orva login --endpoint --api-key [--test]`
    ├── logs.go           # `orva logs [--follow]` (SSE)
    ├── pool.go           # `orva pool get/set` (per-fn warm-pool autoscaler)
    ├── rollback.go       # `orva rollback <fn> [deployment-id|--code-hash]`
    ├── routes.go         # `orva routes …`
    ├── secrets.go        # `orva secrets …`
    ├── system.go         # `orva system health/metrics/db-stats/storage/vacuum`
    ├── traces.go         # `orva traces list/get/baseline`
    ├── upgrade.go        # `orva upgrade` (self-update via go-selfupdate)
    ├── webhooks.go       # `orva webhooks …`
    ├── commands_test.go  # command-tree + flag-presence tests
    ├── chat_test.go      # chat SSE drive + approval-flow + idle/EOF tests (httptest)
    ├── upgrade_test.go   # `orva upgrade` decision logic + asset-filter tests
    ├── reference.md      # GENERATED — embedded by docs.go (make docs-embed)
    └── theme/            # lipgloss color palette (theme.New(enabled))
```

The HTTP client and `~/.orva/config.yaml` loader live at `internal/client/`
(repo-root internal package) so the server binary at `backend/cmd/orva/`
can also import the same client code through `cli/commands`.

## Output framework (`output.go`)

All commands share one output layer that enforces the **data → stdout,
status → stderr** contract: response bodies and list/`get` payloads print
to stdout; progress, success lines, timings, and confirmation prompts go
to stderr. This keeps `orva <cmd> | jq` clean regardless of verbosity.
It centralizes the global persistent flags wired up in `root.go` —
`-o/--output` (`table`|`json`), `-q/--quiet` (silence stderr status),
`--no-color` (also honors `NO_COLOR` and auto-off on non-TTY), and
`-y/--yes` (skip the interactive confirm; destructive ops refuse on a
non-TTY without it). New commands should render through this layer
rather than calling `fmt.Print*` directly, so the streams and formats
stay consistent.

## Color & theming (`theme/`)

Color lives in one place: `cli/commands/theme` wraps `charmbracelet/lipgloss`
(pure-Go, auto-degrades truecolor → 256 → 16 and adapts to light/dark
backgrounds). `theme.New(enabled)` returns a `*Styles`; when `enabled` is false
every style is a no-op pass-through that emits no ANSI. Commands get theirs via
`styles(cmd)` in `output.go`, which gates on the same `colorEnabled(cmd)` chain
(`--no-color` → `NO_COLOR` → JSON mode → non-TTY) — so color control stays a
single decision. `okf()` and the `diff` colorizer render through the theme.
**Don't** push ANSI into `tabwriter` cells: the escape bytes break column
alignment, so table bodies stay uncolored by design.

## AI chat (`orva chat`)

`chat.go` is the terminal front end to the AI assistant — the same agent the
dashboard drives, over `POST /api/v1/ai/chat` (SSE) with the CLI's existing API
key (the AI endpoints accept it; they need `admin`). Interactive REPL by
default, or one-shot with `-p` (supports `@file`/`@-`). It reuses `consumeSSE`
for the wire format and `client.Send(Request{..., Ctx, NoTimeout})` for a
cancellable streaming POST (Ctrl-C aborts the turn, not the process).

- **Rendering:** assistant text streams raw to stdout live; on a TTY (not
  `--raw`/`ORVA_CHAT_NO_GLAMOUR`) the message is re-rendered with `glamour` by
  erasing the streamed block and reprinting — guarded by terminal-size checks,
  degrades to raw if anything is uncertain. Thinking + tool status go to stderr;
  stdout stays clean for piping.
- **Approvals:** the CLI never sets the policy; it reacts to `requires_approval`
  /`awaiting_approval`. Write tools prompt `[y/N]`; non-interactive without
  `--auto-approve` fails closed. The global `-y` is intentionally **not** honored
  for AI tool approval.
- **Selection:** `--provider/--model/--thinking` are per-session overrides;
  `/model` and `/thinking` persist via `PUT /api/v1/ai/selection`.

Markdown rendering uses `glamour` (pulls `chroma`), which adds ~8 MB to the slim
binary (~12 MB → ~20 MB). The same `docs/reference.md` is embedded for
`orva docs` via `//go:embed reference.md`; `make docs-embed` keeps that copy in
sync alongside the MCP and frontend copies.

## Build commands

```bash
# Current OS, stripped + static (CGO disabled). Output: build/orva.
make cli

# Cross-compile all six release targets (Linux/macOS/Windows × amd64/arm64).
# Outputs: build/orva-cli-<os>-<arch>[.exe].
make cli-all
```

`make cli` produces the slim CLI. The server binary (server + CLI bundled)
is built by `make build` from `./backend/cmd/orva`.

## Constraints

- **No `backend/internal/...` imports.** That is what keeps the slim CLI
  slim. If a subcommand needs a utility that currently lives under
  `backend/internal/...`, lift it to repo-root `internal/...` first
  (precedent: `internal/ids/`, `internal/client/`).
- **No CGO.** All release binaries are pure-Go static builds. The CLI
  must work on a fresh Alpine container without `apk add libc6-compat`.
- **No `os.Exit` inside subcommand bodies.** Use `RunE` and return errors
  so tests can observe failures. The existing `Run`-style commands are
  pre-refactor; new commands should use `RunE`.
- **Register new top-level commands in `RegisterClient` (`root.go`).** Add the
  `*Cmd` var to the `root.AddCommand(...)` list and give it a group in
  `commandGroups()`. Otherwise it won't show up — and
  `cli/commands/commands_test.go::TestCommandTree` will fail in CI.

## Testing

```bash
# Go unit tests (command tree + flag presence + version wiring).
go test ./cli/commands/

# Cross-build matrix + binary-format / size sanity.
bash test/cli/build-matrix.sh

# Command-tree golden diff: slim CLI and server binary must expose the
# SAME client-side surface.
bash test/cli/command-tree.sh

# End-to-end installer test inside a privileged Docker container.
bash test/cli/install-cli-test.sh ubuntu24

# Upgrade round-trip (install vN-1, run `orva upgrade`, verify vN).
bash test/cli/upgrade-test.sh
```

CI runs every script in `.github/workflows/cli-e2e.yml` on push, plus a
weekly schedule to catch GH-API / release-asset drift.

## Adding a new subcommand

1. Create `cli/commands/<name>.go` with `package commands`.
2. Define `var <name>Cmd = &cobra.Command{…}` + an `init()` for flags.
3. Add `<name>Cmd` to the `root.AddCommand(...)` list in `RegisterClient` (`root.go`)
   and assign it a group in `commandGroups()`.
4. Add the leaf path to `commands_test.go::TestCommandTree`'s `paths` list.
5. Add any required flags to `TestRequiredFlagsPresent`.
6. (If a subcommand takes a function name) wire fn-name completion in
   `wireCompletions` (`completions.go`).
7. Run `go test ./cli/commands/` — should pass.
8. Run `bash test/cli/command-tree.sh` — golden diff should remain zero.

## Self-update (`orva upgrade`)

Uses `github.com/creativeprojects/go-selfupdate`. The library queries
GitHub for the latest release, downloads the OS/arch asset matching
`Filters: ["^orva-cli-<os>-<arch>"]` (pinned to the exact platform token
by `upgradeAssetFilter` — a loose `^orva-cli-` filter let it fall back to
arch-only matching and pick a wrong-OS binary → intermittent "exec format
error"), verifies against `checksums.txt`, and atomically replaces the
running binary via rename-and-hide on Windows / unlink-and-replace on Unix.

**Same-tag re-cut detection (checksum staleness).** Releases use date tags
(`vYYYY.MM.DD`) and are re-cut under the *same* tag when we ship more than once
a day, so an equal tag can point at a different published binary. A pure semver
compare would tell a morning-upgraded user "already the latest" after an
afternoon re-cut. So when the latest tag is **not strictly newer**, `orva
upgrade` also compares the running binary's SHA-256 against the published
checksum for its platform asset (`latest.AssetName` looked up in the
`latest.ValidationAssetURL` checksums file). A mismatch ⇒ a fresh build under
the same tag ⇒ reinstall. Decision lives in `upgradeAction`; the checksum probe
is `remoteBuildDiffers` (best-effort: any network/parse failure returns
`known=false` and falls back to version-only, so a flaky network never blocks or
hangs the upgrade — the fetch is bounded to 10s). Note: running `orva upgrade`
against the full server binary (`orva-<os>-<arch>`) sees a mismatch vs the CLI
asset and offers to replace it — same direction as a version bump; `orva
upgrade` is the CLI self-update path (servers update via install.sh / Docker).

If the install path is not writable, `orva upgrade` exits non-zero with
a "re-run with `sudo orva upgrade`" hint. Never silently elevates.
