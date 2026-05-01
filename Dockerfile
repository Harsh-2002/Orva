# syntax=docker/dockerfile:1.6
ARG VERSION=0.1.0

FROM node:22-alpine AS ui
WORKDIR /ui
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM golang:1.25-bookworm AS go
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=ui /ui/dist ./internal/server/ui_dist
ARG VERSION
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w -X main.Version=${VERSION}" \
      -o /out/orva ./cmd/orva

FROM debian:bookworm-slim AS nsjail
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates git make gcc g++ autoconf bison flex libtool pkg-config \
      libprotobuf-dev libnl-route-3-dev protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 https://github.com/google/nsjail.git /nsjail \
    && cd /nsjail && make -j"$(nproc)" && strip nsjail

FROM node:22-slim AS rootfs-node22
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* /usr/share/doc /usr/share/man /usr/share/locale \
    && mkdir -p /opt/orva /opt/orva/node_modules/orva /code
COPY backend/runtimes/node22/adapter.js /opt/orva/adapter.js
COPY backend/runtimes/node22/orva.js    /opt/orva/node_modules/orva/index.js
RUN echo '{"name":"orva","version":"0.2.0","main":"index.js"}' > /opt/orva/node_modules/orva/package.json

FROM node:24-slim AS rootfs-node24
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* /usr/share/doc /usr/share/man /usr/share/locale \
    && mkdir -p /opt/orva /opt/orva/node_modules/orva /code
COPY backend/runtimes/node24/adapter.js /opt/orva/adapter.js
COPY backend/runtimes/node24/orva.js    /opt/orva/node_modules/orva/index.js
RUN echo '{"name":"orva","version":"0.2.0","main":"index.js"}' > /opt/orva/node_modules/orva/package.json

FROM python:3.13-slim AS rootfs-python313
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* /usr/share/doc /usr/share/man /usr/share/locale \
    && find /usr/local/lib/python3.13 -depth -type d -name __pycache__ -exec rm -rf {} + \
    && find /usr/local/lib/python3.13 -depth -type d -name tests -exec rm -rf {} + \
    && mkdir -p /opt/orva /code
COPY backend/runtimes/python313/adapter.py /opt/orva/adapter.py
COPY backend/runtimes/python313/orva.py    /opt/orva/orva.py

FROM python:3.14-slim AS rootfs-python314
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* /usr/share/doc /usr/share/man /usr/share/locale \
    && find /usr/local/lib/python3.14 -depth -type d -name __pycache__ -exec rm -rf {} + \
    && find /usr/local/lib/python3.14 -depth -type d -name tests -exec rm -rf {} + \
    && mkdir -p /opt/orva /code
COPY backend/runtimes/python314/adapter.py /opt/orva/adapter.py
COPY backend/runtimes/python314/orva.py    /opt/orva/orva.py

FROM debian:bookworm-slim
ARG VERSION

LABEL org.opencontainers.image.title="Orva" \
      org.opencontainers.image.description="Self-hosted serverless function platform — Node.js + Python on nsjail" \
      org.opencontainers.image.source="https://github.com/Harsh-2002/Orva" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tini curl libprotobuf32 libnl-route-3-200 libcap2-bin \
      nftables \
      python3-pip nodejs npm \
    && rm -rf /var/lib/apt/lists/* /usr/share/doc /usr/share/man /usr/share/locale \
    && mkdir -p /var/lib/orva/functions

COPY --from=nsjail /nsjail/nsjail /usr/local/bin/nsjail
# /usr/local/bin/orva is dual-purpose: `orva serve` is the daemon (CMD below),
# every other subcommand is the standalone CLI. `docker exec orva orva …`
# uses the same binary; the entrypoint pre-writes ~/.orva/config.yaml so
# common commands work without re-passing --endpoint / --api-key.
COPY --from=go /out/orva /usr/local/bin/orva
COPY --from=rootfs-node22    / /opt/orva/rootfs/node22/
COPY --from=rootfs-node24    / /opt/orva/rootfs/node24/
COPY --from=rootfs-python313 / /opt/orva/rootfs/python313/
COPY --from=rootfs-python314 / /opt/orva/rootfs/python314/
COPY scripts/entrypoint.sh /usr/local/bin/orva-entrypoint
RUN chmod +x /usr/local/bin/orva-entrypoint

WORKDIR /var/lib/orva
EXPOSE 8443

ENV ORVA_DATA_DIR=/var/lib/orva

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD curl -fsS http://localhost:8443/api/v1/system/health || exit 1

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/orva-entrypoint"]
CMD ["/usr/local/bin/orva", "serve"]
