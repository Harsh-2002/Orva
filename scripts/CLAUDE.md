# scripts/

Support scripts for deployment and installation. None of these are called by the Makefile — they are runtime and installer artefacts.

| File | Purpose |
|---|---|
| `entrypoint.sh` | Docker container init: seeds rootfs from image on first start, refreshes adapters on every start, polls for bootstrap API key and writes `~/.orva/config.yaml`, then execs `orva serve` |
| `install.sh` | Universal installer (POSIX sh, idempotent). One script, three modes: `--docker` (writes a compose file + `up -d`), `--bare-metal` (distro deps → verified binary download → service user → systemd/OpenRC unit), `--cli-only`. Auto-detects Docker, the host distro, an existing install (offers upgrade), and the Docker runtime (refuses gVisor, accepts Kata). Interactive over `/dev/tty` with a TTY; flag/env-driven when piped. **Self-contained:** it embeds the systemd unit, OpenRC unit, and the generated `uninstall.sh` as heredocs (it ships as a single `curl \| sh` release asset, so it cannot read sibling files). Those embedded copies are the single source of truth (note the systemd unit must keep `RestartForceExitStatus=70` and the OpenRC unit must keep `supervisor="supervise-daemon"`: `orva backup restore` exits 70 on purpose, and with `Restart=on-failure` alone a successful restore leaves the service down) — there are deliberately no standalone `orva.service` / `orva.openrc` / `uninstall.sh` files to drift against. |
| `install-cli.sh` | POSIX sh installer that **only** installs the slim `orva` CLI to `/usr/local/bin/orva`. No service user, no systemd unit, no rootfs. Same intent as `install.sh --cli-only` but a smaller standalone script (linked directly from the README CLI-install instructions). Also supports macOS + shell completion, which `install.sh --cli-only` does not. |
| `install-cli.ps1` | Windows PowerShell CLI installer. Installs `orva.exe` to `%LOCALAPPDATA%\Programs\orva\` and adds it to the user PATH. |
| `build-rootfs.sh` | Builds nsjail root filesystem bundle for each runtime from a base container image. Requires Docker. Output tarballs go into the release image. |

## Gotchas

- `entrypoint.sh` **always overwrites** `adapter.js` / `adapter.py` from the image on every container start — this ensures runtime upgrades roll out even when the user mounts a persistent `orva-data` volume. The bundled SDK is *replaced*, not merged: `opt/orva/node_modules/orva/` is removed and re-copied, because a merge would leave files an older image shipped under names the current one no longer uses (`index.js`) alive in the volume forever.
- `install.sh` embeds the systemd/OpenRC units and `uninstall.sh`; the bare-metal install writes them to `$PREFIX/share/orva/scripts/` and the generated uninstaller to the same path. Edit the heredocs in `install.sh` — there is no separate unit file.
- `install.sh --cli-only` installs only the `orva` CLI binary to `/usr/local/bin/orva` — no systemd unit, no rootfs, no service user. Use this on operator laptops or CI runners that talk to a remote Orva over HTTPS.
- Mode/option precedence is flag > env > interactive prompt > default. Key knobs: `--version`/`ORVA_VERSION` (pin a release), `--dry-run`/`ORVA_INSTALL_DRYRUN=1` (detect only), `--no-pkg`/`ORVA_NO_PKG=1` (skip system packages), `--runtime`/`ORVA_DOCKER_RUNTIME` (force the Docker runtime). There is **no** checksum-bypass env var (no `ORVA_SKIP_VERIFY` or similar).
- Downloaded assets (orva, nsjail, rootfs, CLI) are SHA-256 verified against `checksums.txt`. Missing checksum files, missing asset entries, and digest mismatches all abort the install; there is no fail-open path.
- `build-rootfs.sh` produces large tarballs (~hundreds of MB); run only when updating the rootfs base image or adding system libraries.
- Cross-distro installer tests: `test/install/matrix.sh` (fast, unprivileged — shellcheck + POSIX parse + dry-run + real CLI install across 6 distros) and the privileged systemd-in-docker harness under `test/install/`. CI: the installer suite in `.github/workflows/ci.yml`.
