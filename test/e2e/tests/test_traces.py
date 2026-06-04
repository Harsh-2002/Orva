#!/usr/bin/env python3
"""Causal traces: list (read-only), get-by-id 404, function baseline snapshot.

Traces are a set of executions sharing a trace_id; the list contains only ROOT
spans. On a fresh instance there is no traffic, so the list is empty — we assert
the 200 + list shape rather than requiring rows. The baseline endpoint is always
200 with a zero-sample snapshot for a brand-new function. Mostly read-only; the
only resource we create is a function (no sandbox needed), which we clean up.
"""
import sys

from harness import OrvaClient, section, check, summary

NAME = "e2e-trace-fn"


def cleanup(c):
    lst = c.get("/api/v1/functions") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") == NAME:
            c.req("DELETE", f"/api/v1/functions/{f['id']}", expect=(200, 204, 404))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    fid = None
    try:
        section("list traces")
        lc, lst = c.req("GET", "/api/v1/traces", expect=range(200, 599))
        check("list -> 200", lc == 200, f"status {lc}")
        check("response is object with traces field",
              isinstance(lst, dict) and "traces" in lst, str(lst)[:160])
        traces = (lst or {}).get("traces")
        check("traces is a list (may be empty)", isinstance(traces, list), str(traces)[:160])
        check("has limit field", isinstance(lst, dict) and isinstance(lst.get("limit"), int),
              str(lst)[:160])
        # next_cursor is present in the contract; empty when no further page.
        check("has next_cursor field", isinstance(lst, dict) and "next_cursor" in lst,
              str(lst)[:160])

        section("list traces with filters")
        # Filter params are honored (function_id / status / limit); a bogus
        # function_id simply yields an empty result, still 200.
        fc, flt = c.req("GET", "/api/v1/traces?function_id=does-not-exist&limit=5",
                        expect=range(200, 599))
        check("filtered list -> 200", fc == 200, f"status {fc}")
        check("filtered traces is empty list",
              isinstance(flt, dict) and (flt.get("traces") or []) == [],
              str(flt)[:160])
        check("filtered limit echoes 5", isinstance(flt, dict) and flt.get("limit") == 5,
              str(flt)[:160])

        section("get trace by id")
        # Unknown trace id -> 404 (no spans for trace).
        gc, _ = c.req("GET", "/api/v1/traces/e2e-trace-does-not-exist", expect=range(200, 599))
        check("unknown trace -> 404", gc == 404, f"status {gc}")
        # If the fresh instance happens to have any trace, fetch the first and
        # validate the detailed shape; otherwise this is a no-op (still PASS).
        if traces:
            tid = traces[0].get("trace_id")
            if tid:
                dc, det = c.req("GET", f"/api/v1/traces/{tid}", expect=range(200, 599))
                check("existing trace get -> 200", dc == 200, f"status {dc}")
                check("trace detail has spans list",
                      isinstance(det, dict) and isinstance(det.get("spans"), list),
                      str(det)[:160])
                check("trace detail trace_id matches",
                      isinstance(det, dict) and det.get("trace_id") == tid)
        else:
            check("no traces on fresh instance (expected)", True)

        section("create function for baseline")
        body = {"name": NAME, "description": "baseline", "runtime": "node",
                "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        cc, created = c.req("POST", "/api/v1/functions", body, expect=range(200, 599))
        check("create function -> 2xx", 200 <= cc < 300, f"status {cc}: {str(created)[:160]}")
        fid = (created or {}).get("id") if isinstance(created, dict) else None
        check("created has id", bool(fid))

        section("function baseline")
        if fid:
            bc, base = c.req("GET", f"/api/v1/functions/{fid}/baseline", expect=range(200, 599))
            check("baseline -> 200 (always)", bc == 200, f"status {bc}")
            check("baseline is object", isinstance(base, dict), str(base)[:160])
            check("baseline function_id matches",
                  isinstance(base, dict) and base.get("function_id") == fid,
                  str(base)[:160])
            # No traffic yet -> zero samples; window_size is a fixed positive const.
            check("baseline sample_count == 0 (no traffic)",
                  isinstance(base, dict) and base.get("sample_count") == 0,
                  str(base)[:160])
            check("baseline window_size > 0",
                  isinstance(base, dict) and isinstance(base.get("window_size"), int)
                  and base.get("window_size") > 0, str(base)[:160])

        section("baseline by name")
        # The baseline route resolves name -> id (mirrors functions helpers).
        nc, nbase = c.req("GET", f"/api/v1/functions/{NAME}/baseline", expect=range(200, 599))
        check("baseline by name -> 200", nc == 200, f"status {nc}")
        check("baseline by name resolves to same fn",
              isinstance(nbase, dict) and nbase.get("function_id") == fid,
              str(nbase)[:160])

        section("cleanup function")
        if fid:
            dc, _ = c.req("DELETE", f"/api/v1/functions/{fid}", expect=range(200, 599))
            check("delete function -> 2xx", dc in (200, 204), f"status {dc}")
            check("gone after delete (404)",
                  c.status("GET", f"/api/v1/functions/{fid}") == 404)
            fid = None
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
