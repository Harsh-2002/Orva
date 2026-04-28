# Orva

Run Node.js and Python functions on your own server.

## Install

### Linux (any distro)

```bash
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sudo sh
```

Tested on Ubuntu, Debian, Alpine, Rocky/RHEL, Fedora, Arch, openSUSE.

### Docker

```bash
docker run -d --name orva -p 8443:8443 \
  --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined \
  --security-opt systempaths=unconfined \
  -v orva-data:/var/lib/orva \
  ghcr.io/harsh-2002/orva:latest
```

Or `docker compose up -d` from this repo.

After install, open `http://localhost:8443` and complete onboarding.

## Layout

```
backend/    Go server, sandbox runtime, SQLite
frontend/   Vue 3 dashboard
scripts/    install.sh / uninstall.sh / systemd unit
docs/       SECURITY.md, ERRORS.md, CAPACITY.md
```

## Build from source

```bash
make build-all     # frontend + backend → build/orva
make dev           # frontend on :5173, backend on :8443
make test          # go test ./...
```

## License

Apache-2.0
