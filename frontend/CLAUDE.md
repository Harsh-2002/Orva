# Frontend

Vue 3 + Vite + Tailwind CSS 4 dashboard. Dev server at http://localhost:5173 (proxies API calls to `:8443`). Production build is embedded in the Go binary.

## Commands

```bash
npm install          # install dependencies (node_modules/)
npm run dev          # hot-reload dev server → http://localhost:5173
npm run build        # production build → dist/
npm run lint         # eslint check
npm run lint:fix     # eslint auto-fix
```

After `npm run build`, run `make embed` from the repo root to copy `dist/` into `backend/internal/server/ui_dist/` so it gets embedded in the Go binary.

## Stack

- **Vue 3** Composition API with `<script setup>` everywhere
- **Pinia** for global state
- **Vue Router 4** — routes defined in `src/router/`
- **Tailwind CSS 4** (PostCSS plugin)
- **CodeMirror 6** — `@codemirror/lang-javascript`, `@codemirror/lang-python`, `@codemirror/theme-one-dark`
- **lucide-vue-next** icons (tree-shaken per-import)
- **axios** for HTTP

## Key Files

| Path | Purpose |
|---|---|
| `src/api/client.js` | Axios instance; injects `X-Orva-API-Key` auth header |
| `src/api/endpoints.js` | Every API helper function (one per endpoint) |
| `src/router/` | Vue Router route table |
| `src/stores/auth.js` | Auth state + login/logout |
| `src/stores/confirm.js` | Global confirmation modal store |
| `src/stores/events.js` | Persistent SSE connection to `/api/v1/events` |
| `src/stores/system.js` | System info (version, runtime stats) |
| `src/views/Editor.vue` | Function editor + test pane (method/path/headers/body) + saved fixtures + suggest-fix |
| `src/views/InvocationsLog.vue` | Execution history drawer + request panel + replay button + suggest-fix |
| `src/views/Settings.vue` | System settings, backup/restore card, storage card |
| `src/views/InboundWebhooks.vue` | Inbound webhook trigger management |
| `src/utils/aiPrompts.js` | `buildPromptText()` (code gen) + `buildFixSuggestionPrompt()` (debug) |
| `src/templates/index.js` | Built-in function templates (including `ts_hello`, `py_stream_llm`) |

## Non-obvious

- Dev proxy: `vite.config.js` proxies `/api` and `/auth` to `http://localhost:8443`. Direct `/fn/`, `/webhook/`, and `/metrics` calls in dev must be made to `:8443` directly — they are not proxied through Vite.
- `src/stores/events.js` opens a persistent SSE connection on mount and reconnects automatically on drop. Dashboard widgets subscribe to this store — they do not open their own connections.
- All AI prompt and clipboard operations (`aiPrompts.js`) are purely client-side — no source code is sent over the network.
- The `Editor.vue` test pane sends requests through the backend (`POST /api/v1/functions/{id}/invoke`) rather than directly to `/fn/` — this ensures auth and capture still apply.
