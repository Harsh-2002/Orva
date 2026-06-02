#!/usr/bin/env python3
"""Background job queue CRUD + state via REST (no sandbox needed):
create function -> enqueue job -> list/get -> retry -> validation -> delete -> gone.

Job EXECUTION needs nsjail; this module only exercises the queue's CRUD + state
machine, none of which dispatches a sandbox. See backend/internal/server/handlers/jobs.go:
  POST   /api/v1/jobs            enqueue  -> 201, body is the Job row
  GET    /api/v1/jobs            list     -> 200 {"jobs": [...]}
  GET    /api/v1/jobs/{id}       get      -> 200 Job
  POST   /api/v1/jobs/{id}/retry retry    -> 200 {"status":"pending","id":...}
  DELETE /api/v1/jobs/{id}       delete   -> 200 {"status":"deleted","id":...}
"""
import sys

from harness import OrvaClient, section, check, summary

FN_NAME = "e2e-jobs-fn"


def cleanup(c):
    # Delete any jobs belonging to our function, then the function itself.
    lst = c.get("/api/v1/functions") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") != FN_NAME:
            continue
        fid = f.get("id")
        jl = c.get(f"/api/v1/jobs?function_id={fid}") or {}
        for j in (jl.get("jobs") or []):
            c.req("DELETE", f"/api/v1/jobs/{j['id']}", expect=range(200, 599))
        c.req("DELETE", f"/api/v1/functions/{fid}", expect=(200, 204, 404))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    fid = None
    jid = None
    try:
        section("setup: create owning function")
        fnbody = {"name": FN_NAME, "description": "jobs queue target",
                  "runtime": "node24", "entrypoint": "handler.js",
                  "timeout_ms": 30000, "memory_mb": 128, "cpus": 1,
                  "network_mode": "none", "auth_mode": "none"}
        fc, fn = c.req("POST", "/api/v1/functions", fnbody, expect=range(200, 599))
        check("function create -> 2xx", 200 <= fc < 300, f"status {fc}: {str(fn)[:160]}")
        fid = (fn or {}).get("id") if isinstance(fn, dict) else None
        check("function has id", bool(fid))
        if not fid:
            return summary()

        section("enqueue job")
        ec, job = c.req("POST", "/api/v1/jobs",
                        {"function_id": fid, "payload": {"hello": "world", "n": 1}},
                        expect=range(200, 599))
        check("enqueue -> 201", ec == 201, f"status {ec}: {str(job)[:160]}")
        jid = (job or {}).get("id") if isinstance(job, dict) else None
        check("job has id", bool(jid))
        check("job bound to function", isinstance(job, dict) and job.get("function_id") == fid)
        check("job starts pending/queued",
              isinstance(job, dict) and job.get("status") in ("pending", "queued"),
              f"status={job.get('status') if isinstance(job, dict) else job!r}")
        check("default max_attempts > 0",
              isinstance(job, dict) and (job.get("max_attempts") or 0) > 0)

        section("list + get")
        ll = c.get("/api/v1/jobs") or {}
        jobs = ll.get("jobs") or []
        check("appears in unfiltered list", any(j.get("id") == jid for j in jobs))
        fl = c.get(f"/api/v1/jobs?function_id={fid}") or {}
        check("appears in function-filtered list",
              any(j.get("id") == jid for j in (fl.get("jobs") or [])))
        sl = c.get("/api/v1/jobs?status=pending") or {}
        check("appears in status=pending filter",
              any(j.get("id") == jid for j in (sl.get("jobs") or [])))
        if jid:
            gc, got = c.req("GET", f"/api/v1/jobs/{jid}", expect=range(200, 599))
            check("get by id -> 200", gc == 200, f"status {gc}")
            check("get returns same id", isinstance(got, dict) and got.get("id") == jid)

        section("retry")
        if jid:
            rc, rb = c.req("POST", f"/api/v1/jobs/{jid}/retry", expect=range(200, 599))
            check("retry -> 200", rc == 200, f"status {rc}: {str(rb)[:160]}")
            check("retry returns pending state",
                  isinstance(rb, dict) and rb.get("status") == "pending",
                  str(rb)[:160])
            gc2, got2 = c.req("GET", f"/api/v1/jobs/{jid}", expect=range(200, 599))
            check("job still readable after retry, status pending",
                  gc2 == 200 and isinstance(got2, dict) and got2.get("status") == "pending",
                  f"status {gc2}: {str(got2)[:120]}")

        section("validation")
        # Missing function_id AND function_name -> VALIDATION 400.
        bc, berr = c.req("POST", "/api/v1/jobs", {"payload": {"x": 1}}, expect=range(200, 599))
        check("enqueue without function ref -> 4xx", 400 <= bc < 500, f"status {bc}: {str(berr)[:120]}")
        # Unknown function id -> NOT_FOUND 404.
        nc, _ = c.req("POST", "/api/v1/jobs",
                      {"function_id": "00000000-0000-0000-0000-000000000000", "payload": {}},
                      expect=range(200, 599))
        check("enqueue unknown function -> 404", nc == 404, f"status {nc}")
        # Get a non-existent job -> 404.
        check("get missing job -> 404",
              c.status("GET", "/api/v1/jobs/00000000-0000-0000-0000-000000000000") == 404)

        section("delete")
        if jid:
            dc, db = c.req("DELETE", f"/api/v1/jobs/{jid}", expect=range(200, 599))
            check("delete -> 2xx", 200 <= dc < 300, f"status {dc}: {str(db)[:120]}")
            check("gone after delete (404)", c.status("GET", f"/api/v1/jobs/{jid}") == 404)
            check("not in list after delete",
                  not any(j.get("id") == jid for j in ((c.get("/api/v1/jobs") or {}).get("jobs") or [])))
            jid = None
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
