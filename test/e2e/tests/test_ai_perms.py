#!/usr/bin/env python3
"""
test_perms.py — prove the agent's tool catalog is permission-scoped: a
read-only API key can never drive a WRITE tool.

Two enforcement layers protect a read-only key, and EITHER one passing is a
valid proof of the scoping guarantee:

  1. REST middleware: the ENTIRE /api/v1/ai/* surface requires the "admin"
     permission (requiredPermission() returns "admin" for any /api/v1/ai/ path,
     GET included), so a key with permissions=["read"] gets 403 FORBIDDEN on
     every AI route — it can't even open the agentic loop to attempt a write.

  2. Agent tool catalog: the AI handler scopes the in-process tool registry to
     the caller's permission set (BuildAgentRegistry(deps, perms)). A read-only
     catalog contains read tools (list_functions) but NOT write tools
     (create_function), so a CALL create_function directive dispatches against
     an empty/unknown tool and yields a failed tool_result / error. (With the
     admin gate above, layer 1 fires first; layer 2 is the defense-in-depth
     fallback if the gate were ever relaxed.)

This test drives BOTH a read tool (must work, modulo the 403 gate) and a write
tool (must NOT take effect), then verifies via the ADMIN client that no
function was ever created. It handles whichever layer fires.

Run:  ORVA_API_KEY=$(sudo cat /var/lib/orva/.admin-key) python3 test/ai/test_perms.py
(or set ORVA_URL/ORVA_API_KEY for any instance).
"""
import sys
import urllib.error

from harness import (
    OrvaClient, start_mock, configure_mock_provider, remove_mock_provider,
    section, check, summary, events_of, has_event, first_tool_call,
)

RO_KEY_NAME = "e2e-perm-readonly"
PERM_FN = "e2e-perm-fn"

# create_function args used to probe the write path. node / minimal limits.
CREATE_ARGS = (
    '{"name":"%s","description":"x","runtime":"node",'
    '"entrypoint":"handler.js","timeout_ms":30000,"memory_mb":128,'
    '"cpus":1,"network_mode":"none","auth_mode":"none"}'
) % PERM_FN


def fn_id_by_name(c, name):
    """Look up a function id by name via the ADMIN client (REST resolves by
    UUID only, so we list and match on name)."""
    try:
        lst = c.get("/api/v1/functions") or {}
        for fn in lst.get("functions", []):
            if fn.get("name") == name:
                return fn.get("id")
    except Exception:
        pass
    return None


def cleanup_fn(c, name):
    fid = fn_id_by_name(c, name)
    if fid:
        try:
            c.req("DELETE", f"/api/v1/functions/{fid}", expect=(200, 204, 404))
        except Exception:
            pass


def safe_chat(client, content):
    """Run client.chat but tolerate an HTTP error on the POST itself (a
    read-only key gets 403 on the non-GET /ai/chat route before any frames
    are streamed). Returns (frames, conv, http_status). http_status is None
    when the stream opened normally (200); otherwise it's the error code."""
    try:
        frames, conv = client.chat(content)
        return frames, conv, None
    except urllib.error.HTTPError as e:
        return [], None, e.code
    except urllib.error.URLError:
        # Some stacks surface the HTTP status wrapped; treat as gate failure.
        return [], None, -1


def main():
    c = OrvaClient()  # admin client (full perms)
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2

    start_mock()
    configure_mock_provider(c, approval="auto")
    cleanup_fn(c, PERM_FN)

    ro_key_id = None
    try:
        # ── 0. mint a read-only API key via admin ────────────────────────
        section("admin mints a read-only API key")
        created = c.post("/api/v1/keys", {
            "name": RO_KEY_NAME, "permissions": ["read"], "expires_in_days": 1,
        }) or {}
        ro_key_id = created.get("id")
        ro_key = created.get("key")
        check("key created with an id", bool(ro_key_id), str(created)[:160])
        check("plaintext key returned", bool(ro_key) and str(ro_key).startswith("orva_"),
              str(created.get("prefix")))
        check("key scoped to read only", created.get("permissions") == ["read"],
              str(created.get("permissions")))

        if not ro_key:
            return summary()

        # Second client authenticated with the read-only key.
        ro = OrvaClient(api_key=ro_key)

        # Sanity: the read-only key CAN do a plain GET (read perm honored).
        section("read-only key authenticates for reads")
        code, _ = ro.req("GET", "/api/v1/functions", expect=range(200, 600))
        check("read-only key GET /functions -> 200", code == 200, f"status {code}")

        # Regression lock: the AI surface is admin-only, so even a GET AI route
        # is 403 for a read key (not just the chat POST).
        section("read-only key is admin-gated off ALL of /api/v1/ai/*")
        gc, _ = ro.req("GET", "/api/v1/ai/conversations", expect=range(200, 600))
        check("read-only key GET /ai/conversations -> 403", gc == 403, f"status {gc}")
        sc, _ = ro.req("GET", "/api/v1/ai/settings", expect=range(200, 600))
        check("read-only key GET /ai/settings -> 403", sc == 403, f"status {sc}")

        # ── 1. a READ tool via the agent ─────────────────────────────────
        # The AI surface is admin-only, so a read-only key gets 403 on the chat
        # POST — which itself proves it can't drive the agent loop. (If the gate
        # were ever relaxed to allow the POST, the read tool would still succeed.)
        section("read-only key: read tool (list_functions)")
        frames, conv, status = safe_chat(ro, "CALL list_functions {}")
        if status is not None:
            check("chat POST gated by admin perm (403)", status == 403, f"status {status}")
        else:
            tc = first_tool_call(frames)
            check("read tool dispatched", bool(tc) and tc.get("name") == "list_functions", str(tc))
            results = events_of(frames, "tool_result")
            check("read tool_result succeeded",
                  any(r.get("status") == "succeeded" for r in results), str(results))
            check("read tool stream completed", has_event(frames, "done"))

        # ── 2. a WRITE tool via the agent must NOT take effect ────────────
        section("read-only key: write tool (create_function) is unavailable")
        cleanup_fn(c, PERM_FN)  # ensure clean slate before the probe
        frames, conv, status = safe_chat(ro, "CALL create_function " + CREATE_ARGS)

        if status is not None:
            # Layer 1: middleware refused the POST outright.
            check("write attempt blocked at REST gate (403)", status == 403, f"status {status}")
        else:
            # Layer 2: the POST opened but the read-only catalog has no
            # create_function — dispatch must fail (unknown tool) or error,
            # and it must NOT pause for approval (approval=auto anyway).
            tc = first_tool_call(frames)
            results = events_of(frames, "tool_result")
            failed_results = [r for r in results if r.get("status") in ("failed", "error", "rejected")]
            wrote = any(r.get("status") == "succeeded" for r in results)
            check("create_function tool_result did NOT succeed", not wrote,
                  str(results)[:200])
            check("write attempt errored or produced a failed tool_result",
                  has_event(frames, "error") or bool(failed_results) or not tc,
                  f"tc={tc} results={results}")

        # ── 3. AUTHORITATIVE check: no e2e-perm-fn exists ─────────────────
        # Regardless of which layer fired, the admin client must see no
        # function with the probed name — the write never took effect.
        section("admin confirms the write never landed")
        fid = fn_id_by_name(c, PERM_FN)
        check("no function created by the read-only key", fid is None,
              f"unexpected function id {fid}")

    finally:
        # Delete the probe function if it somehow exists (id-based, admin).
        cleanup_fn(c, PERM_FN)
        # Delete the read-only key (admin perm required for /keys).
        if ro_key_id:
            try:
                c.req("DELETE", f"/api/v1/keys/{ro_key_id}", expect=(200, 204, 404))
            except Exception:
                pass
        remove_mock_provider(c)

    return summary()


if __name__ == "__main__":
    sys.exit(main())
