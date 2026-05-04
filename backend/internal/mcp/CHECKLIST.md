# MCP tool checklist (read before adding a new tool)

Every tool registered with `mcpsdk.AddTool` must satisfy these rules. They
exist so the same MCP server works identically against Claude Code,
ChatGPT custom connectors, Cursor, Codex, Gemini, and any future
spec-conformant client — without per-client schema tweaks.

The portability test in `schema_portability_test.go` enforces most of
these automatically. If you can't satisfy a rule for a good reason,
update the test allow-list and explain why in code comments.

---

## Schema dialect & shape

1. **Don't set `$schema`** — let the SDK pick the MCP 2025-11-25 default
   (JSON-Schema Draft 2020-12).
2. **Object schemas have `additionalProperties: false`** — at every
   nesting level. The Go SDK's `jsonschema-go` does this automatically
   for inferred struct schemas; if you hand-roll a `*jsonschema.Schema`,
   you must set it yourself.
3. **No `interface{}` / `any` in tool input fields**. Either use a
   typed struct, or wrap with a discriminator envelope (`type` enum +
   one branch field per type). The `KVValue` envelope in `tools_kv.go`
   and the `InvokeBody` envelope in `tools_invoke.go` are the canonical
   examples.
4. **No `oneOf` at the schema root, no recursive `$ref`, no external
   `$ref`**. Strict-mode APIs reject all three. `anyOf` works on Claude
   and OpenAI but breaks Gemini — avoid.
5. **No `default` for behaviour-changing fields**. OpenAI strict-mode
   ignores `default`. Restate the default value in the field's
   `description` so the model sees it verbatim.
6. **No regex `pattern`, no numeric `minimum` / `maximum`, no string
   `minLength` / `maxLength`** for validation. Strict modes ignore
   them. Validate server-side and return a tool-execution error
   (`isError: true` with a clear message) so the model self-corrects
   (per MCP SEP-1303).
7. **`enum` is high-leverage** — use it whenever a string field has a
   small known value set. Models pick the right value 20–40% more
   often with an enum than with a free-form string.

## Naming

8. **Tool names match `^[a-z][a-z0-9_]{0,62}$`**. Snake_case, ≤63 chars
   (leaves headroom for the `mcp__<server>__<tool>` flatten in Claude
   Code), no dots (Anthropic regex rejects them), no leading digits.

## Descriptions

9. **Tool description ≥ 80 chars** and ideally 3–4 sentences covering:
   what it does, when to use vs not, what each parameter means, what
   the output looks like. Don't restate the name. Mention sister tools
   when wrong-tool selection is plausible (e.g. `delete_*` referencing
   `update_*` for the "want to disable" case).
10. **Per-property `description` on every field** — this is the only
    schema text guaranteed to reach every model's context window
    verbatim. Use it to declare units (`"in seconds"`), defaults
    (`"defaults to 30000"`), constraints (`"must be ≥ 1"`), and
    formats (`"ISO-8601 in UTC"`).

## Annotations & metadata

11. **Set every annotation honestly** on every tool:
    `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`.
    Some clients gate auto-run on these (ChatGPT reads `readOnlyHint`).
12. **Set `Title:` on every tool** — give clients a human display name
    distinct from the slug. Required for channel-mode where the slug is
    sanitised. Operator-mode mirrors the function intent in title-case
    (e.g. `"List Functions"` for `list_functions`); channel-mode uses
    the original (un-sanitised) function name.

---

## Channel-mode generation

When the channel auto-emits one tool per bundled function:

- Tool name comes from `sanitiseChannelToolName` (regex, leading-digit
  guard, length cap, reserved-prefix block, hash suffix on collision).
- Title is the original (un-sanitised) function name.
- Description self-introduces with `Invokes the Orva function "<name>"
  on channel <channel>.` so the model has context — channel-mode
  delivers tools to agents that have NEVER seen Orva before.
- Input is the typed `ChannelInvokeInput` envelope (method, path,
  headers, body); `body` uses the same `InvokeBody` discriminator as
  operator-mode.

---

## Anti-patterns to reject in code review

- Tools whose only input is a single `any` / `map[string]any` field.
- Tools named `delete_*` / `create_*` / `update_*` / `list_*` for the
  same resource — consolidate when possible (Anthropic's "Writing tools
  for agents" recommends an `action` enum).
- Output structs nesting > 3 levels deep — flatten or split.
- Free-form strings where an `enum` would do.
- Pattern/format/min/max validation in the schema (use server-side
  guards instead).

## Sources

- MCP spec 2025-11-25 — Tools section + JSON Schema usage.
- Anthropic — "Writing tools for agents" (engineering blog, Sept 2025).
- Anthropic — Strict tool use (JSON Schema subset).
- OpenAI — Structured Outputs supported subset.
- Google Gemini — Structured Output / OpenAPI subset.
