#!/usr/bin/env python3
"""
test_chat.py — end-to-end scenarios for the AI agentic loop, driven entirely by
the mock LLM (no real provider key). Exercises the full chain:
  chat -> model tool_call -> (approval) -> in-process dispatch -> result -> final answer.

Run:  ORVA_API_KEY=$(sudo cat /var/lib/orva/.admin-key) python3 test/ai/test_chat.py
(or set ORVA_URL/ORVA_API_KEY for any instance).
"""
import json
import sys

from harness import (
    OrvaClient, start_mock, configure_mock_provider, remove_mock_provider,
    section, check, summary, events_of, has_event, first_tool_call,
)

# A distinctive provider key so a substring search for it in any chat output is
# unambiguous (unlike the default "test").
LEAK_SENTINEL = "sk-e2e-SENTINEL-do-not-leak-7c1f9a2b"

TEST_FN = "e2e-mock-fn"


def cleanup_fn(c):
    # Tolerant cleanup. NOTE: the REST GET/DELETE /functions/{fn_id} route
    # resolves by UUID only (unlike the MCP/agent layer, which also accepts a
    # name), so we list and delete by id rather than by name.
    try:
        lst = c.get("/api/v1/functions") or {}
        for fn in lst.get("functions", []):
            if fn.get("name") == TEST_FN:
                c.req("DELETE", f"/api/v1/functions/{fn['id']}", expect=(200, 204, 404))
    except Exception:
        pass


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    start_mock()
    configure_mock_provider(c, approval="all_writes")
    cleanup_fn(c)
    try:
        # ── 1. read tool auto-runs, no approval ──────────────────────────
        section("read tool auto-runs (list_functions)")
        frames, conv = c.chat("CALL list_functions {}")
        check("conversation created", bool(conv))
        check("message_start emitted", has_event(frames, "message_start"))
        tc = first_tool_call(frames)
        check("tool_call is list_functions", bool(tc) and tc.get("name") == "list_functions", str(tc))
        check("read tool did NOT require approval", bool(tc) and tc.get("requires_approval") is False)
        results = events_of(frames, "tool_result")
        check("tool_result succeeded", any(r.get("status") == "succeeded" for r in results), str(results))
        check("final answer streamed", has_event(frames, "delta"))
        check("done emitted", has_event(frames, "done"))
        check("did NOT pause for approval", not has_event(frames, "awaiting_approval"))

        # ── 2. write tool pauses for approval, then approve runs it ───────
        section("write tool requires approval, then approve (create_function)")
        cleanup_fn(c)
        args = ('{"name":"%s","description":"e2e mock","runtime":"node24",'
                '"entrypoint":"handler.js","timeout_ms":30000,"memory_mb":128,'
                '"cpus":1,"network_mode":"none","auth_mode":"none"}') % TEST_FN
        frames, conv = c.chat("CALL create_function " + args)
        tc = first_tool_call(frames)
        check("tool_call is create_function", bool(tc) and tc.get("name") == "create_function", str(tc))
        check("write tool requires approval", bool(tc) and tc.get("requires_approval") is True)
        check("stream paused (awaiting_approval)", has_event(frames, "awaiting_approval"))
        check("did NOT auto-run before approval", not events_of(frames, "tool_result"))
        row_id = tc.get("id") if tc else None
        check("tool_call has a row id to approve", bool(row_id))

        if row_id:
            rframes = c.approve(row_id)
            rresults = events_of(rframes, "tool_result")
            check("after approve: tool_result present", bool(rresults), str(rframes)[:200])
            check("after approve: succeeded", any(r.get("status") == "succeeded" for r in rresults), str(rresults))
            check("after approve: final answer + done", has_event(rframes, "delta") and has_event(rframes, "done"))
            # Confirm the function really exists now (the agent did real work).
            # Use the id from the tool result — REST GET resolves by UUID only.
            created = (rresults[0].get("result") or {}) if rresults else {}
            fid = created.get("id") if isinstance(created, dict) else None
            check("create result carries a function id", bool(fid), str(created)[:160])
            if fid:
                code, _ = c.req("GET", f"/api/v1/functions/{fid}", expect=(200, 404))
                check("function was actually created in Orva", code == 200, f"GET status {code}")

        # ── 3. reject path: tool does NOT run ─────────────────────────────
        section("write tool reject (delete_secret rejected)")
        frames, conv = c.chat('CALL set_secret {"function_id":"%s","key":"E2E","value":"x"}' % TEST_FN)
        tc = first_tool_call(frames)
        check("tool_call requires approval", bool(tc) and tc.get("requires_approval") is True)
        row_id = tc.get("id") if tc else None
        if row_id:
            rframes = c.reject(row_id)
            rresults = events_of(rframes, "tool_result")
            check("reject recorded as rejected", any(r.get("status") == "rejected" for r in rresults), str(rresults))

        # ── 4. provider API key never leaks through the chat path ─────────
        section("provider key is not exposed via chat SSE or conversation detail")
        # Re-point the same provider (upsert by provider+label) at a sentinel key.
        configure_mock_provider(c, approval="all_writes", api_key=LEAK_SENTINEL)
        frames, lconv = c.chat("CALL list_functions {}")
        check("key absent from chat SSE frames", LEAK_SENTINEL not in json.dumps(frames),
              "sentinel leaked into the chat stream")
        if lconv:
            d = c.get(f"/api/v1/ai/conversations/{lconv}") or {}
            check("key absent from conversation detail", LEAK_SENTINEL not in json.dumps(d),
                  "sentinel leaked into persisted conversation")
        provs = c.get("/api/v1/ai/providers") or {}
        check("key absent from providers list", LEAK_SENTINEL not in json.dumps(provs))

    finally:
        cleanup_fn(c)
        remove_mock_provider(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
