#!/usr/bin/env python3
"""Agent channels CRUD via REST: create a function, bundle it into a channel,
assert the channel lists + yields an orva_chn_ token, delete, confirm gone."""
import sys

from harness import OrvaClient, section, check, summary

CHAN = "e2e-chn-bundle"
FN = "e2e-chn-fn"


def cleanup(c):
    for ch in ((c.get("/api/v1/channels") or {}).get("channels") or []):
        if ch.get("name") == CHAN:
            c.req("DELETE", f"/api/v1/channels/{ch['id']}", expect=(200, 204, 404))
    for f in ((c.get("/api/v1/functions") or {}).get("functions") or []):
        if f.get("name") == FN:
            c.req("DELETE", f"/api/v1/functions/{f['id']}", expect=(200, 204, 404))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    fid = None
    cid = None
    try:
        section("setup function")
        fbody = {"name": FN, "description": "channel member", "runtime": "node24",
                 "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                 "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        fc, fn = c.req("POST", "/api/v1/functions", fbody, expect=range(200, 599))
        check("function create -> 2xx", 200 <= fc < 300, f"status {fc}: {str(fn)[:160]}")
        fid = (fn or {}).get("id") if isinstance(fn, dict) else None
        check("function has id", bool(fid))

        section("create channel")
        cbody = {"name": CHAN, "description": "e2e bundle",
                 "function_ids": [fid] if fid else []}
        cc, created = c.req("POST", "/api/v1/channels", cbody, expect=range(200, 599))
        check("channel create -> 201", cc == 201, f"status {cc}: {str(created)[:160]}")
        created = created if isinstance(created, dict) else {}
        cid = created.get("id")
        check("channel has id", bool(cid))
        check("channel name matches", created.get("name") == CHAN)
        tok = created.get("token") or ""
        check("returns orva_chn_ token", isinstance(tok, str) and tok.startswith("orva_chn_"),
              f"token={tok[:24]!r}")
        check("token has 32 hex body", len(tok) == len("orva_chn_") + 32, f"len={len(tok)}")
        check("prefix is orva_chn_ public head",
              isinstance(created.get("prefix"), str) and created["prefix"].startswith("orva_chn_"))
        check("function_ids echoes member", created.get("function_ids") == ([fid] if fid else []))

        section("read + list")
        if cid:
            gc, got = c.req("GET", f"/api/v1/channels/{cid}", expect=range(200, 599))
            check("get by id -> 200", gc == 200, f"status {gc}")
            got = got if isinstance(got, dict) else {}
            check("get returns same id", got.get("id") == cid)
            check("get does NOT leak token", "token" not in got, str(got.keys()))
            check("get binds the function", (got.get("function_ids") or []) == ([fid] if fid else []))
        lst = (c.get("/api/v1/channels") or {}).get("channels") or []
        item = next((x for x in lst if x.get("id") == cid), None)
        check("appears in list", item is not None)
        if item is not None:
            check("list reports function_count=1", item.get("function_count") == 1,
                  f"count={item.get('function_count')}")
            check("list exposes prefix, not full token",
                  bool(item.get("prefix")) and "token" not in item)

        section("validation")
        bc, _ = c.req("POST", "/api/v1/channels", {"name": "e2e-chn-bad"}, expect=range(200, 599))
        check("missing function_ids rejected (4xx)", 400 <= bc < 500, f"status {bc}")
        nc, _ = c.req("POST", "/api/v1/channels",
                      {"function_ids": [fid] if fid else []}, expect=range(200, 599))
        check("missing name rejected (4xx)", 400 <= nc < 500, f"status {nc}")

        section("delete")
        if cid:
            dc, _ = c.req("DELETE", f"/api/v1/channels/{cid}", expect=range(200, 599))
            check("delete -> 2xx", dc in (200, 204), f"status {dc}")
            check("gone after delete (404)", c.status("GET", f"/api/v1/channels/{cid}") == 404)
            gone = (c.get("/api/v1/channels") or {}).get("channels") or []
            check("absent from list", all(x.get("id") != cid for x in gone))
            cid = None
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
