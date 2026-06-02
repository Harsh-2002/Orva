#!/usr/bin/env python3
"""
test_conversations.py — end-to-end coverage for the AI assistant's conversation
lifecycle: create / list / send-into / rehydrate (messages + tool calls) /
rename / archive / delete. Driven entirely by the mock LLM (no real provider).

Verifies that messages and tool calls are persisted under the right
conversation and rehydrate cleanly from the REST detail endpoint:
  POST /api/v1/ai/conversations
  GET  /api/v1/ai/conversations[?archived=true]
  GET  /api/v1/ai/conversations/{id}      -> {conversation, messages, tool_calls}
  PATCH /api/v1/ai/conversations/{id}      {title|archived}
  DELETE /api/v1/ai/conversations/{id}     -> 204

Run:  ORVA_API_KEY=$(sudo cat /var/lib/orva/.admin-key) python3 test/ai/test_conversations.py
(or set ORVA_URL/ORVA_API_KEY for any instance).
"""
import sys

from harness import (
    OrvaClient, start_mock, configure_mock_provider, remove_mock_provider,
    section, check, summary, events_of, has_event,
)

CONV_TITLE = "e2e-conv"


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    start_mock()
    # auto policy: nothing pauses for approval, so tool chats complete in one stream.
    configure_mock_provider(c, approval="auto")

    conv_id = None
    try:
        # ── 1. create a conversation ──────────────────────────────────────
        section("create conversation")
        code, created = c.req("POST", "/api/v1/ai/conversations", {"title": CONV_TITLE})
        check("POST returns 200", code == 200, f"status {code}")
        conv = (created or {}).get("conversation") if isinstance(created, dict) else None
        conv_id = conv.get("id") if isinstance(conv, dict) else None
        check("conversation has an id", bool(conv_id), str(created)[:200])
        check("conversation title is e2e-conv", bool(conv) and conv.get("title") == CONV_TITLE, str(conv)[:200])
        check("new conversation is not archived", bool(conv) and conv.get("archived") is False, str(conv)[:200])

        # ── 2. list shows it ──────────────────────────────────────────────
        section("list shows the conversation")
        lst = c.get("/api/v1/ai/conversations") or {}
        ids = [x.get("id") for x in lst.get("conversations", [])]
        check("GET conversations lists the new id", conv_id in ids, str(ids)[:200])

        # ── 3. send a chat INTO the conversation ──────────────────────────
        section("chat into the conversation")
        frames, _ = c.chat("hello there", conversation_id=conv_id)
        check("message_start emitted", has_event(frames, "message_start"))
        check("delta streamed", has_event(frames, "delta"))
        check("done emitted", has_event(frames, "done"))

        # ── 4. detail rehydrates user + assistant messages, ordered by seq ─
        section("detail rehydrates messages (ordered by seq)")
        detail = c.get(f"/api/v1/ai/conversations/{conv_id}") or {}
        msgs = detail.get("messages") or []
        check("detail has messages", len(msgs) >= 2, f"{len(msgs)} messages")
        roles = [m.get("role") for m in msgs]
        user_msgs = [m for m in msgs if m.get("role") == "user"]
        check("a user message was persisted", bool(user_msgs), str(roles))
        check("user message content is 'hello there'",
              any(m.get("content") == "hello there" for m in user_msgs), str(user_msgs)[:200])
        check("an assistant message was persisted",
              any(m.get("role") == "assistant" for m in msgs), str(roles))
        seqs = [m.get("seq") for m in msgs]
        check("seq values are strictly increasing",
              all(isinstance(s, int) for s in seqs) and all(b > a for a, b in zip(seqs, seqs[1:])),
              str(seqs))

        # ── 5. tool chat persists a succeeded tool_call row ───────────────
        section("tool chat persists a tool_call (list_functions)")
        tframes, _ = c.chat("CALL list_functions {}", conversation_id=conv_id)
        check("tool chat reached done", has_event(tframes, "done"))
        # no approval pause under auto policy
        check("auto policy did not pause", not has_event(tframes, "awaiting_approval"))
        detail = c.get(f"/api/v1/ai/conversations/{conv_id}") or {}
        tcs = detail.get("tool_calls") or []
        lf = [t for t in tcs if t.get("tool_name") == "list_functions"]
        check("detail.tool_calls has a list_functions row", bool(lf), str([t.get("tool_name") for t in tcs]))
        check("list_functions tool_call status is succeeded",
              any(t.get("status") == "succeeded" for t in lf), str(lf)[:240])

        # ── 6. rename via PATCH ───────────────────────────────────────────
        section("rename via PATCH")
        code, renamed = c.req("PATCH", f"/api/v1/ai/conversations/{conv_id}", {"title": "renamed"})
        check("PATCH title returns 200", code == 200, f"status {code}")
        rconv = (renamed or {}).get("conversation") if isinstance(renamed, dict) else None
        check("PATCH response shows new title", bool(rconv) and rconv.get("title") == "renamed", str(rconv)[:200])
        detail = c.get(f"/api/v1/ai/conversations/{conv_id}") or {}
        check("GET detail shows renamed title",
              (detail.get("conversation") or {}).get("title") == "renamed",
              str(detail.get("conversation"))[:200])

        # ── 7. archive via PATCH; list filtering ──────────────────────────
        section("archive via PATCH and list filtering")
        code, arch = c.req("PATCH", f"/api/v1/ai/conversations/{conv_id}", {"archived": True})
        check("PATCH archived returns 200", code == 200, f"status {code}")
        aconv = (arch or {}).get("conversation") if isinstance(arch, dict) else None
        check("PATCH response shows archived true", bool(aconv) and aconv.get("archived") is True, str(aconv)[:200])

        active = c.get("/api/v1/ai/conversations") or {}
        active_ids = [x.get("id") for x in active.get("conversations", [])]
        check("default list excludes archived conversation", conv_id not in active_ids, str(active_ids)[:200])

        archived = c.get("/api/v1/ai/conversations?archived=true") or {}
        archived_ids = [x.get("id") for x in archived.get("conversations", [])]
        check("archived=true list includes the conversation", conv_id in archived_ids, str(archived_ids)[:200])

        # ── 8. delete; gone afterwards ────────────────────────────────────
        section("delete and confirm gone")
        code = c.delete(f"/api/v1/ai/conversations/{conv_id}")
        check("DELETE returns 204", code == 204, f"status {code}")
        gcode, _ = c.req("GET", f"/api/v1/ai/conversations/{conv_id}", expect=range(200, 599))
        check("GET deleted conversation returns 404", gcode == 404, f"status {gcode}")
        conv_id = None  # already deleted; skip cleanup

    finally:
        if conv_id:
            try:
                c.req("DELETE", f"/api/v1/ai/conversations/{conv_id}", expect=range(200, 599))
            except Exception:
                pass
        remove_mock_provider(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
