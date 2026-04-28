<div align="center" style="background:#12111C;color:#FFFFFF;padding:24px;border-radius:14px;border:1px solid #2a2935;">
  <div style="font-size:28px;font-weight:700;letter-spacing:0.5px;">Orva API</div>
  <div style="margin-top:8px;color:#b3afc7;">Minimal, production-ready docs for the self-hosted serverless platform</div>
  <div style="margin-top:12px;">
    <span style="background:#553F83;color:#FFFFFF;padding:4px 10px;border-radius:999px;font-size:12px;">v0.5.0</span>
    <span style="background:#1a1923;color:#FFFFFF;padding:4px 10px;border-radius:999px;font-size:12px;border:1px solid #2a2935;">SSE Enabled</span>
  </div>
</div>

---

## Contents

- [Base URL](#base-url)
- [Authentication](#authentication)
- [Response & Error Format](#response--error-format)
- [Core Endpoints](#core-endpoints)
  - [Deploy (SSE)](#deploy-sse)
  - [Invoke (SSE)](#invoke-sse)
  - [Functions](#functions)
  - [Cron Schedules](#cron-schedules)
  - [Invocations](#invocations)
  - [Secrets](#secrets)
  - [API Keys](#api-keys)
- [Streaming Endpoints](#streaming-endpoints)
- [Health](#health)
- [End-to-End Examples](#end-to-end-examples)
- [Status Codes](#status-codes)

---

## Base URL

```
http://localhost:8080
```

---

## Authentication

Orva supports **two auth methods** for all protected endpoints:

1. **Session cookie** (recommended for dashboard):
   - Login at `POST /auth/login`.
   - Server sets `session_token` (HTTP-only cookie).

2. **API key** (recommended for CI/automation):
   - Create with `POST /api-keys`.
   - Send in `X-API-Key` or `Authorization: Bearer <key>`.

> **Public endpoint:** `GET /health` is public.

### Login

**POST** `/auth/login`

Request:
```json
{
  "username": "admin",
  "password": "admin123"
}
```

Response:
```json
{
  "user": {
    "id": 1,
    "username": "admin",
    "created_at": "2026-01-22T13:29:24Z",
    "last_login": "2026-01-22T13:31:59Z"
  },
  "token": "<session_token>"
}
```

### Logout

**POST** `/auth/logout`

Response:
```json
{
  "message": "Logged out successfully"
}
```

### Current User

**GET** `/auth/me`

Response:
```json
{
  "id": 1,
  "username": "admin",
  "created_at": "2026-01-22T13:29:24Z",
  "last_login": "2026-01-22T13:31:59Z"
}
```

---

## Response & Error Format

Successful responses use standard JSON. Errors use a consistent shape:

```json
{
  "error": {
    "code": "INVALID_REQUEST",
    "message": "Secret name and value are required",
    "details": {}
  }
}
```

---

## Core Endpoints

### Deploy (SSE)

**POST** `/deploy` (multipart/form-data)

Form fields:
- `name` (string, required)
- `runtime` (string, required) — e.g., `python`, `node`, `go`, `rust`, `ruby`
- `entrypoint` (string, optional)
- `memory_mb` (int, optional, default 128)
- `cpus` (int, optional, default 1)
- `env_vars` (stringified JSON, optional)
- `file` (binary, required)
- `dependencies` (optional file)

SSE events:
- `message` — progress messages
- `build-progress` — build steps
- `error` — error details
- `complete` — final deployment result

Example (curl):
```bash
curl -N -X POST http://localhost:8080/deploy \
  -H "X-API-Key: <api_key>" \
  -F "name=hello" \
  -F "runtime=python" \
  -F "file=@main.py" \
  -F "entrypoint=main.handler"
```

---

### Invoke (SSE)

**POST** `/invoke/{function}` or `/invoke/{function}/v/{version}`

- Body can be raw bytes.
- Response is streamed (SSE).

SSE events:
- `message` — start info
- `log` — function logs
- `output` — function output + metadata
- `complete` — invocation completion
- `error` — error details

Example:
```bash
curl -N -X POST http://localhost:8080/invoke/hello \
  -H "X-API-Key: <api_key>" \
  -d '{"name":"world"}'
```

---

### Functions

**GET** `/functions` — list all functions

**GET** `/functions/{name}` — latest version

**GET** `/functions/{name}/v/{version}` — specific version

**GET** `/functions/{name}/versions` — list versions

**GET** `/functions/{name}/source` — source code

**POST** `/functions/{name}/rollback/{version}` — rollback to version

**DELETE** `/functions/{name}` — delete all versions

**DELETE** `/functions/{name}/v/{version}` — delete a version

---

### Cron Schedules

**GET** `/cron` — list schedules

**POST** `/cron/{function}` — create or update

Request:
```json
{
  "cron": "0 9 * * 1",
  "enabled": true
}
```

**DELETE** `/cron/{function}` — delete schedule

---

### Invocations

**GET** `/invocations` — list (supports filters)

Query params:
- `function`, `status`, `limit`, `offset`
- `include_log=true`
- `include_payload=true`

**GET** `/invocations/{id}` — full details (includes log & payloads)

---

### Secrets

**GET** `/functions/{name}/secrets` — list names

**POST** `/functions/{name}/secrets` — create/update

Request:
```json
{
  "name": "API_KEY",
  "value": "secret-value"
}
```

Response:
```json
{ "status": "ok" }
```

**DELETE** `/functions/{name}/secrets/{id}` — delete (204)

---

### API Keys

**GET** `/api-keys` — list

Response:
```json
{
  "keys": [
    {
      "id": 1,
      "name": "CI",
      "prefix": "a1b2c3",
      "created_at": "2026-01-22T12:00:00Z",
      "last_used_at": "2026-01-22T12:30:00Z"
    }
  ]
}
```

**POST** `/api-keys` — create

Request:
```json
{ "name": "CI" }
```

Response (201):
```json
{
  "id": 1,
  "name": "CI",
  "prefix": "a1b2c3",
  "key": "<full_api_key>"
}
```

**DELETE** `/api-keys/{id}` — delete (204)

---

## Streaming Endpoints

### Invocation stream
**GET** `/stream/invocations`

Events:
- `message`
- `initial`
- `update`

### Metrics stream
**GET** `/stream/metrics`

Events:
- `message`
- `metrics`

---

## Health

**GET** `/health` (public)

Response:
```json
{
  "status": "healthy",
  "version": "0.1.0",
  "database": "connected",
  "ops_available": true,
  "current_load": 0,
  "max_parallel": 10
}
```

---

## End-to-End Examples

### 1) Login → Create API key → Deploy → Invoke

```bash
# Login
curl -s -c cookies.txt -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}'

# Create API key (uses cookie)
curl -s -b cookies.txt -X POST http://localhost:8080/api-keys \
  -H "Content-Type: application/json" \
  -d '{"name":"CI"}'

# Deploy (uses key)
curl -N -X POST http://localhost:8080/deploy \
  -H "X-API-Key: <key>" \
  -F "name=hello" \
  -F "runtime=python" \
  -F "file=@main.py"

# Invoke (uses key)
curl -N -X POST http://localhost:8080/invoke/hello \
  -H "X-API-Key: <key>" \
  -d '{"name":"world"}'
```

---

## Status Codes

- `200 OK` — success
- `201 Created` — resource created
- `204 No Content` — deleted
- `400 Bad Request` — invalid input
- `401 Unauthorized` — missing/invalid auth
- `404 Not Found` — resource missing
- `500 Internal Server Error`

---

<div align="center" style="color:#b3afc7;">
  Built with the Orva theme: #12111C background · #553F83 primary · #FFFFFF text
</div>
