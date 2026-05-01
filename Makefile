VERSION ?= 0.1.0
BINARY  = orva
BUILD   = build

.PHONY: build test lint clean ui embed build-all dev adapters-embed cli cli-all

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

build: adapters-embed
	@mkdir -p $(BUILD)
	cd backend && go build -ldflags="-s -w -X main.Version=$(VERSION)" -o ../$(BUILD)/$(BINARY) ./cmd/orva

test:
	cd backend && go test -count=1 ./...

lint:
	cd backend && go vet ./...

ui:
	cd frontend && npm install && npm run build

embed: ui
	rm -rf backend/internal/server/ui_dist
	cp -r frontend/dist backend/internal/server/ui_dist

build-all: embed build

dev:
	cd frontend && npm run dev &
	cd backend && go run ./cmd/orva serve

# Standalone CLI binary. Same Go program as the daemon (orva and orva-cli
# share `./cmd/orva`), but built without the embedded UI/rootfs assumptions
# and named distinctly so release artifacts don't collide with the server.
# CGO disabled + -trimpath + stripped symbols → fully static, ships anywhere.
cli: adapters-embed
	@mkdir -p $(BUILD)
	cd backend && CGO_ENABLED=0 go build \
	  -trimpath \
	  -ldflags='-s -w -X main.Version=$(VERSION)' \
	  -o ../$(BUILD)/orva-cli ./cmd/orva

# Cross-compile the CLI for every target we ship as a release asset.
# Output naming matches the GitHub release-asset convention so install.sh
# (and the README curl recipe) can point straight at /releases/latest/download/.
cli-all: adapters-embed
	@mkdir -p $(BUILD)
	@for target in linux/amd64 linux/arm64 darwin/arm64; do \
	  os=$${target%/*}; arch=$${target#*/}; \
	  echo ">> building orva-cli-$$os-$$arch"; \
	  (cd backend && CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build \
	    -trimpath \
	    -ldflags='-s -w -X main.Version=$(VERSION)' \
	    -o ../$(BUILD)/orva-cli-$$os-$$arch ./cmd/orva) || exit 1; \
	done

clean:
	rm -rf $(BUILD)
	rm -rf backend/internal/server/ui_dist
	rm -rf frontend/dist
