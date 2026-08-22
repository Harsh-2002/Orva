#!/usr/bin/env python3
"""End-to-end proof for the TypeScript entrypoint split and the rollback model.

Runs against a real Orva container with a working nsjail sandbox, so every
assertion here is about what the sandbox actually executed -- not about what a
database row says. That distinction is the whole point: the defects this covers
were invisible to unit tests because they only surfaced when a worker tried to
run the file it had been handed.
"""
import json, subprocess, sys, time, urllib.request, urllib.error

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18600"
KEY = sys.argv[2]
CONTAINER = sys.argv[3] if len(sys.argv) > 3 else "orva-e2e"

results = []


def check(name, ok, detail=""):
    results.append((name, ok, detail))
    print(("  PASS  " if ok else "  FAIL  ") + name + (("\n          " + str(detail)) if detail and not ok else ""))
    return ok


def api(method, path, body=None, raw=False):
    url = BASE + path
    data = None
    headers = {"X-Orva-API-Key": KEY}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            payload = r.read()
            return r.status, (payload.decode() if raw else json.loads(payload or b"{}"))
    except urllib.error.HTTPError as e:
        payload = e.read()
        try:
            return e.code, json.loads(payload or b"{}")
        except Exception:
            return e.code, payload.decode()


def invoke(fn_id, body):
    url = BASE + "/fn/" + fn_id
    req = urllib.request.Request(url, data=json.dumps(body).encode(),
                                 headers={"X-Orva-API-Key": KEY, "Content-Type": "application/json"},
                                 method="POST")
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def wait_build(fn_id, want_version=None, timeout=180):
    """Block until the newest deployment reaches a terminal status."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        _, d = api("GET", f"/api/v1/functions/{fn_id}/deployments")
        deps = d.get("deployments") or []
        if deps:
            newest = deps[0]
            if newest.get("status") in ("succeeded", "failed"):
                return newest
        time.sleep(1)
    return None


DB = "/var/lib/orva/orva.db"


def _in_container(script):
    """Run a Python snippet inside the container.

    The image ships no sqlite3 CLI, but it does ship python3, whose stdlib has
    the driver. Going through the container (rather than copying the file out)
    keeps every read against the database the server is actually using.
    """
    out = subprocess.run(
        ["sudo", "-n", "docker", "exec", CONTAINER, "python3", "-c", script],
        capture_output=True, text=True)
    if out.returncode != 0:
        return "ERR:" + out.stderr.strip()
    return out.stdout.strip()


def sql(query):
    """Read one scalar from the container's database."""
    script = (
        "import sqlite3\n"
        f"c=sqlite3.connect({DB!r})\n"
        f"r=c.execute({query!r}).fetchone()\n"
        "print('' if r is None or r[0] is None else r[0])"
    )
    return _in_container(script)


def sql_exec(stmt, args=()):
    """Apply a write to the container's database."""
    script = (
        "import sqlite3\n"
        f"c=sqlite3.connect({DB!r})\n"
        f"c.execute({stmt!r}, {tuple(args)!r})\n"
        "c.commit()\nprint('ok')"
    )
    return _in_container(script)


TS_SOURCE = """interface Payload { name?: string }

export async function handler(event: any) {
  const body: Payload = event.body ? JSON.parse(event.body) : {}
  return { statusCode: 200, body: JSON.stringify({ hello: body.name ?? 'world', typed: true, rev: REV }) }
}
"""
TSCONFIG = '{"compilerOptions":{"target":"ES2022","module":"CommonJS","outDir":"dist"}}'
PKG = '{"name":"ts-e2e","private":true,"devDependencies":{"typescript":"^5.4"}}'


def deploy_ts(fn_id, rev):
    code = TS_SOURCE.replace("REV", str(rev))
    return api("POST", f"/api/v1/functions/{fn_id}/deploy-inline", {
        "code": code,
        "filename": "handler.ts",
        "dependencies": PKG,
        "extras": {"tsconfig.json": TSCONFIG},
    })


print("\n=== TypeScript entrypoint split, end to end ===\n")

st, fn = api("POST", "/api/v1/functions", {
    "name": "ts-e2e", "runtime": "node", "entrypoint": "handler.ts",
    "memory_mb": 256, "timeout_ms": 60000,
})
if st not in (200, 201):
    print("could not create function:", st, fn)
    sys.exit(2)
FN = fn["id"] if "id" in fn else fn["function"]["id"]
print(f"function {FN}\n")

# --- v1 -------------------------------------------------------------------
st, _ = deploy_ts(FN, 1)
d1 = wait_build(FN)
check("first deploy of a TypeScript function succeeds", d1 and d1["status"] == "succeeded", d1)
check("the first deploy is v1, not v2",
      d1 and d1["version"] == 1,
      f"version={d1 and d1.get('version')} -- functions.version is a mutation counter, "
      f"deployment numbers must come from the deployment sequence")

row = sql("select entrypoint||'|'||run_entrypoint from functions where name='ts-e2e'")
check("entrypoint stays the authored file; run_entrypoint holds the build output",
      row == "handler.ts|dist/handler.js", f"row={row!r}")

st, src = api("GET", f"/api/v1/functions/{FN}/source")
first = (src.get("code") or "").splitlines()[0] if isinstance(src, dict) else ""
check("GET /source returns TypeScript as authored, not compiled JavaScript",
      first.startswith("interface Payload"), f"first line={first!r}")

st, body = invoke(FN, {"name": "v1"})
check("v1 invokes: the sandbox ran dist/handler.js", st == 200 and '"typed":true' in body, f"{st} {body}")
check("v1 is the revision serving", '"rev":1' in body, body)

# --- v2, proving re-deploy validates the authored .ts ----------------------
st, _ = deploy_ts(FN, 2)
d2 = wait_build(FN)
check("re-deploying a TypeScript function succeeds "
      "(the validator checks handler.ts, not the compiled path)",
      d2 and d2["status"] == "succeeded", d2)
check("the second deploy is v2 -- versions are gapless", d2 and d2["version"] == 2, d2 and d2.get("version"))

st, body = invoke(FN, {"name": "v2"})
check("v2 invokes and serves the new revision", st == 200 and '"rev":2' in body, f"{st} {body}")

# --- rollback: promote, do not append ------------------------------------
_, before = api("GET", f"/api/v1/functions/{FN}/deployments")
n_before = len(before.get("deployments") or [])
d1_id = [d for d in before["deployments"] if d["version"] == 1][0]["id"]

st, r = api("POST", f"/api/v1/functions/{FN}/rollback", {"deployment_id": d1_id})
check("rollback to v1 is accepted", st == 200, f"{st} {r}")

_, after = api("GET", f"/api/v1/functions/{FN}/deployments")
n_after = len(after.get("deployments") or [])
check("rollback creates no new version -- it promotes an existing one",
      n_after == n_before, f"{n_before} deployments before, {n_after} after")

active = sql("select active_deployment_id from functions where name='ts-e2e'")
check("the live deployment is now v1", active == d1_id, f"active={active!r} want {d1_id!r}")

st, body = invoke(FN, {"name": "back"})
check("the rolled-back version actually runs", st == 200, f"{st} {body}")
check("and serves v1's revision, not v2's", '"rev":1' in body, body)

# --- the legacy-snapshot defect ------------------------------------------
# A deployment recorded before run_entrypoint existed carries no value for it.
# Rollback used to apply that absence verbatim, pointing a compiled TypeScript
# version back at its .ts source -- which Node cannot execute. Strip the field
# from v2's stored snapshot to reproduce exactly that row, then roll onto it.
print("\n--- legacy snapshot (written before run_entrypoint existed) ---\n")
d2_id = [d for d in after["deployments"] if d["version"] == 2][0]["id"]
snap = sql(f"select snapshot from deployments where id='{d2_id}'")
try:
    parsed = json.loads(snap)
    parsed.pop("run_entrypoint", None)
    sql_exec("update deployments set snapshot=? where id=?", (json.dumps(parsed), d2_id))
    check("v2's snapshot now has no run_entrypoint (legacy row reproduced)",
          "run_entrypoint" not in sql(f"select snapshot from deployments where id='{d2_id}'"))
except Exception as e:
    check("could reproduce a legacy snapshot", False, e)

st, r = api("POST", f"/api/v1/functions/{FN}/rollback", {"deployment_id": d2_id})
check("rollback onto the legacy row is accepted", st == 200, f"{st} {r}")

run_ep = sql("select run_entrypoint from functions where name='ts-e2e'")
check("run_entrypoint is derived from the version on disk, not from the silent snapshot",
      run_ep == "dist/handler.js",
      f"run_entrypoint={run_ep!r} -- empty would hand Node a TypeScript file")

st, body = invoke(FN, {"name": "legacy"})
check("THE DEFECT: invoking after rolling back onto a legacy snapshot returns 200, not WORKER_CRASHED",
      st == 200, f"{st} {body}")
check("and serves v2's revision", '"rev":2' in body, body)

# --- no-op guard ----------------------------------------------------------
st, r = api("POST", f"/api/v1/functions/{FN}/rollback", {"deployment_id": d2_id})
check("rolling back to the version already live is refused as a no-op",
      st in (400, 409), f"{st} {r}")

print("\n=== summary ===")
failed = [n for n, ok, _ in results if not ok]
print(f"{len(results) - len(failed)}/{len(results)} passed")
for n in failed:
    print("  FAILED:", n)
sys.exit(1 if failed else 0)
