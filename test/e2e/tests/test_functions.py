#!/usr/bin/env python3
"""Function CRUD via REST (no sandbox needed): create, get, list, validation, delete."""
import sys

from harness import OrvaClient, section, check, summary

NAME = "e2e-fns-crud"


def cleanup(c):
    lst = c.get("/api/v1/functions") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") in (NAME, "e2e-bad-fn"):
            c.req("DELETE", f"/api/v1/functions/{f['id']}", expect=(200, 204, 404))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    fid = None
    try:
        section("create function")
        body = {"name": NAME, "description": "crud", "runtime": "node24",
                "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        code, created = c.req("POST", "/api/v1/functions", body, expect=range(200, 599))
        check("create -> 2xx", 200 <= code < 300, f"status {code}: {str(created)[:160]}")
        fid = (created or {}).get("id") if isinstance(created, dict) else None
        check("created has id", bool(fid))
        check("created name matches", isinstance(created, dict) and created.get("name") == NAME)

        section("get + list")
        if fid:
            gc, got = c.req("GET", f"/api/v1/functions/{fid}", expect=range(200, 599))
            check("get by id -> 200", gc == 200, f"status {gc}")
            check("get returns same id", isinstance(got, dict) and got.get("id") == fid)
        lst = c.get("/api/v1/functions") or {}
        check("appears in list", any(f.get("name") == NAME for f in (lst.get("functions") or [])))

        section("validation")
        # REST create fills sensible defaults, so a minimal body is accepted;
        # validate with a genuinely invalid runtime, which must be rejected (4xx).
        bad = {"name": "e2e-bad-fn", "description": "x", "runtime": "not-a-real-runtime",
               "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
               "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        bc, _ = c.req("POST", "/api/v1/functions", bad, expect=range(200, 599))
        check("invalid runtime rejected (4xx)", 400 <= bc < 500, f"status {bc}")

        section("delete")
        if fid:
            dc, _ = c.req("DELETE", f"/api/v1/functions/{fid}", expect=range(200, 599))
            check("delete -> 2xx", dc in (200, 204), f"status {dc}")
            check("gone after delete (404)", c.status("GET", f"/api/v1/functions/{fid}") == 404)
            fid = None
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
