#!/usr/bin/env python3
"""Per-function KV store via the operator REST surface
(/api/v1/functions/{fn_id}/kv). No sandbox needed — these handlers hit the
DB directly. Lifecycle: create fn -> put key -> get -> list -> validation ->
delete -> confirm gone -> cleanup fn."""
import sys

from harness import OrvaClient, section, check, summary

FN_NAME = "e2e-kv-fn"
KEY = "e2e-kv-key"
KEY2 = "e2e-kv-key2"


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
        section("setup function")
        body = {"name": FN_NAME, "description": "kv host", "runtime": "node24",
                "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        code, created = c.req("POST", "/api/v1/functions", body, expect=range(200, 599))
        check("create fn -> 2xx", 200 <= code < 300, f"status {code}: {str(created)[:160]}")
        fid = (created or {}).get("id") if isinstance(created, dict) else None
        check("created fn has id", bool(fid))
        if not fid:
            return summary()

        base = f"/api/v1/functions/{fid}/kv"

        section("put key")
        # Value is any JSON; envelope is {"value": <json>, "ttl_seconds": <int>}.
        pc, put = c.req("PUT", f"{base}/{KEY}",
                        {"value": {"hits": 1, "name": "alpha"}, "ttl_seconds": 0},
                        expect=range(200, 599))
        check("put -> 200", pc == 200, f"status {pc}: {str(put)[:160]}")
        check("put status ok", isinstance(put, dict) and put.get("status") == "ok")
        check("put echoes key", isinstance(put, dict) and put.get("key") == KEY)

        # Second key so list has >1 entry and prefix filtering is exercisable.
        pc2, _ = c.req("PUT", f"{base}/{KEY2}", {"value": [1, 2, 3]}, expect=range(200, 599))
        check("put 2nd key -> 200", pc2 == 200, f"status {pc2}")

        section("get key")
        gc, got = c.req("GET", f"{base}/{KEY}", expect=range(200, 599))
        check("get -> 200", gc == 200, f"status {gc}")
        check("get found=true", isinstance(got, dict) and got.get("found") is True)
        check("get returns same key", isinstance(got, dict) and got.get("key") == KEY)
        check("get round-trips value",
              isinstance(got, dict) and got.get("value") == {"hits": 1, "name": "alpha"},
              str(got)[:160])

        section("list keys")
        lc, lst = c.req("GET", base, expect=range(200, 599))
        check("list -> 200", lc == 200, f"status {lc}")
        entries = (lst or {}).get("entries") if isinstance(lst, dict) else None
        keys = {e.get("key") for e in (entries or [])}
        check("list contains both keys", {KEY, KEY2} <= keys, str(sorted(keys))[:160])
        check("list reports total", isinstance(lst, dict) and isinstance(lst.get("total"), int))
        # Each entry carries a computed size_bytes — domain-specific assertion.
        e1 = next((e for e in (entries or []) if e.get("key") == KEY), None)
        check("entry has size_bytes > 0", bool(e1) and isinstance(e1.get("size_bytes"), int) and e1["size_bytes"] > 0)

        section("validation")
        # Missing required `value` field -> 400 VALIDATION.
        vc, _ = c.req("PUT", f"{base}/{KEY}", {"ttl_seconds": 5}, expect=range(200, 599))
        check("put without value rejected (4xx)", 400 <= vc < 500, f"status {vc}")
        # KV on an unknown function id -> 404 NOT_FOUND.
        nc, _ = c.req("GET", "/api/v1/functions/00000000-0000-0000-0000-000000000000/kv",
                      expect=range(200, 599))
        check("kv on unknown fn -> 404", nc == 404, f"status {nc}")

        section("delete key")
        dc, deleted = c.req("DELETE", f"{base}/{KEY}", expect=range(200, 599))
        check("delete -> 200", dc == 200, f"status {dc}")
        check("delete status deleted", isinstance(deleted, dict) and deleted.get("status") == "deleted")
        # Idempotent: deleting again still succeeds.
        dc2, _ = c.req("DELETE", f"{base}/{KEY}", expect=range(200, 599))
        check("delete is idempotent (200)", dc2 == 200, f"status {dc2}")

        section("confirm gone")
        gc2, gone = c.req("GET", f"{base}/{KEY}", expect=range(200, 599))
        check("get after delete -> 404", gc2 == 404, f"status {gc2}")
        check("get after delete found=false", isinstance(gone, dict) and gone.get("found") is False)
        # Surviving key still present.
        lc2, lst2 = c.req("GET", base, expect=range(200, 599))
        keys2 = {e.get("key") for e in ((lst2 or {}).get("entries") or [])}
        check("deleted key absent from list", KEY not in keys2)
        check("other key survives", KEY2 in keys2, str(sorted(keys2))[:160])
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
