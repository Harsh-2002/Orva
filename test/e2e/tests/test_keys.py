#!/usr/bin/env python3
"""API keys + permission scopes via REST.

Lifecycle: create a scoped key (plaintext returned ONCE) → list (plaintext NOT
returned) → use the read-only key (read allowed, write forbidden 403) →
validation error (missing name -> 4xx) → delete → confirm gone.

Routes (see backend/internal/server/router.go + handlers/keys.go):
  POST   /api/v1/keys            -> 201 {id, key(plaintext, once), prefix, name, permissions, expires_at, created_at}
  GET    /api/v1/keys            -> 200 {keys: [{id, prefix, name, created_at, last_used_at, expires_at}]}  (no plaintext, no hash)
  DELETE /api/v1/keys/{key_id}   -> 200 {status:"deleted", id}

Permission model (middleware_auth.requiredPermission): GET needs "read",
POST/PUT/DELETE need "write"; /api/v1/keys itself needs "admin". So a key with
only ["read"] can GET /functions but is 403 FORBIDDEN on POST /functions.
"""
import sys

from harness import OrvaClient, section, check, summary

NAME = "e2e-keys-ro"


def cleanup(c):
    lst = c.get("/api/v1/keys") or {}
    for k in (lst.get("keys") or []):
        if k.get("name") == NAME:
            c.req("DELETE", f"/api/v1/keys/{k['id']}", expect=(200, 204, 404))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    kid = None
    try:
        section("create scoped api key")
        body = {"name": NAME, "permissions": ["read"], "expires_in_days": 1}
        code, created = c.req("POST", "/api/v1/keys", body, expect=range(200, 599))
        check("create -> 2xx", 200 <= code < 300, f"status {code}: {str(created)[:160]}")
        kid = (created or {}).get("id") if isinstance(created, dict) else None
        plaintext = (created or {}).get("key") if isinstance(created, dict) else None
        check("created has id", bool(kid))
        check("plaintext key returned once", bool(plaintext) and str(plaintext).startswith("orva_"),
              str(created)[:160])
        check("created name matches", isinstance(created, dict) and created.get("name") == NAME)
        check("permissions echoed = [read]", isinstance(created, dict) and created.get("permissions") == ["read"],
              str((created or {}).get("permissions")))
        check("expires_at populated (expires_in_days honored)",
              isinstance(created, dict) and bool(created.get("expires_at")))

        section("list — plaintext NOT exposed")
        lst = c.get("/api/v1/keys") or {}
        keys = lst.get("keys") or []
        mine = next((k for k in keys if k.get("id") == kid), None)
        check("created key appears in list", mine is not None)
        if mine is not None:
            check("list entry has prefix", bool(mine.get("prefix")))
            check("list does NOT return plaintext key", "key" not in mine, str(mine)[:160])
            check("list does NOT return key hash",
                  "key_hash" not in mine and "keyHash" not in mine, str(mine)[:160])

        section("scope enforcement — read-only key")
        if plaintext:
            ro = OrvaClient(api_key=plaintext)
            rc = ro.status("GET", "/api/v1/functions")
            check("read-only key: GET /functions allowed (2xx)", 200 <= rc < 300, f"status {rc}")
            wbody = {"name": "e2e-keys-ro-fn", "runtime": "node",
                     "entrypoint": "handler.js", "timeout_ms": 30000,
                     "memory_mb": 128, "cpus": 1, "network_mode": "none",
                     "auth_mode": "none"}
            wc = ro.status("POST", "/api/v1/functions", wbody)
            check("read-only key: POST /functions forbidden (403)", wc == 403, f"status {wc}")
            # The read-only key must also be locked out of key management (admin scope).
            ac = ro.status("GET", "/api/v1/keys")
            check("read-only key: GET /keys forbidden (admin, 403)", ac == 403, f"status {ac}")

        section("validation — missing name")
        bc, berr = c.req("POST", "/api/v1/keys", {"permissions": ["read"]}, expect=range(200, 599))
        check("create without name rejected (4xx)", 400 <= bc < 500, f"status {bc}: {str(berr)[:160]}")

        section("delete + confirm gone")
        if kid:
            dc, dresp = c.req("DELETE", f"/api/v1/keys/{kid}", expect=range(200, 599))
            check("delete -> 2xx", dc in (200, 204), f"status {dc}")
            after = c.get("/api/v1/keys") or {}
            check("gone from list after delete",
                  not any(k.get("id") == kid for k in (after.get("keys") or [])))
            # The deleted plaintext key must no longer authenticate.
            if plaintext:
                gone = OrvaClient(api_key=plaintext)
                gc = gone.status("GET", "/api/v1/functions")
                check("deleted key no longer authenticates (401)", gc == 401, f"status {gc}")
            kid = None
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
