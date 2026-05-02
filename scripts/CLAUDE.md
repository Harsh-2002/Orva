# scripts/

Support scripts for deployment and installation. None of these are called by the Makefile — they are runtime and installer artefacts.

| File | Purpose |
|---|---|
| `entrypoint.sh` | Docker container init: seeds rootfs from image on first start, refreshes adapters on every start, polls for bootstrap API key and writes `~/.orva/config.yaml`, then execs `orva serve` |
| `install.sh` | Bare-metal installer (POSIX sh, idempotent). Detects distro, installs system deps, downloads release binary, sets up service user, installs systemd/OpenRC unit. |
| `build-rootfs.sh` | Builds nsjail root filesystem bundle for each runtime from a base container image. Requires Docker. Output tarballs go into the release image. |
| `orva.service` | systemd unit file for `orva serve` on bare-metal Linux |
| `orva.openrc` | OpenRC unit file for Alpine Linux |
| `uninstall.sh` | Removes binary, service, and (optionally) data directory |

## Gotchas

- `entrypoint.sh` **always overwrites** `adapter.js` / `adapter.py` from the image on every container start — this ensures runtime upgrades roll out even when the user mounts a persistent `orva_data` volume.
- `install.sh --cli-only` installs only the `orva` CLI binary to `/usr/local/bin/orva` — no systemd unit, no rootfs, no service user. Use this on operator laptops or CI runners that talk to a remote Orva over HTTPS.
- `install.sh` accepts `ORVA_VERSION=vX.Y.Z` to pin a specific release, `ORVA_INSTALL_DRYRUN=1` to detect without installing, and `ORVA_NO_PKG=1` to skip system package installation.
- `build-rootfs.sh` produces large tarballs (~hundreds of MB); run only when updating the rootfs base image or adding system libraries.
