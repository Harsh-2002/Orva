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
- **CodeMirror 6** — `@codemirror/lang-javascript`, `@codemirror/lang-python`, `@codemirror/lang-json`, `@codemirror/theme-one-dark`, `@codemirror/merge` (side-by-side / unified diff in FunctionDiff.vue)
- **lucide-vue-next** icons (tree-shaken per-import)
- **axios** for HTTP
- **markdown-it** + **highlight.js** — render assistant chat messages (markdown + fenced-code highlight) in the AI view

## Key Files

| Path | Purpose |
|---|---|
| `src/api/client.js` | Axios instance; injects `X-Orva-API-Key` auth header |
| `src/api/endpoints.js` | Every API helper function (one per endpoint) |
| `src/router/` | Vue Router route table |
| `src/stores/auth.js` | Auth state + login/logout |
| `src/stores/confirm.js` | Global confirmation modal store |
| `src/stores/events.js` | Persistent SSE connection to `/api/v1/events` |
| `src/stores/ai.js` | AI chat store: conversations, timeline, streaming client (fetch + ReadableStream, NOT EventSource — the chat POST carries a body), provider/model/settings, + message actions (regenerate / editAndResend / deleteMessageFrom / retry / stop / renameConversation / exportActive) |
| `src/views/AI.vue` + `src/components/ai/*` | Native Vue agentic chat UI; talks to `/api/v1/ai/*`. Components: `ConversationRail`, `ChatHeader`, `EmptyState` (greeting + click-to-fill starter prompts), `Composer` (textarea + `ReasoningMenu` + `ModelMenu` + Send/Stop), `MessageList`, `Message`, `MessagePart` (markdown-it + fenced → `CodeBlock`, thinking → `ThinkingBlock`), `ToolCallCard`, `TypingIndicator`, `ScrollToBottom`, `ErrorCard`, `AISettingsPanel`. AI **configuration** (providers, keys, defaults) is centralized on the Settings page via `AISettingsPanel` (rendered in `Settings.vue`'s "AI assistant" card, anchor `#ai`); the chat's gear + no-provider banner deep-link there (`router.push({name:'settings', hash:'#ai'})`). The chat composer keeps the active provider/model/reasoning pickers (per-conversation controls, persisted server-side via `PUT /api/v1/ai/selection`). |
| `src/stores/system.js` | System info (version, runtime stats) |
| `src/views/Editor.vue` | Function editor + test pane (method/path/headers/body) + saved fixtures + suggest-fix |
| `src/views/InvocationsLog.vue` | Execution history drawer + request panel + replay button + suggest-fix |
| `src/views/Settings.vue` | System settings, backup/restore card, storage card |
| `src/views/InboundWebhooks.vue` | Inbound webhook trigger management |
| `src/views/Traces.vue` | Trace list with filters + outlier badges |
| `src/views/TraceDetail.vue` | Single-trace waterfall + span detail |
| `src/views/FunctionDiff.vue` | Side-by-side / unified source diff between two deployments (CodeMirror merge) + rollback CTA |
| `src/utils/aiPrompts.js` | `buildPromptText()` (code gen) + `buildFixSuggestionPrompt()` (debug) |
| `src/utils/rollbackDiff.js` | `describeSnapshotDiff()` — shared settings/env diff lines (Editor, Deployments, FunctionDiff) |
| `src/templates/index.js` | Built-in function templates (including `ts_hello`, `py_stream_llm`) |

## Non-obvious

- Dev proxy: `vite.config.js` proxies `/api` and `/auth` to `http://localhost:8443`. Direct `/fn/`, `/webhook/`, and `/metrics` calls in dev must be made to `:8443` directly — they are not proxied through Vite.
- `src/stores/events.js` opens a persistent SSE connection on mount and reconnects automatically on drop. Dashboard widgets subscribe to this store — they do not open their own connections.
- All AI prompt and clipboard operations (`aiPrompts.js`) are purely client-side — no source code is sent over the network.
- The `Editor.vue` test pane sends requests through the backend (`POST /api/v1/functions/{id}/invoke`) rather than directly to `/fn/` — this ensures auth and capture still apply.
- **AI streaming reactivity (load-bearing):** `stores/ai.js` tracks the streaming assistant message by **index** (`curIdx`) and writes every delta back through the reactive array via `patchAssistant()` (rebuilds `parts` immutably, then `timeline.value[curIdx] = next`). Never hold a raw object reference and mutate `parts[i].text +=` — Vue 3 tracks the array proxy, not the raw ref, so per-token mutations silently fail to re-render. The same index-write rule applies to `tool_result` frames.
- **AI markdown rendering:** `MessagePart.vue` splits markdown into ordered segments so top-level fenced code becomes a real `<CodeBlock>` while prose stays HTML; parsing is throttled to ~12/s (leading + trailing edge) during streaming. Tables inherit the body font size (no shrink) so tabular output matches prose; the system prompt steers the model toward prose/bullets and reserves tables for genuinely tabular data.
