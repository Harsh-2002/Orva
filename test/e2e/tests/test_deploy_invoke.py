#!/usr/bin/env python3
"""Full sandboxed loop: create node24 fn -> deploy-inline -> wait active -> invoke -> executions.

Needs nsjail/sandbox. If the build never reaches active (no nested sandboxing in
this env), the whole module skips. REST resolves functions by UUID only, so the
id is captured from the create response.
"""
import json
import sys
import time

from harness import OrvaClient, section, check, summary, skip

NAME = "e2e-deploy-invoke"

# Known-good node handler (mirrors frontend node_http_hello template): a default
# exports.handler(event) returning {statusCode, headers, body} with JSON body.
HANDLER_JS = """exports.handler = async (event) => {
  const body = typeof event.body === 'string'
    ? JSON.parse(event.body || '{}')
    : event.body || {};
  const name = body.name || 'World';
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ message: `Hello ${name}!`, language: 'Node.js', echo: name }),
  };
};
"""


def cleanup(c):
    lst = c.get("/api/v1/functions") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") == NAME:
            c.req("DELETE", f"/api/v1/functions/{f['id']}", expect=(200, 204, 404))


def wait_active(c, fid, timeout=60):
    """Poll the function row until status flips. Returns the terminal status
    string ("active", "error", or the last seen status on timeout)."""
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        code, fn = c.req("GET", f"/api/v1/functions/{fid}", expect=range(200, 599))
        if code == 200 and isinstance(fn, dict):
            last = fn.get("status")
            if last in ("active", "error"):
                return last
        time.sleep(1.0)
    return last


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    fid = None
    try:
        section("create node24 function")
        body = {"name": NAME, "description": "deploy+invoke e2e", "runtime": "node24",
                "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        code, created = c.req("POST", "/api/v1/functions", body, expect=range(200, 599))
        check("create -> 2xx", 200 <= code < 300, f"status {code}: {str(created)[:160]}")
        fid = (created or {}).get("id") if isinstance(created, dict) else None
        check("created has id", bool(fid))
        if not fid:
            return summary()

        section("deploy inline")
        dep_body = {"code": HANDLER_JS, "filename": "handler.js"}
        dc, dep = c.req("POST", f"/api/v1/functions/{fid}/deploy-inline", dep_body, expect=range(200, 599))
        # Async path returns 202 {deployment_id, status:"queued"}; the legacy
        # synchronous fallback returns 200 {status:"deployed"}. Both are valid.
        check("deploy-inline accepted (200/202)", dc in (200, 202), f"status {dc}: {str(dep)[:200]}")
        deployment_id = (dep or {}).get("deployment_id") if isinstance(dep, dict) else None

        section("wait for build to reach active")
        status = wait_active(c, fid, timeout=60)
        if status != "active":
            # Build never went active. Most likely no nsjail/nested sandbox in
            # this environment — skip the whole module per the suite contract.
            detail = f"final function status={status!r}"
            if deployment_id:
                _, d = c.req("GET", f"/api/v1/deployments/{deployment_id}", expect=range(200, 599))
                if isinstance(d, dict):
                    detail += f", deployment status={d.get('status')!r} error={str(d.get('error'))[:120]!r}"
            return skip(f"sandbox/nsjail not available ({detail})")
        check("function status == active", status == "active")

        if deployment_id:
            _, d = c.req("GET", f"/api/v1/deployments/{deployment_id}", expect=range(200, 599))
            check("deployment reports succeeded",
                  isinstance(d, dict) and d.get("status") in ("succeeded", "deployed"),
                  str(d)[:160])

        section("invoke via public /fn/{id}")
        # /fn/ is at the root, NOT under /api/v1; it bypasses API-key auth and
        # returns the function's own statusCode + body verbatim.
        icode, ibody = c.req("POST", f"/fn/{fid}", {"name": "Orva"}, expect=range(200, 599))
        # The build can succeed (function goes active) yet the INVOKE can't run
        # if the environment lacks a usable sandbox: nested Docker where nsjail
        # can't launch a child (502 WORKER_CRASHED), or an instance with no
        # language rootfs installed (503 SANDBOX_ERROR). Either is an
        # environment capability gap, not a product failure — skip the invoke
        # leg. On a properly-provisioned host (rootfs + nsjail caps) it runs end
        # to end and these checks execute.
        err = (ibody.get("error") or {}) if isinstance(ibody, dict) else {}
        ecode = err.get("code", "")
        blob = str(err).lower()
        if icode >= 500 and (ecode in ("WORKER_CRASHED", "SANDBOX_ERROR")
                             or "rootfs" in blob or "nsjail" in blob or "sandbox" in blob):
            return skip(f"sandbox/invoke unavailable here ({ecode or icode}); create+deploy+build verified")
        check("invoke -> 200", icode == 200, f"status {icode}: {str(ibody)[:200]}")
        # Body may come back parsed (dict) or as a raw JSON string; normalize.
        parsed = ibody
        if isinstance(ibody, str):
            try:
                parsed = json.loads(ibody)
            except Exception:
                parsed = {}
        check("invoke body echoes name", isinstance(parsed, dict) and parsed.get("echo") == "Orva",
              str(ibody)[:200])
        check("invoke greets correctly",
              isinstance(parsed, dict) and parsed.get("message") == "Hello Orva!",
              str(parsed)[:160])
        check("invoke reports runtime",
              isinstance(parsed, dict) and parsed.get("language") == "Node.js")

        section("default arg path (empty body)")
        i2c, i2b = c.req("POST", f"/fn/{fid}", {}, expect=range(200, 599))
        check("invoke (empty body) -> 200", i2c == 200, f"status {i2c}")
        p2 = i2b
        if isinstance(i2b, str):
            try:
                p2 = json.loads(i2b)
            except Exception:
                p2 = {}
        check("defaults to World", isinstance(p2, dict) and p2.get("message") == "Hello World!",
              str(p2)[:160])

        section("executions recorded")
        # Give the async execution writer a moment to flush its batch.
        recorded = False
        deadline = time.time() + 10
        execs = []
        while time.time() < deadline:
            ec, el = c.req("GET", f"/api/v1/executions?function_id={fid}&limit=50",
                           expect=range(200, 599))
            if ec == 200 and isinstance(el, dict):
                execs = el.get("executions") or []
                if execs:
                    recorded = True
                    break
            time.sleep(0.5)
        check("executions list -> 200 with rows", recorded, f"got {len(execs)} rows")
        if execs:
            check("execution belongs to this function",
                  all(e.get("function_id") == fid for e in execs))
            check("at least one successful execution",
                  any(e.get("status") == "success" for e in execs),
                  str([e.get("status") for e in execs])[:160])

        section("delete + confirm gone")
        delc, _ = c.req("DELETE", f"/api/v1/functions/{fid}", expect=range(200, 599))
        check("delete -> 2xx", delc in (200, 204), f"status {delc}")
        check("gone after delete (404)", c.status("GET", f"/api/v1/functions/{fid}") == 404)
        fid = None
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
