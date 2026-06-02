#!/usr/bin/env python3
"""Saved request fixtures: create function → save fixture → list/get → upsert →
validation/conflict errors → delete → confirm gone. No sandbox needed (REST only).

Fixture routes (router.go):
  GET/POST  /api/v1/functions/{fn_id}/fixtures
  GET/PUT/DELETE /api/v1/functions/{fn_id}/fixtures/{name}
Create -> 201 fixtureView; List -> {"fixtures":[...]}; Delete -> 204 (idempotent);
dup Create -> 409; bad input -> 400; Get on missing -> 404.
"""
import sys

from harness import OrvaClient, section, check, summary

FN_NAME = "e2e-fixt-fn"
FIX_NAME = "e2e-fixt-hello"


def cleanup(c):
    lst = c.get("/api/v1/functions") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") == FN_NAME:
            # Deleting the function cascades its fixtures; belt-and-suspenders
            # delete the fixture by name first (idempotent 204) in case the
            # function delete is partial on a fresh instance.
            c.req("DELETE", f"/api/v1/functions/{f['id']}/fixtures/{FIX_NAME}",
                  expect=range(200, 599))
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
        fnbody = {"name": FN_NAME, "description": "fixture host", "runtime": "node24",
                  "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                  "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        code, created = c.req("POST", "/api/v1/functions", fnbody, expect=range(200, 599))
        check("function create -> 2xx", 200 <= code < 300, f"status {code}: {str(created)[:160]}")
        fid = (created or {}).get("id") if isinstance(created, dict) else None
        check("function has id", bool(fid))
        if not fid:
            return summary()

        base = f"/api/v1/functions/{fid}/fixtures"

        section("empty list")
        lc, empty = c.req("GET", base, expect=range(200, 599))
        check("list empty -> 200", lc == 200, f"status {lc}")
        check("fixtures is empty collection",
              isinstance(empty, dict) and len(empty.get("fixtures") or []) == 0,
              str(empty)[:160])

        section("create fixture")
        fixbody = {"name": FIX_NAME, "method": "post", "path": "echo",
                   "headers": {"X-Test": "e2e", "Content-Type": "application/json"},
                   "body": '{"hello":"world"}'}
        cc, fx = c.req("POST", base, fixbody, expect=range(200, 599))
        check("create fixture -> 201", cc == 201, f"status {cc}: {str(fx)[:160]}")
        check("fixture has id", isinstance(fx, dict) and bool(fx.get("id")))
        check("fixture name echoed", isinstance(fx, dict) and fx.get("name") == FIX_NAME)
        # validateAndNormalise uppercases method and prefixes path with "/".
        check("method normalised to upper", isinstance(fx, dict) and fx.get("method") == "POST",
              str(fx)[:160])
        check("path normalised with leading slash", isinstance(fx, dict) and fx.get("path") == "/echo",
              str(fx)[:160])
        check("headers round-trip", isinstance(fx, dict) and (fx.get("headers") or {}).get("X-Test") == "e2e")
        check("body round-trip", isinstance(fx, dict) and fx.get("body") == '{"hello":"world"}')
        check("function_id matches", isinstance(fx, dict) and fx.get("function_id") == fid)

        section("list + get by name")
        lc2, lst = c.req("GET", base, expect=range(200, 599))
        check("list -> 200", lc2 == 200, f"status {lc2}")
        names = [x.get("name") for x in ((lst or {}).get("fixtures") or [])]
        check("fixture appears in list", FIX_NAME in names, str(names)[:160])
        gc, got = c.req("GET", f"{base}/{FIX_NAME}", expect=range(200, 599))
        check("get by name -> 200", gc == 200, f"status {gc}")
        check("get returns same fixture", isinstance(got, dict) and got.get("name") == FIX_NAME
              and got.get("method") == "POST")

        section("conflict on duplicate create")
        dupc, dup = c.req("POST", base, fixbody, expect=range(200, 599))
        check("duplicate create -> 409", dupc == 409, f"status {dupc}: {str(dup)[:160]}")

        section("upsert (PUT) is idempotent + URL name wins")
        # Body name is advisory; the {name} path segment is authoritative.
        putbody = {"name": "ignored-name", "method": "get", "path": "/v2",
                   "headers": {"X-Test": "updated"}, "body": ""}
        pc, upd = c.req("PUT", f"{base}/{FIX_NAME}", putbody, expect=range(200, 599))
        check("upsert -> 200", pc == 200, f"status {pc}: {str(upd)[:160]}")
        check("upsert keeps URL name (body name ignored)",
              isinstance(upd, dict) and upd.get("name") == FIX_NAME, str(upd)[:160])
        check("upsert updated method", isinstance(upd, dict) and upd.get("method") == "GET")
        check("upsert updated path", isinstance(upd, dict) and upd.get("path") == "/v2")

        section("validation errors")
        nonamec, _ = c.req("POST", base, {"method": "GET", "path": "/"}, expect=range(200, 599))
        check("missing name rejected (400)", nonamec == 400, f"status {nonamec}")
        badmc, _ = c.req("POST", base, {"name": "e2e-fixt-badmethod", "method": "TELEPORT"},
                         expect=range(200, 599))
        check("bad method rejected (400)", badmc == 400, f"status {badmc}")
        # Fixtures on a non-existent function id -> 404.
        nfc = c.status("GET", "/api/v1/functions/00000000-0000-0000-0000-000000000000/fixtures")
        check("fixtures of missing function -> 404", nfc == 404, f"status {nfc}")
        # Getting an unknown fixture name on a real function -> 404.
        gnf = c.status("GET", f"{base}/e2e-fixt-nope")
        check("get unknown fixture -> 404", gnf == 404, f"status {gnf}")

        section("delete + confirm gone")
        dc, _ = c.req("DELETE", f"{base}/{FIX_NAME}", expect=range(200, 599))
        check("delete -> 204", dc == 204, f"status {dc}")
        check("gone after delete (get -> 404)", c.status("GET", f"{base}/{FIX_NAME}") == 404)
        lc3, after = c.req("GET", base, expect=range(200, 599))
        check("not in list after delete",
              FIX_NAME not in [x.get("name") for x in ((after or {}).get("fixtures") or [])])
        # Delete is idempotent — second delete still 204.
        dc2, _ = c.req("DELETE", f"{base}/{FIX_NAME}", expect=range(200, 599))
        check("delete is idempotent (204 again)", dc2 == 204, f"status {dc2}")
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
