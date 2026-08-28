# syntax=docker/dockerfile:1.6
# Build identity — wired through to internal/version via -X ldflags AND
# stamped as OCI image labels for `docker inspect`. The release workflow
# supplies all three; bare `docker build` falls back to safe defaults.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

FROM node:24-alpine AS ui
WORKDIR /ui
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-bookworm AS go
WORKDIR /src
# go.mod / go.sum live at the repo root since the v2026.05.12 CLI split.
COPY go.mod go.sum ./
RUN go mod download
# Copy every Go tree the build needs: backend/, cli/, internal/.
COPY backend/ ./backend/
COPY cli/ ./cli/
COPY internal/ ./internal/
COPY --from=ui /ui/dist ./backend/internal/server/ui_dist
ARG VERSION
ARG COMMIT
ARG BUILD_TIME
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/Harsh-2002/Orva/backend/internal/version.Version=${VERSION} \
        -X github.com/Harsh-2002/Orva/backend/internal/version.Commit=${COMMIT} \
        -X github.com/Harsh-2002/Orva/backend/internal/version.BuildTime=${BUILD_TIME}" \
      -o /out/orva ./backend/cmd/orva

FROM debian:bookworm-slim AS nsjail
ARG NSJAIL_REF=5ebcc30bef4af60d6e28f012dd8bf7b99b8b0acf
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates git make gcc g++ autoconf bison flex libtool pkg-config \
      libprotobuf-dev libnl-route-3-dev protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --filter=blob:none https://github.com/google/nsjail.git /nsjail \
    && git -C /nsjail checkout "$NSJAIL_REF" \
    && cd /nsjail && make -j"$(nproc)" && strip nsjail

# Orva offers two runtimes, latest-stable only: node (Node.js 24) and
# python (Python 3.14). Bump the base image here to track a newer stable.
FROM node:24-slim AS rootfs-node
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* /usr/share/doc /usr/share/man /usr/share/locale \
    && mkdir -p /opt/orva /opt/orva/node_modules/orva /code
# The SDK's own package.json is canonical (main ./orva.js, types ./orva.d.ts),
# so every file must keep its real name — a rename here breaks `main`.
COPY backend/runtimes/node/adapter.js   /opt/orva/adapter.js
COPY backend/runtimes/node/orva.js      /opt/orva/node_modules/orva/orva.js
COPY backend/runtimes/node/orva.d.ts    /opt/orva/node_modules/orva/orva.d.ts
COPY backend/runtimes/node/package.json /opt/orva/node_modules/orva/package.json

FROM python:3.14-slim AS rootfs-python
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* /usr/share/doc /usr/share/man /usr/share/locale \
    && find /usr/local/lib/python3.14 -depth -type d -name __pycache__ -exec rm -rf {} + \
    && find /usr/local/lib/python3.14 -depth -type d -name tests -exec rm -rf {} + \
    && mkdir -p /opt/orva /code
COPY backend/runtimes/python/adapter.py /opt/orva/adapter.py
COPY backend/runtimes/python/orva.py    /opt/orva/orva.py
COPY backend/runtimes/python/py.typed   /opt/orva/py.typed

FROM debian:bookworm-slim
ARG VERSION
ARG COMMIT
ARG BUILD_TIME
ARG IMAGE_REF

LABEL org.opencontainers.image.title="Orva" \
      org.opencontainers.image.description="Self-hosted serverless function platform — Node.js + Python on nsjail" \
      org.opencontainers.image.source="https://github.com/Harsh-2002/Orva" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_TIME}"

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tini curl libprotobuf32 libnl-route-3-200 libcap2-bin \
      iproute2 \
      python3-pip nodejs npm \
    && rm -rf /var/lib/apt/lists/* /usr/share/doc /usr/share/man /usr/share/locale \
    && mkdir -p /var/lib/orva/functions

COPY --from=nsjail /nsjail/nsjail /usr/local/bin/nsjail
# /usr/local/bin/orva is dual-purpose: `orva serve` is the daemon (CMD below),
# every other subcommand is the standalone CLI. `docker exec orva orva …`
# uses the same binary; the entrypoint pre-writes ~/.orva/config.yaml so
# common commands work without re-passing --endpoint / --api-key.
COPY --from=go /out/orva /usr/local/bin/orva
COPY --from=rootfs-node   / /opt/orva/rootfs/node/
COPY --from=rootfs-python / /opt/orva/rootfs/python/
COPY scripts/entrypoint.sh /usr/local/bin/orva-entrypoint
RUN chmod +x /usr/local/bin/orva-entrypoint

WORKDIR /var/lib/orva
EXPOSE 8443

ENV ORVA_DATA_DIR=/var/lib/orva
# Only the publishing pipeline knows the reference this image is pushed under;
# unset (any local build) makes /system/health report no image, not a wrong one.
ENV ORVA_IMAGE=${IMAGE_REF}

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD curl -fsS http://localhost:8443/api/v1/system/health || exit 1

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/orva-entrypoint"]
CMD ["/usr/local/bin/orva", "serve"]
