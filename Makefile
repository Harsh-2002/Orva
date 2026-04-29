VERSION ?= 0.1.0
BINARY  = orva
BUILD   = build

.PHONY: build test lint clean ui embed build-all dev adapters-embed

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

clean:
	rm -rf $(BUILD)
	rm -rf backend/internal/server/ui_dist
	rm -rf frontend/dist
