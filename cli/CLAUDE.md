# cli/

Standalone Orva CLI codebase. Builds into a slim `orva` binary (~12 MB
stripped) that ships on Linux, macOS, and Windows × amd64/arm64 from
every release.

## Layout

```
cli/
├── cmd/orva/main.go      # slim CLI entry point (this is THE binary)
└── commands/             # Cobra subcommand library, package `commands`
    ├── root.go           # NewRoot() + RegisterClient(root) + Version var
    ├── helpers.go        # getClient(cmd), checkResponse, etc.
    ├── activity.go       # `orva activity`
    ├── channels.go       # `orva channels …`
    ├── completion.go     # `orva completion {bash|zsh|fish|powershell}`
    ├── cron.go           # `orva cron …`
    ├── deploy.go         # `orva deploy <path>`
    ├── functions.go      # `orva functions …`
    ├── invoke.go         # `orva invoke <name>`
    ├── jobs.go           # `orva jobs …`
    ├── keys.go           # `orva keys …`
    ├── kv.go             # `orva kv …`
    ├── login.go          # `orva login --endpoint --api-key`
    ├── logs.go           # `orva logs [--tail]` (SSE)
    ├── routes.go         # `orva routes …`
    ├── secrets.go        # `orva secrets …`
    ├── system.go         # `orva system health`
    ├── upgrade.go        # `orva upgrade` (self-update via go-selfupdate)
    ├── webhooks.go       # `orva webhooks …`
    └── commands_test.go  # command-tree + flag-presence tests
```

The HTTP client and `~/.orva/config.yaml` loader live at `internal/client/`
(repo-root internal package) so the server binary at `backend/cmd/orva/`
can also import the same client code through `cli/commands`.

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
- **Add new subcommands to `clientFactories` in `root.go`.** Otherwise
  they won't show up — and `cli/commands/commands_test.go::TestCommandTree`
  will fail in CI.

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
3. Add `<name>Cmd` to the `clientFactories` slice in `root.go`.
4. Add the leaf path to `commands_test.go::TestCommandTree`'s `paths` list.
5. Add any required flags to `TestRequiredFlagsPresent`.
6. Run `go test ./cli/commands/` — should pass.
7. Run `bash test/cli/command-tree.sh` — golden diff should remain zero.

## Self-update (`orva upgrade`)

Uses `github.com/creativeprojects/go-selfupdate`. The library queries
GitHub for the latest release matching `Filters: ["^orva-cli-"]`,
downloads the right OS/arch asset, verifies against `checksums.txt`,
and atomically replaces the running binary via rename-and-hide on
Windows / unlink-and-replace on Unix.

If the install path is not writable, `orva upgrade` exits non-zero with
a "re-run with `sudo orva upgrade`" hint. Never silently elevates.
