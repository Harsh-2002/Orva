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

# The slim CLI does NOT import backend/internal/version (CONTRACT.md forbids it
# importing backend/internal at all), so the server's -X targets above are
# silently ignored there and a locally built CLI reports "dev". It carries its
# own `main.Version` instead — the same variable release.yml stamps.
CLI_LDFLAGS = -s -w -X main.Version=$(VERSION)

.PHONY: build test lint clean ui embed build-all dev adapters-embed docs-embed cli cli-all

# Sync the canonical docs reference markdown into both consumers:
# - backend/internal/mcp/reference.md → embedded by the get_orva_docs MCP
#   tool via //go:embed
# - frontend/public/docs.md → served by Vite at /docs.md so the Docs page's
#   "Copy as Markdown" button reads the same bytes
# - cli/commands/reference.md → embedded by the `orva docs` command via
#   //go:embed so the slim CLI renders the same reference offline
# Single source of truth lives at docs/reference.md. Edit it, run
# `make docs-embed`, and UI + MCP + CLI serve the new content.
docs-embed:
	@cp docs/reference.md backend/internal/mcp/reference.md
	@cp docs/reference.md frontend/public/docs.md
	@cp docs/reference.md cli/commands/reference.md

# Copy adapter sources + bundled SDK into backend/cmd/orva/adapters/ so
# //go:embed has them at build time. Keeps backend/runtimes/ as the
# source-of-truth directory (shared with Dockerfile COPY paths).
# Also copies the bundled Orva SDK module (kv / invoke / jobs / tracing).
adapters-embed:
	@rm -rf backend/cmd/orva/adapters
	@mkdir -p backend/cmd/orva/adapters/node backend/cmd/orva/adapters/python
	@cp backend/runtimes/node/adapter.js     backend/cmd/orva/adapters/node/adapter.js
	@cp backend/runtimes/python/adapter.py   backend/cmd/orva/adapters/python/adapter.py
	@cp backend/runtimes/node/orva.js        backend/cmd/orva/adapters/node/orva.js
	@cp backend/runtimes/python/orva.py      backend/cmd/orva/adapters/python/orva.py
	@# Ship .d.ts + package.json so TS handlers get types;
	@# py.typed marks the Python module as fully typed for static checkers.
	@cp backend/runtimes/node/orva.d.ts      backend/cmd/orva/adapters/node/orva.d.ts
	@cp backend/runtimes/node/package.json   backend/cmd/orva/adapters/node/package.json
	@cp backend/runtimes/python/py.typed     backend/cmd/orva/adapters/python/py.typed

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

# Slim standalone CLI. Built from the dedicated ./cli/cmd/orva entry point
# which imports only the cli/commands library — no server packages, no
# embedded UI/adapters/MCP. It embeds the CLI reference docs and currently
# ships at roughly 20 MB.
# CGO disabled + -trimpath + stripped symbols → fully static.
cli:
	@mkdir -p $(BUILD)
	CGO_ENABLED=0 go build \
	  -trimpath \
	  -ldflags="$(CLI_LDFLAGS)" \
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
	    -ldflags="$(CLI_LDFLAGS)" \
	    -o $(BUILD)/orva-cli-$$os-$$arch$$ext ./cli/cmd/orva || exit 1; \
	done

clean:
	rm -rf $(BUILD)
	rm -rf backend/internal/server/ui_dist
	rm -rf frontend/dist
