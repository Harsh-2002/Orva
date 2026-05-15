# Build identity — stamped into the binary via -X ldflags and surfaced
# at /api/v1/system/health + Settings → Build info in the dashboard. The
# three values flow Makefile → Dockerfile → release.yml so a single
# source of truth feeds bare-metal, Docker, and CI builds alike.
#
# VERSION:    git tag when tagged, otherwise `git describe` (e.g.
#             "v2026.05.15-3-g1be3399-dirty"). Default "dev" if no git.
# COMMIT:     short SHA of the build's HEAD commit.
# BUILD_TIME: wall-clock at build moment (RFC3339 UTC) — useful for
#             telling "is this image the one CI just produced?".
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BINARY      = orva
BUILD       = build

# The version package lives at backend/internal/version. Go's ldflags
# fail silently when the target path is wrong — keep these three lines
# the only place this string appears so renames stay in sync.
LDFLAGS = -s -w \
  -X github.com/Harsh-2002/Orva/backend/internal/version.Version=$(VERSION) \
  -X github.com/Harsh-2002/Orva/backend/internal/version.Commit=$(COMMIT) \
  -X github.com/Harsh-2002/Orva/backend/internal/version.BuildTime=$(BUILD_TIME)

.PHONY: build test lint clean ui embed build-all dev adapters-embed docs-embed cli cli-all

# Sync the canonical docs reference markdown into both consumers:
# - backend/internal/mcp/reference.md → embedded by the get_orva_docs MCP
#   tool via //go:embed
# - frontend/public/docs.md → served by Vite at /docs.md so the Docs page's
#   "Copy as Markdown" button reads the same bytes
# Single source of truth lives at docs/reference.md. Edit it, run
# `make docs-embed`, and both UI + MCP serve the new content.
docs-embed:
	@cp docs/reference.md backend/internal/mcp/reference.md
	@cp docs/reference.md frontend/public/docs.md

# Copy adapter sources + bundled SDK into backend/cmd/orva/adapters/ so
# //go:embed has them at build time. Keeps backend/runtimes/ as the
# source-of-truth directory (shared with Dockerfile COPY paths).
# Also copies the v0.2 orva SDK module (kv / invoke / jobs).
adapters-embed:
	@rm -rf backend/cmd/orva/adapters
	@mkdir -p backend/cmd/orva/adapters/node22 backend/cmd/orva/adapters/node24 \
	          backend/cmd/orva/adapters/python313 backend/cmd/orva/adapters/python314
	@cp backend/runtimes/node22/adapter.js    backend/cmd/orva/adapters/node22/adapter.js
	@cp backend/runtimes/node24/adapter.js    backend/cmd/orva/adapters/node24/adapter.js
	@cp backend/runtimes/python313/adapter.py backend/cmd/orva/adapters/python313/adapter.py
	@cp backend/runtimes/python314/adapter.py backend/cmd/orva/adapters/python314/adapter.py
	@cp backend/runtimes/node22/orva.js       backend/cmd/orva/adapters/node22/orva.js
	@cp backend/runtimes/node24/orva.js       backend/cmd/orva/adapters/node24/orva.js
	@cp backend/runtimes/python313/orva.py    backend/cmd/orva/adapters/python313/orva.py
	@cp backend/runtimes/python314/orva.py    backend/cmd/orva/adapters/python314/orva.py
	@# v0.6 SDK: ship .d.ts + package.json so TS handlers get types;
	@# py.typed marks the Python module as fully typed for static checkers.
	@cp backend/runtimes/node22/orva.d.ts     backend/cmd/orva/adapters/node22/orva.d.ts
	@cp backend/runtimes/node22/package.json  backend/cmd/orva/adapters/node22/package.json
	@cp backend/runtimes/node24/orva.d.ts     backend/cmd/orva/adapters/node24/orva.d.ts
	@cp backend/runtimes/node24/package.json  backend/cmd/orva/adapters/node24/package.json
	@cp backend/runtimes/python313/py.typed   backend/cmd/orva/adapters/python313/py.typed
	@cp backend/runtimes/python314/py.typed   backend/cmd/orva/adapters/python314/py.typed

build: adapters-embed docs-embed
	@mkdir -p $(BUILD)
	go build -ldflags="$(LDFLAGS)" -o $(BUILD)/$(BINARY) ./backend/cmd/orva

test:
	go test -count=1 ./...

lint:
	go vet ./...

ui: docs-embed
	cd frontend && npm install && npm run build

embed: ui
	rm -rf backend/internal/server/ui_dist
	cp -r frontend/dist backend/internal/server/ui_dist

build-all: embed build

dev:
	cd frontend && npm run dev &
	go run ./backend/cmd/orva serve

# Standalone CLI binary. Same Go program as the daemon (orva and orva-cli
# share `./cmd/orva`), but built without the embedded UI/rootfs assumptions
# and named distinctly so release artifacts don't collide with the server.
# CGO disabled + -trimpath + stripped symbols → fully static, ships anywhere.
# Slim standalone CLI. Built from the dedicated ./cli/cmd/orva entry point
# which imports only the cli/commands library — no server packages, no
# embedded UI/adapters/docs/MCP. Targets ~6–9 MB vs the ~20 MB server.
# CGO disabled + -trimpath + stripped symbols → fully static.
cli:
	@mkdir -p $(BUILD)
	CGO_ENABLED=0 go build \
	  -trimpath \
	  -ldflags="$(LDFLAGS)" \
	  -o $(BUILD)/orva ./cli/cmd/orva

# Cross-compile the slim CLI for every release-asset target.
# Output naming matches the GitHub release-asset convention so install-cli.sh
# (and the README curl recipe) can point straight at /releases/latest/download/.
# Windows targets get the .exe suffix.
cli-all:
	@mkdir -p $(BUILD)
	@for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
	  os=$${target%/*}; arch=$${target#*/}; \
	  ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	  echo ">> building orva-cli-$$os-$$arch$$ext"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build \
	    -trimpath \
	    -ldflags="$(LDFLAGS)" \
	    -o $(BUILD)/orva-cli-$$os-$$arch$$ext ./cli/cmd/orva || exit 1; \
	done

clean:
	rm -rf $(BUILD)
	rm -rf backend/internal/server/ui_dist
	rm -rf frontend/dist
