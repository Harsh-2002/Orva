#!/usr/bin/env python3
"""Function secrets via REST: set → list (names only, value hidden) → delete → gone.

Routes (backend/internal/server/router.go + handlers/secrets.go):
  GET    /api/v1/functions/{fn_id}/secrets        -> {"secrets": [keys]}  (names only)
  POST   /api/v1/functions/{fn_id}/secrets        body {"key","value"} -> {"status":"saved","key"}
  DELETE /api/v1/functions/{fn_id}/secrets/{key}  -> {"status":"deleted","key"}
Secret VALUES are never returned once stored — List returns key NAMES only.
"""
import json
import sys

from harness import OrvaClient, section, check, summary

FN_NAME = "e2e-sec-fn"
KEY = "E2E_SEC_TOKEN"
VALUE = "sk_live_e2e_supersecret_value_8f3a"


def cleanup(c):
    lst = c.get("/api/v1/functions") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") == FN_NAME:
            c.req("DELETE", f"/api/v1/functions/{f['id']}", expect=(200, 204, 404))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    fid = None
    try:
        section("create owning function")
        body = {"name": FN_NAME, "description": "secrets host", "runtime": "node24",
                "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        code, created = c.req("POST", "/api/v1/functions", body, expect=range(200, 599))
        check("create function -> 2xx", 200 <= code < 300, f"status {code}: {str(created)[:160]}")
        fid = (created or {}).get("id") if isinstance(created, dict) else None
        check("function has id", bool(fid))
        if not fid:
            return summary()

        base = f"/api/v1/functions/{fid}/secrets"

        section("list empty")
        lc, empty = c.req("GET", base, expect=range(200, 599))
        check("list -> 200", lc == 200, f"status {lc}")
        check("secrets key present (empty/null guarded)",
              isinstance(empty, dict) and "secrets" in empty, str(empty)[:160])
        check("no secrets yet", KEY not in (((empty or {}).get("secrets")) or []))

        section("set secret")
        sc, saved = c.req("POST", base, {"key": KEY, "value": VALUE}, expect=range(200, 599))
        check("set -> 2xx", 200 <= sc < 300, f"status {sc}: {str(saved)[:160]}")
        check("set echoes key", isinstance(saved, dict) and saved.get("key") == KEY)
        # The set response must never echo the plaintext value back.
        check("set response hides value", VALUE not in json.dumps(saved or {}))

        section("list shows name, hides value")
        glc, got = c.req("GET", base, expect=range(200, 599))
        check("list -> 200", glc == 200, f"status {glc}")
        keys = (((got or {}).get("secrets")) or []) if isinstance(got, dict) else []
        check("secret NAME appears in list", KEY in keys, str(keys)[:160])
        # Critical: the plaintext value must not leak anywhere in the response.
        check("plaintext VALUE not in list response", VALUE not in json.dumps(got or {}))

        section("validation")
        # Missing/blank key must be rejected with 4xx.
        bc, berr = c.req("POST", base, {"value": "orphan"}, expect=range(200, 599))
        check("blank key rejected (4xx)", 400 <= bc < 500, f"status {bc}: {str(berr)[:160]}")
        # Secret op on a non-existent function id -> 404.
        nc = c.status("GET", "/api/v1/functions/00000000-0000-0000-0000-000000000000/secrets")
        check("unknown function -> 404", nc == 404, f"status {nc}")

        section("delete secret")
        dc, deleted = c.req("DELETE", f"{base}/{KEY}", expect=range(200, 599))
        check("delete -> 2xx", 200 <= dc < 300, f"status {dc}: {str(deleted)[:160]}")

        section("confirm gone")
        ac, after = c.req("GET", base, expect=range(200, 599))
        check("list -> 200", ac == 200, f"status {ac}")
        after_keys = (((after or {}).get("secrets")) or []) if isinstance(after, dict) else []
        check("secret no longer listed", KEY not in after_keys, str(after_keys)[:160])

        section("cleanup function")
        fc, _ = c.req("DELETE", f"/api/v1/functions/{fid}", expect=range(200, 599))
        check("delete function -> 2xx", fc in (200, 204), f"status {fc}")
        check("function gone (404)", c.status("GET", f"/api/v1/functions/{fid}") == 404)
        fid = None
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
