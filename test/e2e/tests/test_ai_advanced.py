#!/usr/bin/env python3
"""
test_agent_advanced.py — advanced E2E scenarios for Orva's AI agentic loop,
driven entirely by the mock LLM (no real provider key). Focuses on the parts
test_chat.py does not cover:

  - approval POLICY behaviour (auto / destructive_only)
  - MULTI-TOOL turns (two tool_calls emitted in a single model turn)
  - ERROR HANDLING (a tool that fails mid-loop must not crash the stream)

Each scenario reconfigures the provider's approval policy before running so the
gate behaviour is isolated. Resources are all read-only or side-effect-free
(nonexistent ids), so there is nothing to clean up beyond the mock provider.

Run:  ORVA_API_KEY=$(sudo cat /var/lib/orva/.admin-key) python3 test/ai/test_agent_advanced.py
(or set ORVA_URL/ORVA_API_KEY for any instance).
"""
import sys

from harness import (
    OrvaClient, start_mock, configure_mock_provider, remove_mock_provider,
    section, check, summary, events_of, has_event, first_tool_call,
)


def _final_or_done(frames):
    """The loop closed cleanly: it reached a terminal frame (done/error) or
    at least produced a final assistant delta. Proves it did not hang."""
    return has_event(frames, "done") or has_event(frames, "delta") or has_event(frames, "error")


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    start_mock()
    try:
        # ── 1. POLICY auto: read tool runs straight through, no pause ─────
        section("policy=auto: read tool runs with no approval (list_functions)")
        configure_mock_provider(c, approval="auto")
        frames, conv = c.chat("CALL list_functions {}")
        check("conversation created", bool(conv))
        check("message_start emitted", has_event(frames, "message_start"))
        tc = first_tool_call(frames)
        check("tool_call is list_functions", bool(tc) and tc.get("name") == "list_functions", str(tc))
        check("did NOT pause for approval", not has_event(frames, "awaiting_approval"))
        results = events_of(frames, "tool_result")
        check("tool_result succeeded", any(r.get("status") == "succeeded" for r in results), str(results))
        check("final answer streamed", has_event(frames, "delta"))
        check("done emitted", has_event(frames, "done"))

        # ── 2. POLICY destructive_only: non-destructive read auto-runs ────
        section("policy=destructive_only: non-destructive tool auto-runs (system_health)")
        configure_mock_provider(c, approval="destructive_only")
        frames, conv = c.chat("CALL system_health {}")
        tc = first_tool_call(frames)
        check("tool_call is system_health", bool(tc) and tc.get("name") == "system_health", str(tc))
        check("system_health did NOT require approval", bool(tc) and tc.get("requires_approval") is False)
        check("non-destructive auto-ran (no pause)", not has_event(frames, "awaiting_approval"))
        results = events_of(frames, "tool_result")
        check("tool_result succeeded", any(r.get("status") == "succeeded" for r in results), str(results))
        check("reached done", has_event(frames, "done"))

        # ── 3. POLICY destructive_only: destructive tool MUST pause ───────
        # bulk_delete_executions is destructive, so under destructive_only it
        # must gate even though everything non-destructive auto-runs. Keep it
        # side-effect-free by targeting a nonexistent execution id.
        section("policy=destructive_only: destructive tool pauses (bulk_delete_executions)")
        frames, conv = c.chat('CALL bulk_delete_executions {"ids":["does-not-exist-xyz"],"confirm":true}')
        tc = first_tool_call(frames)
        check("tool_call is bulk_delete_executions",
              bool(tc) and tc.get("name") == "bulk_delete_executions", str(tc))
        check("destructive tool requires approval", bool(tc) and tc.get("requires_approval") is True)
        check("stream paused (awaiting_approval)", has_event(frames, "awaiting_approval"))
        check("did NOT auto-run before approval", not events_of(frames, "tool_result"))
        # Do not approve — leaving it gated has zero side effects. If we did
        # approve, the nonexistent id would make it a no-op/failure either way.

        # ── 4. MULTI-TOOL: two tool_calls in one model turn (auto) ────────
        section("policy=auto: multi-tool turn (list_functions || system_health)")
        configure_mock_provider(c, approval="auto")
        frames, conv = c.chat("CALL2 list_functions {} || system_health {}")
        calls = events_of(frames, "tool_call")
        check("two tool_call events emitted", len(calls) == 2, f"got {len(calls)}: {[x.get('name') for x in calls]}")
        names = {x.get("name") for x in calls}
        check("both expected tools present", names == {"list_functions", "system_health"}, str(names))
        check("neither paused for approval", not has_event(frames, "awaiting_approval"))
        results = events_of(frames, "tool_result")
        check("two tool_results emitted", len(results) == 2, str([r.get("status") for r in results]))
        check("both tool_results succeeded",
              len(results) == 2 and all(r.get("status") == "succeeded" for r in results), str(results))
        check("final answer streamed", has_event(frames, "delta"))
        check("done emitted", has_event(frames, "done"))

        # ── 5. ERROR HANDLING: a failing tool must not crash the loop ─────
        # get_function on a nonexistent id returns an error from the dispatcher.
        # The loop must surface a failed tool_result (or error frame) and still
        # close out — never hang.
        section("policy=auto: tool error is handled without crashing (get_function)")
        frames, conv = c.chat('CALL get_function {"function_id":"does-not-exist-xyz"}')
        tc = first_tool_call(frames)
        check("tool_call is get_function", bool(tc) and tc.get("name") == "get_function", str(tc))
        check("did NOT pause for approval (read under auto)", not has_event(frames, "awaiting_approval"))
        results = events_of(frames, "tool_result")
        failed_result = any(r.get("status") == "failed" for r in results)
        error_frame = has_event(frames, "error")
        check("tool error surfaced (failed tool_result or error frame)",
              failed_result or error_frame, str(results)[:200])
        check("loop closed cleanly without hanging (done/delta/error present)", _final_or_done(frames))

    finally:
        remove_mock_provider(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
