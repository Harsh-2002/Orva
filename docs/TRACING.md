# Tracing

Orva produces a full causal trace for every invocation chain — automatically,
with **zero changes required in user code**. Every execution row in the
database IS a span. Spans share a `trace_id`; child spans point at their
parent via `parent_span_id`. The dashboard renders the result as a waterfall.

## Model

| Field | Format | Example |
|---|---|---|
| `trace_id` | `tr_` + 32 hex chars | `tr_3e39f6991c66f140577c6021da7dd13b` |
| `span_id` | `sp_` + 16 hex chars | `sp_e0febab07c8a3915` |
| `parent_span_id` | span_id of caller (empty for standalone roots; may name an external W3C parent) | `sp_4ceba57f6b1c982e` |
| `trigger` | how this span started | `http` / `cron` / `job` / `f2f` / `webhook` / `inbound` / `replay` / `mcp` |

Causality covered today:

| Source → child | Trace creation | Parent set on child |
|---|---|---|
| HTTP request → function | New trace at middleware | none (root) |
| Function A → `orva.invoke(B)` → function B | A's trace reused | A's span_id |
| Function A → `orva.jobs.enqueue()` → job runs → function C | A's trace stored on job row, reused on pickup | A's span_id |
| Cron → function | Fresh trace at fire | none (root) |
| Inbound webhook → function | Fresh trace | none (root) |
| External system → HTTP → function | Honors W3C `traceparent` | external parent_span_id |
| Replay of an execution | Fresh trace (replays are independent) | none (root) |

Orva defines the **local root** as the earliest execution whose parent span is
absent from the same trace. A normal trace therefore has an empty parent on its
local root. An externally parented W3C trace keeps the upstream parent in
`external_parent_span_id` and is still listed normally.

## What user code sees

The platform stamps two env vars per invocation:

```bash
ORVA_TRACE_ID=tr_…
ORVA_SPAN_ID=sp_…
```

The Orva SDK reads them and forwards `X-Orva-Trace-Id` / `X-Orva-Span-Id`
headers on every internal call (`orva.invoke()`, `orva.jobs.enqueue()`).
You don't need to do anything; just keep using the SDK.

## W3C interop

Send a [`traceparent`](https://www.w3.org/TR/trace-context/) header on the
inbound HTTP request and Orva will:

1. Parse the trace_id (32 hex) and parent_span_id (16 hex).
2. Use them as the trace root, prefixing internally with `tr_` / `sp_`.
3. Echo the same trace_id back in the `X-Trace-Id` response header so your
   upstream can correlate.

```bash
curl -H "traceparent: 00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01" \
     https://orva.example.com/fn/myfn/
# → response: X-Trace-Id: tr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
```

Malformed traceparent values are ignored gracefully — Orva generates a
fresh trace_id and never panics.

## Outlier flag

Each function maintains an in-memory rolling P95 baseline over its last
100 successful warm executions. A new execution is flagged as an outlier
when:

- The function has at least 20 samples in its window, AND
- The execution's duration exceeds **P95 × 2**.

Cold starts and errors are excluded from the baseline so a flapping
function can't drag P95 down. The flag and the baseline_p95_ms ride along
on the execution row.

## API

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/traces/{trace_id}` | Full causal tree with system spans, user spans, associated structured logs, and waterfall offsets |
| `GET /api/v1/traces?function_id=&since=&until=&status=&outlier_only=&limit=&before=` | Trace-wide summaries with stable opaque cursor pagination |
| `GET /api/v1/functions/{id}/baseline` | Current P95/P99/mean for a function plus sample count |

Every HTTP response also carries `X-Trace-Id` for external correlation.

List semantics are trace-wide: any failed system or user span makes `status`
`error`; `duration_ms` covers earliest start through latest finish;
`is_outlier` is true when any execution is an outlier; and `span_count`,
`error_count`, and `cold_start_count` cover the complete trace. The
`function_id` filter matches any execution by exact function ID or exact current
name. `next_cursor` is opaque and encodes the stable `(started_at, trace_id)`
ordering tuple. Older timestamp-only cursors remain accepted temporarily.

## MCP tools

- `get_trace(trace_id)` — full span tree
- `list_traces({function_id?, status?, outlier_only?, since?, until?, limit?, before?})`
- `get_function_baseline(function_id)`

All three tools require the read permission and run side-effect-free.

## UI

- **/traces** lists trace-wide status, duration, span/error/cold-start counts,
  and offers function, status, outlier, and time-range filters.
- **/traces/:id** renders one expandable causal waterfall. Selecting a span
  keeps you on the trace and reveals status code, cold/warm state, error,
  duration versus baseline, and its structured logs. **Open invocation** is a
  separate action. Logs can be switched between all spans and the selected
  span; structured fields expand in place.
- **Invocations log** has a Trace column showing the first 11 chars of
  the trace_id; click jumps to the trace detail.

## Concurrency / scalability

- Trace ID generation: single `crypto/rand.Read(16)` call per request.
  Microseconds. Negligible vs request P95.
- Storage: ~200 bytes/execution row for the seven new columns. 1M
  executions = +220 MB.
- Composite indexes cover `(trace_id, started_at, id)`,
  `(trace_id, parent_span_id)`, `(started_at, trace_id)`, and
  `(function_id, trace_id)` for deterministic trees, local-root discovery,
  stable pagination, and any-span filtering.
- Baselines: per-function ring buffer of 100 samples × 8 bytes = 800 B
  per function. 1000 functions = under 1 MB.
- All inserts ride on the existing async batch writer — no synchronous
  DB calls on the hot path.

## Schema

```sql
ALTER TABLE executions  ADD COLUMN trace_id TEXT;
ALTER TABLE executions  ADD COLUMN span_id TEXT;
ALTER TABLE executions  ADD COLUMN parent_span_id TEXT;
ALTER TABLE executions  ADD COLUMN trigger TEXT;
ALTER TABLE executions  ADD COLUMN parent_function_id TEXT;
ALTER TABLE executions  ADD COLUMN is_outlier INTEGER NOT NULL DEFAULT 0;
ALTER TABLE executions  ADD COLUMN baseline_p95_ms INTEGER;
ALTER TABLE jobs        ADD COLUMN trace_id TEXT;
ALTER TABLE jobs        ADD COLUMN parent_span_id TEXT;
ALTER TABLE jobs        ADD COLUMN enqueued_by_function_id TEXT;
ALTER TABLE activity_log ADD COLUMN trace_id TEXT;

CREATE INDEX idx_executions_trace_id      ON executions(trace_id);
CREATE INDEX idx_executions_parent_span_id ON executions(parent_span_id);
CREATE INDEX idx_executions_trace_started ON executions(trace_id, started_at, id);
CREATE INDEX idx_executions_trace_parent  ON executions(trace_id, parent_span_id);
CREATE INDEX idx_executions_started_trace ON executions(started_at DESC, trace_id DESC);
CREATE INDEX idx_executions_function_trace ON executions(function_id, trace_id);
CREATE INDEX idx_activity_log_trace_id    ON activity_log(trace_id);
```

All migrations are idempotent — applied automatically on startup; failures
on duplicate columns are swallowed.
