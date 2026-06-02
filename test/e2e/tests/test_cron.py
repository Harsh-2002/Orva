#!/usr/bin/env python3
"""Cron schedules via REST (no sandbox needed).

Routes (per-function, nested under the owning function):
  GET    /api/v1/functions/{fn_id}/cron        -> {"schedules":[...]}
  POST   /api/v1/functions/{fn_id}/cron        -> 201 CronSchedule row
  PUT    /api/v1/functions/{fn_id}/cron/{id}   -> 200 CronSchedule row
  DELETE /api/v1/functions/{fn_id}/cron/{id}   -> 200 {"status":"deleted","id":...}
  GET    /api/v1/cron                           -> {"schedules":[...]} across all fns

Body fields are {cron_expr, enabled, payload, timezone}; the function id comes
from the path, NOT the body. Create computes next_run_at server-side.
"""
import sys

from harness import OrvaClient, section, check, summary

FN_NAME = "e2e-cron-fn"


def _find_fn_id(c):
    lst = c.get("/api/v1/functions") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") == FN_NAME:
            return f.get("id")
    return None


def cleanup(c):
    fid = _find_fn_id(c)
    if not fid:
        return
    # Remove any schedules left on this function, then the function itself.
    lst = c.req("GET", f"/api/v1/functions/{fid}/cron", expect=range(200, 599))[1]
    if isinstance(lst, dict):
        for s in (lst.get("schedules") or []):
            c.req("DELETE", f"/api/v1/functions/{fid}/cron/{s['id']}", expect=range(200, 599))
    c.req("DELETE", f"/api/v1/functions/{fid}", expect=(200, 204, 404))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    fid = None
    sid = None
    try:
        section("setup function")
        body = {"name": FN_NAME, "description": "cron owner", "runtime": "node24",
                "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        fc, fn = c.req("POST", "/api/v1/functions", body, expect=range(200, 599))
        check("function create -> 2xx", 200 <= fc < 300, f"status {fc}: {str(fn)[:160]}")
        fid = (fn or {}).get("id") if isinstance(fn, dict) else None
        check("function has id", bool(fid))
        if not fid:
            return summary()

        section("create schedule")
        sched_body = {"cron_expr": "*/5 * * * *", "enabled": True,
                      "payload": {"source": "e2e-cron"}}
        sc, sched = c.req("POST", f"/api/v1/functions/{fid}/cron", sched_body, expect=range(200, 599))
        check("create -> 201", sc == 201, f"status {sc}: {str(sched)[:160]}")
        sid = (sched or {}).get("id") if isinstance(sched, dict) else None
        check("schedule has id", bool(sid))
        check("cron_expr echoed", isinstance(sched, dict) and sched.get("cron_expr") == "*/5 * * * *")
        check("enabled is true", isinstance(sched, dict) and sched.get("enabled") is True)
        check("function_id matches", isinstance(sched, dict) and sched.get("function_id") == fid)
        check("computed next_run_at present", isinstance(sched, dict) and bool(sched.get("next_run_at")),
              str(sched)[:160])

        section("list (per-function + global)")
        lst = c.get(f"/api/v1/functions/{fid}/cron") or {}
        rows = lst.get("schedules") or []
        check("appears in per-function list", any(s.get("id") == sid for s in rows))
        match = next((s for s in rows if s.get("id") == sid), None)
        check("listed row has next_run_at", bool(match) and bool(match.get("next_run_at")))

        gall = c.get("/api/v1/cron") or {}
        check("appears in global /cron list",
              any(s.get("id") == sid for s in (gall.get("schedules") or [])))

        section("update (toggle enabled + change expr)")
        if sid:
            uc, upd = c.req("PUT", f"/api/v1/functions/{fid}/cron/{sid}",
                            {"enabled": False, "cron_expr": "0 */2 * * *"},
                            expect=range(200, 599))
            check("update -> 200", uc == 200, f"status {uc}: {str(upd)[:160]}")
            check("enabled toggled off", isinstance(upd, dict) and upd.get("enabled") is False)
            check("cron_expr updated", isinstance(upd, dict) and upd.get("cron_expr") == "0 */2 * * *")

        section("validation")
        bad = {"cron_expr": "not a valid cron", "enabled": True}
        bc, _ = c.req("POST", f"/api/v1/functions/{fid}/cron", bad, expect=range(200, 599))
        check("invalid cron_expr rejected (4xx)", 400 <= bc < 500, f"status {bc}")

        empty = {"cron_expr": "", "enabled": True}
        ec, _ = c.req("POST", f"/api/v1/functions/{fid}/cron", empty, expect=range(200, 599))
        check("empty cron_expr rejected (4xx)", 400 <= ec < 500, f"status {ec}")

        section("delete")
        if sid:
            dc, dr = c.req("DELETE", f"/api/v1/functions/{fid}/cron/{sid}", expect=range(200, 599))
            check("delete -> 2xx", dc in (200, 204), f"status {dc}")
            after = c.get(f"/api/v1/functions/{fid}/cron") or {}
            check("gone after delete",
                  not any(s.get("id") == sid for s in (after.get("schedules") or [])))
            # Deleting again resolves nothing -> 404.
            check("re-delete -> 404",
                  c.status("DELETE", f"/api/v1/functions/{fid}/cron/{sid}") == 404)
            sid = None
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
