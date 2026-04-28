VERSION ?= 0.1.0
BINARY  = orva
BUILD   = build

.PHONY: build test lint clean ui embed build-all dev adapters-embed

# ── Backend ──────────────────────────────────────────────

# Copy adapter sources into cmd/orva/adapters/ so //go:embed has them at
# build time. Keeps runtimes/ as the source-of-truth directory (shared
# with Dockerfile COPY paths) without needing Go's embed to cross
# package boundaries.
adapters-embed:
	@rm -rf cmd/orva/adapters
	@mkdir -p cmd/orva/adapters/node22 cmd/orva/adapters/node24 \
	          cmd/orva/adapters/python313 cmd/orva/adapters/python314
	@cp runtimes/node22/adapter.js    cmd/orva/adapters/node22/adapter.js
	@cp runtimes/node24/adapter.js    cmd/orva/adapters/node24/adapter.js
	@cp runtimes/python313/adapter.py cmd/orva/adapters/python313/adapter.py
	@cp runtimes/python314/adapter.py cmd/orva/adapters/python314/adapter.py

build: adapters-embed
	@mkdir -p $(BUILD)
	go build -ldflags="-s -w -X main.Version=$(VERSION)" -o $(BUILD)/$(BINARY) ./cmd/orva

test:
	go test -count=1 ./...

lint:
	go vet ./...

# ── Frontend ─────────────────────────────────────────────

ui:
	cd ui && npm install && npm run build

embed: ui
	rm -rf internal/server/ui_dist
	cp -r ui/dist internal/server/ui_dist

# ── All ──────────────────────────────────────────────────

build-all: embed build

dev:
	cd ui && npm run dev &
	go run ./cmd/orva serve

clean:
	rm -rf $(BUILD)
	rm -rf internal/server/ui_dist
	rm -rf ui/dist
