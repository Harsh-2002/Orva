#!/usr/bin/env python3
"""
test_ai_edit.py — the destructive-tail conversation operations: edit, delete,
and regenerate all TRUNCATE the conversation from a point onward (no branching
history). This is the most error-prone AI behavior (irreversible data loss) and
was previously untested. Driven by the mock LLM, no real key.

Run:  ORVA_API_KEY=$(sudo cat /var/lib/orva/.admin-key) python3 test/e2e/tests/test_ai_edit.py
"""
import sys

from harness import (
    OrvaClient, start_mock, configure_mock_provider, remove_mock_provider,
    section, check, summary, has_event,
)


def detail(c, conv):
    return c.get(f"/api/v1/ai/conversations/{conv}") or {}


def msgs_by_role(d, role):
    return [m for m in (d.get("messages") or []) if m.get("role") == role]


def find_user_msg(d, needle):
    for m in d.get("messages") or []:
        if m.get("role") == "user" and needle in (m.get("content") or ""):
            return m
    return None


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    start_mock()
    configure_mock_provider(c, approval="auto")  # auto so the tool turn completes in one stream
    try:
        # ── EDIT truncates from the edited message onward, then re-runs ──────
        section("edit a middle user message truncates the tail + re-runs")
        # Turn 1: plain text. Turn 2: a tool call (creates a tool_call row).
        # Turn 3: plain text.
        _, conv = c.chat("first turn")
        check("conversation created", bool(conv))
        c.chat("CALL list_functions {}", conversation_id=conv)
        c.chat("third turn", conversation_id=conv)

        d0 = detail(c, conv)
        users0 = msgs_by_role(d0, "user")
        tcs0 = d0.get("tool_calls") or []
        check("3 user turns recorded", len(users0) == 3, str([m.get("content") for m in users0]))
        check("a tool_call row exists before edit", len(tcs0) >= 1, str(len(tcs0)))
        check("message seqs are unique", len({m["seq"] for m in d0["messages"]}) == len(d0["messages"]))

        turn2 = find_user_msg(d0, "list_functions")
        check("found turn-2 user message", bool(turn2))
        if turn2:
            # Edit turn 2 with PLAIN text → tail (turn2 tool stuff + turn3) is
            # truncated and the turn re-runs as a simple text reply.
            frames = c.stream(
                f"/api/v1/ai/conversations/{conv}/messages/{turn2['id']}/edit",
                {"content": "edited second turn"},
            )
            check("edit streamed a fresh answer", has_event(frames, "delta"))
            check("edit completed (done)", has_event(frames, "done"))

            d1 = detail(c, conv)
            users1 = msgs_by_role(d1, "user")
            contents = [m.get("content") for m in users1]
            check("third turn was truncated away", "third turn" not in contents, str(contents))
            check("edited content is present", any("edited second turn" in (x or "") for x in contents), str(contents))
            check("first turn survived", "first turn" in contents, str(contents))
            check("old tool_call rows were truncated", len(d1.get("tool_calls") or []) == 0,
                  str(d1.get("tool_calls")))
            check("re-run produced an assistant reply", len(msgs_by_role(d1, "assistant")) >= 2)
            check("seqs still unique after edit", len({m["seq"] for m in d1["messages"]}) == len(d1["messages"]))

        # ── DELETE truncates from a message onward, NO re-run ───────────────
        section("delete-from-here truncates the tail without re-running")
        _, dconv = c.chat("keep me")
        c.chat("delete from here", conversation_id=dconv)
        dd0 = detail(c, dconv)
        target = find_user_msg(dd0, "delete from here")
        asst_before = len(msgs_by_role(dd0, "assistant"))
        check("found delete target", bool(target))
        if target:
            code = c.delete(f"/api/v1/ai/conversations/{dconv}/messages/{target['id']}")
            check("delete returned 204", code in (200, 204), str(code))
            dd1 = detail(c, dconv)
            contents = [m.get("content") for m in msgs_by_role(dd1, "user")]
            check("deleted turn is gone", "delete from here" not in contents, str(contents))
            check("earlier turn survived", "keep me" in contents, str(contents))
            check("no re-run (assistant count dropped)", len(msgs_by_role(dd1, "assistant")) < asst_before)

        # ── REGENERATE replaces the last assistant turn ─────────────────────
        section("regenerate re-runs the last user turn")
        _, rconv = c.chat("regen me")
        r0 = detail(c, rconv)
        asst_ids_before = {m["id"] for m in msgs_by_role(r0, "assistant")}
        frames = c.stream(f"/api/v1/ai/conversations/{rconv}/regenerate", {})
        check("regenerate streamed a fresh answer", has_event(frames, "delta"))
        check("regenerate completed (done)", has_event(frames, "done"))
        r1 = detail(c, rconv)
        users_r = msgs_by_role(r1, "user")
        asst_ids_after = {m["id"] for m in msgs_by_role(r1, "assistant")}
        check("still exactly one user turn", len(users_r) == 1, str([m.get("content") for m in users_r]))
        check("user content preserved", users_r and "regen me" in (users_r[0].get("content") or ""))
        check("assistant turn was replaced (new id)", bool(asst_ids_after) and asst_ids_after != asst_ids_before,
              f"{asst_ids_before} -> {asst_ids_after}")

    finally:
        # Clean up the conversations we created.
        for cv in (c.get("/api/v1/ai/conversations") or {}).get("conversations", []):
            c.delete(f"/api/v1/ai/conversations/{cv['id']}")
        remove_mock_provider(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
