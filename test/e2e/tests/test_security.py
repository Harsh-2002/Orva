#!/usr/bin/env python3
"""Security regressions: the X-Orva-Internal-Token auth bypass (H1) and the
deploy body-size-cap exemption (M4).

H1: authMiddleware used to skip the API-key gate for ANY non-empty
X-Orva-Internal-Token header, so a request with a garbage value reached the
handlers with full operator power. It must now be rejected (401) — only the
correct function-scoped, process-signed credential, which a test can't know,
is accepted.

Mostly read-only; the M4 section creates one function and cleans it up."""
import sys
import urllib.error
import urllib.request

from harness import OrvaClient, section, check, summary


def probe(base, path, headers):
    """GET path with exactly the given headers; return the status code."""
    r = urllib.request.Request(base + path, method="GET", headers=headers)
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            return resp.status
    except urllib.error.HTTPError as e:
        return e.code


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2

    created_id = None
    try:
        section("internal-token auth bypass is closed (H1)")
        # The exact endpoints the live exploit hit.
        for path in ("/api/v1/functions", "/api/v1/keys", "/api/v1/executions"):
            code = probe(c.base, path, {"X-Orva-Internal-Token": "totally-bogus-value"})
            check(f"bogus internal token on {path} -> 401", code == 401, f"status {code}")

        # An empty header must also not bypass.
        code = probe(c.base, "/api/v1/functions", {"X-Orva-Internal-Token": ""})
        check("empty internal token -> 401", code == 401, f"status {code}")

        # No auth at all -> 401 (baseline the bypass must match).
        code = probe(c.base, "/api/v1/functions", {})
        check("no auth -> 401", code == 401, f"status {code}")

        # The legitimate API key still works (fix didn't break normal auth).
        code = probe(c.base, "/api/v1/functions", {"X-Orva-API-Key": c.key})
        check("valid API key -> 200", code == 200, f"status {code}")

        section("deploy accepts payloads over the 6MB JSON body cap (M4)")
        fn = c.post("/api/v1/functions", {"name": "e2e-sec-bigdeploy", "runtime": "node"})
        created_id = fn.get("id") if isinstance(fn, dict) else None
        check("created function for big-deploy probe", bool(created_id), str(fn)[:160])
        if created_id:
            # ~7MB of source — comfortably over the 6MB default JSON body cap.
            big_code = "// pad\n" + ("x" * (7 * 1024 * 1024)) + "\nmodule.exports=async()=>({statusCode:200,body:'ok'})\n"
            code = c.status("POST", f"/api/v1/functions/{created_id}/deploy-inline",
                            {"code": big_code, "filename": "handler.js"})
            # The point is the body-cap exemption: it must NOT be 413. A queued
            # build (202) or any non-413 handler response proves the reader was
            # not capped at 6MB. (The build itself may skip without a sandbox.)
            check("7MB deploy-inline is not 413 PAYLOAD_TOO_LARGE", code != 413, f"status {code}")
    finally:
        if created_id:
            try:
                c.delete(f"/api/v1/functions/{created_id}")
            except Exception:
                pass
    return summary()


if __name__ == "__main__":
    sys.exit(main())
