#!/usr/bin/env python3
"""Full sandboxed loop: create node fn -> deploy-inline -> wait active -> invoke -> executions.

Needs nsjail/sandbox. If the build never reaches active (no nested sandboxing in
this env), the whole module skips. REST resolves functions by UUID only, so the
id is captured from the create response.
"""
import json
import os
import sys
import time

from harness import OrvaClient, latest_execution_stderr, section, check, summary, skip

NAME = "e2e-deploy-invoke"
PYTHON_NAME = "e2e-deploy-invoke-python"
REQUIRE_SANDBOX = os.environ.get("ORVA_REQUIRE_SANDBOX", "") in ("1", "true", "yes")


def sandbox_unavailable(reason):
    """Skip on API-only environments, but fail the mandatory engine gate."""
    if REQUIRE_SANDBOX:
        check("sandbox invocation is available", False, reason)
        return summary()
    return skip(reason)

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
    lst = c.get("/api/v1/functions?limit=10000") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") in (NAME, PYTHON_NAME):
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
        section("create node function")
        body = {"name": NAME, "description": "deploy+invoke e2e", "runtime": "node",
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
            return sandbox_unavailable(f"sandbox/nsjail not available ({detail})")
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
            stderr = latest_execution_stderr(c, function_id=fid)
            diagnostic = f"; stderr={stderr[:700]!r}" if stderr else "; stderr unavailable"
            return sandbox_unavailable(
                f"sandbox/invoke unavailable here ({ecode or icode}: {str(err)[:240]}); "
                f"create+deploy+build verified{diagnostic}"
            )
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
                successful = [e for e in execs if e.get("status") == "success"]
                if len(successful) >= 2:
                    recorded = True
                    break
            time.sleep(0.5)
        check("both invokes recorded successfully", recorded, f"got {len(execs)} rows")
        if execs:
            check("execution belongs to this function",
                  all(e.get("function_id") == fid for e in execs))
            check("at least two successful executions",
                  sum(e.get("status") == "success" for e in execs) >= 2,
                  str([e.get("status") for e in execs])[:160])

        section("python deploy + real sandbox invoke")
        py_body = {
            "name": PYTHON_NAME, "description": "python engine e2e", "runtime": "python",
            "entrypoint": "handler.py", "timeout_ms": 30000, "memory_mb": 128,
            "cpus": 1, "network_mode": "none", "auth_mode": "none",
        }
        pyc, py_created = c.req("POST", "/api/v1/functions", py_body, expect=range(200, 599))
        check("create python -> 2xx", 200 <= pyc < 300, f"status {pyc}: {str(py_created)[:160]}")
        pyfid = (py_created or {}).get("id") if isinstance(py_created, dict) else None
        check("python function has id", bool(pyfid))
        if pyfid:
            py_code = """import json
def handler(event):
    body = event.get("body") or {}
    if isinstance(body, str):
        body = json.loads(body) if body else {}
    return {"statusCode": 200, "headers": {"Content-Type": "application/json"},
            "body": json.dumps({"runtime": "python", "echo": body.get("name")})}
"""
            pdc, pdep = c.req(
                "POST", f"/api/v1/functions/{pyfid}/deploy-inline",
                {"code": py_code, "filename": "handler.py"}, expect=range(200, 599),
            )
            check("python deploy accepted", pdc in (200, 202), f"status {pdc}: {str(pdep)[:160]}")
            py_status = wait_active(c, pyfid, timeout=60)
            check("python status == active", py_status == "active", str(py_status))
            if py_status == "active":
                pic, pib = c.req("POST", f"/fn/{pyfid}", {"name": "Orva"}, expect=range(200, 599))
                check("python invoke -> 200", pic == 200, f"status {pic}: {str(pib)[:200]}")
                if isinstance(pib, str):
                    try:
                        pib = json.loads(pib)
                    except Exception:
                        pib = {}
                check("python handler executed",
                      isinstance(pib, dict) and pib == {"runtime": "python", "echo": "Orva"},
                      str(pib)[:200])
            pdel, _ = c.req("DELETE", f"/api/v1/functions/{pyfid}", expect=range(200, 599))
            check("delete python -> 2xx", pdel in (200, 204), f"status {pdel}")

        # ── dependency installs run inside the build jail ──────────────
        #
        # This is the ONLY coverage anywhere for the build-jail seccomp
        # profile. Nothing else in CI deploys a function with dependencies
        # against a build of this branch, so without these cases a syscall
        # name that Kafel's catalog does not know would ship undetected —
        # and on aarch64 that is a policy COMPILE error, i.e. every
        # dependency build on that architecture fails, not degrades.
        #
        # Deliberately hard checks, not skips: by this point the plain
        # deploy above already went active, so the sandbox demonstrably
        # works and a failure here is the build jail, not the environment.
        section("build jail: node dependency install")
        dep_fid = None
        try:
            dcode, dfn = c.req("POST", "/api/v1/functions",
                               {"name": f"{NAME}-deps", "runtime": "node", "memory_mb": 256},
                               expect=range(200, 599))
            dep_fid = (dfn or {}).get("id") if isinstance(dfn, dict) else None
            check("deps function created", dcode in (200, 201) and bool(dep_fid), f"status {dcode}")
            if dep_fid:
                # semver installs a node_modules/.bin symlink, which is the
                # operation glibc routes through symlinkat on aarch64.
                ddc, ddep = c.req(
                    "POST", f"/api/v1/functions/{dep_fid}/deploy-inline",
                    {"code": ('const s = require("semver");\n'
                              'exports.handler = async () => ({statusCode: 200,'
                              ' headers: {"Content-Type": "application/json"},'
                              ' body: JSON.stringify({valid: s.valid("1.2.3")})});\n'),
                     "filename": "handler.js",
                     "dependencies": '{"name":"e2e-deps","version":"1.0.0",'
                                     '"dependencies":{"semver":"7.6.3"}}'},
                    expect=range(200, 599))
                check("deps deploy accepted", ddc in (200, 202), f"status {ddc}: {str(ddep)[:200]}")
                dstat = wait_active(c, dep_fid, timeout=180)
                check("jailed npm install succeeded", dstat == "active",
                      f"final status={dstat!r} — a Kafel/seccomp failure in the build jail "
                      f"surfaces here as a failed build")
                if dstat == "active":
                    dic, dib = c.req("POST", f"/fn/{dep_fid}", {}, expect=range(200, 599))
                    if isinstance(dib, str):
                        try:
                            dib = json.loads(dib)
                        except Exception:
                            dib = {}
                    check("installed dependency is loadable at invoke",
                          dic == 200 and isinstance(dib, dict) and dib.get("valid") == "1.2.3",
                          f"status {dic}: {str(dib)[:200]}")
        finally:
            if dep_fid:
                c.req("DELETE", f"/api/v1/functions/{dep_fid}", expect=range(200, 599))

        section("build jail: python dependency install")
        pdep_fid = None
        try:
            pcode2, pfn2 = c.req("POST", "/api/v1/functions",
                                 {"name": f"{PYTHON_NAME}-deps", "runtime": "python", "memory_mb": 256},
                                 expect=range(200, 599))
            pdep_fid = (pfn2 or {}).get("id") if isinstance(pfn2, dict) else None
            check("python deps function created", pcode2 in (200, 201) and bool(pdep_fid), f"status {pcode2}")
            if pdep_fid:
                # urllib3 ships a py3-none-any wheel, so this asserts the build
                # jail rather than wheel-platform resolution.
                pdc2, pdep2 = c.req(
                    "POST", f"/api/v1/functions/{pdep_fid}/deploy-inline",
                    {"code": ('import json, urllib3\n'
                              'def handler(event, context):\n'
                              '    return {"statusCode": 200,'
                              ' "headers": {"Content-Type": "application/json"},'
                              ' "body": json.dumps({"v": urllib3.__version__})}\n'),
                     "filename": "handler.py",
                     "dependencies": "urllib3==2.2.3\n"},
                    expect=range(200, 599))
                check("python deps deploy accepted", pdc2 in (200, 202), f"status {pdc2}: {str(pdep2)[:200]}")
                pstat2 = wait_active(c, pdep_fid, timeout=180)
                check("jailed pip install succeeded", pstat2 == "active",
                      f"final status={pstat2!r} — pip needs listxattr/utimensat/fsync "
                      f"from the build profile; EPERM on any of them fails the build")
                if pstat2 == "active":
                    pic2, pib2 = c.req("POST", f"/fn/{pdep_fid}", {}, expect=range(200, 599))
                    if isinstance(pib2, str):
                        try:
                            pib2 = json.loads(pib2)
                        except Exception:
                            pib2 = {}
                    check("installed python dependency is importable at invoke",
                          pic2 == 200 and isinstance(pib2, dict) and pib2.get("v") == "2.2.3",
                          f"status {pic2}: {str(pib2)[:200]}")
        finally:
            if pdep_fid:
                c.req("DELETE", f"/api/v1/functions/{pdep_fid}", expect=range(200, 599))

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
