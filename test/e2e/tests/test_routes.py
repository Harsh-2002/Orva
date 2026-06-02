#!/usr/bin/env python3
"""Custom routes via REST: map a path -> function, list, validate, delete.

Routes live at /api/v1/routes:
  GET    /api/v1/routes                  -> {"routes": [{path, function_id, methods, created_at}, ...]}
  POST   /api/v1/routes  {path, function_id, methods}  (upsert) -> 201 {status:"saved", path, function_id}
  DELETE /api/v1/routes?path=<path>      -> 200 {status:"deleted", path}

Routes are keyed by `path` (there is no route id). Needs a real function (by UUID)
to map to — we create one purely as a mapping target (no deploy/invoke, so no
sandbox needed).
"""
import sys

from harness import OrvaClient, section, check, summary

FN_NAME = "e2e-route-fn"
ROUTE_PATH = "/e2e-route-xyz"
ROUTE_PREFIX = "/e2e-route-prefix/*"


def _routes(c):
    lst = c.get("/api/v1/routes") or {}
    return lst.get("routes") or []


def cleanup(c):
    # Remove any routes we may have created.
    for p in (ROUTE_PATH, ROUTE_PREFIX):
        c.req("DELETE", f"/api/v1/routes?path={p}", expect=range(200, 599))
    # Remove the mapping-target function (resolve id by name).
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
        section("setup: create mapping-target function")
        body = {"name": FN_NAME, "description": "route target", "runtime": "node24",
                "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        code, created = c.req("POST", "/api/v1/functions", body, expect=range(200, 599))
        check("function create -> 2xx", 200 <= code < 300, f"status {code}: {str(created)[:160]}")
        fid = (created or {}).get("id") if isinstance(created, dict) else None
        check("function has id", bool(fid))
        if not fid:
            return summary()

        section("create route (upsert)")
        rc, rbody = c.req("POST", "/api/v1/routes",
                          {"path": ROUTE_PATH, "function_id": fid, "methods": "GET,POST"},
                          expect=range(200, 599))
        check("upsert route -> 201", rc == 201, f"status {rc}: {str(rbody)[:160]}")
        check("upsert echoes status saved", isinstance(rbody, dict) and rbody.get("status") == "saved",
              str(rbody)[:160])
        check("upsert echoes path", isinstance(rbody, dict) and rbody.get("path") == ROUTE_PATH)

        section("list shows route")
        rows = _routes(c)
        mine = next((r for r in rows if r.get("path") == ROUTE_PATH), None)
        check("route appears in list", mine is not None)
        check("route maps to our function", isinstance(mine, dict) and mine.get("function_id") == fid,
              str(mine)[:160])
        check("route preserves methods", isinstance(mine, dict) and mine.get("methods") == "GET,POST",
              str(mine)[:160])

        section("upsert is idempotent (update methods)")
        uc, _ = c.req("POST", "/api/v1/routes",
                      {"path": ROUTE_PATH, "function_id": fid, "methods": "PUT"},
                      expect=range(200, 599))
        check("re-upsert same path -> 201", uc == 201, f"status {uc}")
        mine2 = next((r for r in _routes(c) if r.get("path") == ROUTE_PATH), None)
        check("methods updated, not duplicated",
              isinstance(mine2, dict) and mine2.get("methods") == "PUT", str(mine2)[:160])
        check("path not duplicated in list",
              sum(1 for r in _routes(c) if r.get("path") == ROUTE_PATH) == 1)

        section("prefix wildcard route")
        pc, _ = c.req("POST", "/api/v1/routes",
                      {"path": ROUTE_PREFIX, "function_id": fid}, expect=range(200, 599))
        check("wildcard prefix route -> 201", pc == 201, f"status {pc}")
        check("wildcard route appears",
              any(r.get("path") == ROUTE_PREFIX for r in _routes(c)))

        section("validation")
        # Path missing leading slash.
        bc1, _ = c.req("POST", "/api/v1/routes",
                       {"path": "no-leading-slash", "function_id": fid}, expect=range(200, 599))
        check("path without leading / rejected (4xx)", 400 <= bc1 < 500, f"status {bc1}")
        # Reserved prefix.
        bc2, _ = c.req("POST", "/api/v1/routes",
                       {"path": "/api/hijack", "function_id": fid}, expect=range(200, 599))
        check("reserved /api/ prefix rejected (4xx)", 400 <= bc2 < 500, f"status {bc2}")
        # Wildcard not at end.
        bc3, _ = c.req("POST", "/api/v1/routes",
                       {"path": "/bad/*/middle", "function_id": fid}, expect=range(200, 599))
        check("misplaced wildcard rejected (4xx)", 400 <= bc3 < 500, f"status {bc3}")
        # Missing function_id.
        bc4, _ = c.req("POST", "/api/v1/routes",
                       {"path": "/e2e-route-nofn"}, expect=range(200, 599))
        check("missing function_id rejected (4xx)", 400 <= bc4 < 500, f"status {bc4}")
        # Nonexistent function -> 404.
        bc5, _ = c.req("POST", "/api/v1/routes",
                       {"path": "/e2e-route-ghost", "function_id": "00000000-0000-0000-0000-000000000000"},
                       expect=range(200, 599))
        check("nonexistent function -> 404", bc5 == 404, f"status {bc5}")
        # Delete without path query param.
        dbc, _ = c.req("DELETE", "/api/v1/routes", expect=range(200, 599))
        check("delete without path param rejected (4xx)", 400 <= dbc < 500, f"status {dbc}")

        section("delete route")
        dc, dbody = c.req("DELETE", f"/api/v1/routes?path={ROUTE_PATH}", expect=range(200, 599))
        check("delete route -> 200", dc == 200, f"status {dc}")
        check("delete echoes status deleted",
              isinstance(dbody, dict) and dbody.get("status") == "deleted", str(dbody)[:160])
        check("route gone from list after delete",
              not any(r.get("path") == ROUTE_PATH for r in _routes(c)))

        section("delete wildcard route")
        wc, _ = c.req("DELETE", f"/api/v1/routes?path={ROUTE_PREFIX}", expect=range(200, 599))
        check("delete wildcard route -> 200", wc == 200, f"status {wc}")
        check("wildcard route gone from list",
              not any(r.get("path") == ROUTE_PREFIX for r in _routes(c)))
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
