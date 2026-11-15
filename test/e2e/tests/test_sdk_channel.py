#!/usr/bin/env python3
"""SDK channel: orva.kv from `network_mode: none` functions over the private,
network-free worker channel (no token, no HTTP, no egress).

Needs the sandbox. Scenarios (dynamic, against real nsjail workers):
module-scope + request-scope kv, every kv op, 200-way concurrency, cross-function
isolation, a call that outlives its request (stale lease refused), a worker killed
mid-call (timeout) and replaced, forked Python children, a subprocess, values at
the size limit, per-op metrics, and channel-only SDK access for egress.
"""
import ast
import json
import os
import sys
import time
import urllib.request

from harness import OrvaClient, section, check, summary, skip

NODE = "e2e-chan-node"
NODE2 = "e2e-chan-node-iso"
PY = "e2e-chan-py"
NODE_SLOW = "e2e-chan-slow"
NODE_EGRESS = "e2e-chan-egress"
ALL = (NODE, NODE2, PY, NODE_SLOW, NODE_EGRESS)
REQUIRE_SANDBOX = os.environ.get("ORVA_REQUIRE_SANDBOX", "") in ("1", "true", "yes")

NODE_CODE = r"""
const orva = require('orva');
// Module-scope SDK use: runs at spawn with no execution bound (documented pattern).
let moduleResult = null;
const moduleReady = (async () => {
  try { await orva.kv.put('module-scope', { n: 1 }); moduleResult = await orva.kv.get('module-scope'); }
  catch (e) { moduleResult = { error: String(e) }; }
})();
const ok = (o) => ({ statusCode: 200, body: JSON.stringify(o) });
exports.handler = async (event) => {
  await moduleReady;
  const q = JSON.parse(event.body || '{}');
  try {
    switch (q.op) {
      case 'basic': {
        await orva.kv.put('a', { x: 1 });
        const got = await orva.kv.get('a');
        await orva.kv.putMany([{ key: 'lst-1', value: 1 }, { key: 'lst-2', value: 2 }]);
        const many = await orva.kv.getMany(['lst-1', 'lst-2', 'nope']);
        const listed = await orva.kv.list({ prefix: 'lst-', limit: 10 });
        await orva.kv.delete('ctr');
        const inc = await orva.kv.incr('ctr', 5);
        await orva.kv.delete('cas');   // keep the flow repeatable
        const casOk = await orva.kv.cas('cas', null, 'v1');
        let casMismatch = null;
        try { await orva.kv.cas('cas', 'wrong', 'v2'); } catch (e) { casMismatch = e.name || String(e); }
        const delN = await orva.kv.deleteMany(['lst-1', 'lst-2']);
        await orva.kv.delete('a');
        const gone = await orva.kv.get('a', 'DEFAULT');
        return ok({ moduleResult, got, many, listed, inc, casOk, casMismatch, delN, gone });
      }
      case 'concurrent': {
        await orva.kv.delete('ctr2');
        const rs = await Promise.all(Array.from({ length: q.n }, () => orva.kv.incr('ctr2', 1)));
        return ok({ final: await orva.kv.get('ctr2'), distinct: new Set(rs).size });
      }
      case 'big': {
        try { await orva.kv.put('big', 'x'.repeat(q.size)); return ok({ stored: true, len: (await orva.kv.get('big')).length }); }
        catch (e) { return ok({ stored: false, status: e.status, message: String(e.message).slice(0, 120) }); }
      }
      case 'put': await orva.kv.put(q.key, q.value); return ok({ done: true });
      case 'get': return ok({ value: await orva.kv.get(q.key, null) });
      case 'late': {
        // Outlives the request: its lease is revoked when this dispatch ends; kv still works.
        setTimeout(() => { orva.kv.put('late', 'after-response').catch(() => {}); }, 400);
        return ok({ scheduled: true });
      }
      case 'env': return ok({ fd: process.env.ORVA_SDK_FD || null, proto: process.env.ORVA_SDK_PROTO || null });
      default: return { statusCode: 400, body: 'bad op' };
    }
  } catch (e) { return { statusCode: 500, body: JSON.stringify({ error: String(e), status: e.status }) }; }
};
"""

SLOW_CODE = r"""
const orva = require('orva');
exports.handler = async (event) => {
  const q = JSON.parse(event.body || '{}');
  if (q.op === 'spin') { for (;;) { await orva.kv.get('spin'); } }   // never returns: the timeout kills the worker
  await orva.kv.put('alive', Date.now());
  return { statusCode: 200, body: JSON.stringify({ alive: true, pid: process.pid }) };
};
"""

PY_CODE = r"""
import json, os, subprocess, sys, threading
import orva

def handler(event):
    q = json.loads(event.get("body") or "{}")
    op = q.get("op")
    out = {}
    if op == "basic":
        orva.kv.delete("py-cas"); orva.kv.delete("py-ctr")
        orva.kv.put("py-a", {"x": 1})
        out["got"] = orva.kv.get("py-a")
        out["inc"] = orva.kv.incr("py-ctr", 3)
        out["cas_ok"] = orva.kv.cas("py-cas", None, "v1")
        try:
            orva.kv.cas("py-cas", "wrong", "v2"); out["cas_mismatch"] = False
        except Exception as e:
            out["cas_mismatch"] = type(e).__name__
        orva.kv.put_many([{"key": "p1", "value": 1}, {"key": "p2", "value": 2}])
        out["many"] = orva.kv.get_many(["p1", "p2", "none"])
        out["deleted"] = orva.kv.delete_many(["p1", "p2"])
        orva.kv.delete("py-a")
        out["gone"] = orva.kv.get("py-a", "DEFAULT")
    elif op == "threads":
        orva.kv.delete("py-ctr2")
        def work():
            for _ in range(q["per"]):
                orva.kv.incr("py-ctr2", 1)
        ts = [threading.Thread(target=work) for _ in range(q["threads"])]
        [t.start() for t in ts]; [t.join() for t in ts]
        out["final"] = orva.kv.get("py-ctr2")
    elif op == "fork":
        r, w = os.pipe()
        pid = os.fork()
        if pid == 0:
            try:
                orva.kv.get("anything"); msg = "CHILD-USED-CHANNEL"
            except Exception as e:
                msg = "refused:" + type(e).__name__
            os.write(w, msg.encode()); os._exit(0)
        os.waitpid(pid, 0); os.close(w)
        out["child"] = os.read(r, 100).decode()
        out["parent_ok"] = orva.kv.get("anything", "d") == "d"
    elif op == "subproc":
        out["fds"] = subprocess.run([sys.executable, "-c", "import os;print(sorted(os.listdir('/proc/self/fd')))"],
                                    capture_output=True, text=True).stdout.strip()
    return {"statusCode": 200, "body": json.dumps(out)}
"""

EGRESS_CODE = r"""
const orva = require('orva');
exports.handler = async () => {
  await orva.kv.put('channel-kv', 1);
  const viaKv = await orva.kv.get('channel-kv');
  const res = await orva.invoke('__NODE__', { op: 'basic' });
  const exposed = Object.keys(process.env).filter(k =>
    ['ORVA_SDK_FD', 'ORVA_SDK_PROTO', 'ORVA_INTERNAL_TOKEN', 'ORVA_API_BASE'].includes(k.toUpperCase())).sort();
  return { statusCode: 200, body: JSON.stringify({ viaKv, f2f: res.statusCode, exposed,
    fd: process.env.ORVA_SDK_FD, proto: process.env.ORVA_SDK_PROTO }) };
};
""".replace("__NODE__", NODE)


def cleanup(c):
    lst = c.get("/api/v1/functions?limit=10000") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") in ALL:
            c.req("DELETE", f"/api/v1/functions/{f['id']}", expect=(200, 204, 404))


def deploy(c, name, runtime, code, fname, net="none", timeout_ms=30000):
    code_, fn = c.req("POST", "/api/v1/functions", {
        "name": name, "runtime": runtime, "entrypoint": fname, "memory_mb": 128, "cpus": 1,
        "timeout_ms": timeout_ms, "network_mode": net, "sdk": True,
        "auth_mode": "none"}, expect=range(200, 599))
    if code_ >= 300:
        return None
    fid = fn["id"]
    c.req("POST", f"/api/v1/functions/{fid}/deploy-inline", {"code": code, "filename": fname}, expect=range(200, 599))
    for _ in range(240):
        st = (c.get(f"/api/v1/functions/{fid}") or {}).get("status")
        if st == "active":
            return fid
        if st == "error":
            return None
        time.sleep(0.5)
    return None


def call(c, fid, body):
    code, out = c.req("POST", f"/fn/{fid}", body, expect=range(200, 599))
    if isinstance(out, str):
        try:
            out = json.loads(out)
        except ValueError:
            pass
    return code, out


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    try:
        section("deploy (network_mode: none)")
        fn = deploy(c, NODE, "node", NODE_CODE, "handler.js")
        if not fn:
            if REQUIRE_SANDBOX:
                check("sandbox available", False, "node function never became active")
                return summary()
            return skip("function never became active (no nested sandboxing here)")
        fn2 = deploy(c, NODE2, "node", NODE_CODE, "handler.js")
        py = deploy(c, PY, "python", PY_CODE, "handler.py")
        check("node + node2 + python functions active under network_mode none", bool(fn and fn2 and py))

        section("no network, no token needed: every kv op over the channel (node)")
        code, r = call(c, fn, {"op": "basic"})
        check("basic flow -> 200", code == 200, f"{code} {str(r)[:200]}")
        if isinstance(r, dict) and code == 200:
            check("module-scope kv worked at spawn", r.get("moduleResult") == {"n": 1}, str(r.get("moduleResult")))
            check("get returns the stored value", r.get("got") == {"x": 1})
            many = r.get("many") or {}
            check("getMany returns the present keys", many.get("lst-1") == 1 and many.get("lst-2") == 2, str(many))
            keys = [k if isinstance(k, str) else k.get("key") for k in (r.get("listed") or {}).get("keys", [])]
            check("list by prefix", sorted(keys) == ["lst-1", "lst-2"], str(r.get("listed"))[:120])
            check("incr from missing", r.get("inc") == 5)
            check("cas create succeeds", r.get("casOk") is True)
            check("cas mismatch raises OrvaCASMismatch", r.get("casMismatch") == "OrvaCASMismatch", str(r.get("casMismatch")))
            check("deleteMany count", r.get("delN") == 2, str(r.get("delN")))
            check("delete then get default", r.get("gone") == "DEFAULT")
        code, r = call(c, fn, {"op": "env"})
        check("the worker was handed the channel (fd 3, proto 1)",
              isinstance(r, dict) and r.get("fd") == "3" and r.get("proto") == "1", str(r))

        section("python over the channel")
        code, r = call(c, py, {"op": "basic"})
        check("python basic -> 200", code == 200, f"{code} {str(r)[:200]}")
        if isinstance(r, dict) and code == 200:
            check("python get/incr/cas/many/delete",
                  r.get("got") == {"x": 1} and r.get("inc") == 3 and r.get("cas_ok") is True
                  and r.get("cas_mismatch") == "OrvaCASMismatch" and r.get("deleted") == 2
                  and r.get("gone") == "DEFAULT", str(r))

        section("concurrency")
        code, r = call(c, fn, {"op": "concurrent", "n": 200})
        check("200 concurrent incr: exact total and every result distinct",
              isinstance(r, dict) and r.get("final") == 200 and r.get("distinct") == 200, str(r))
        code, r = call(c, py, {"op": "threads", "threads": 8, "per": 50})
        check("python 8 threads x 50 incr: exact total", isinstance(r, dict) and r.get("final") == 400, str(r))

        section("isolation between functions")
        call(c, fn, {"op": "put", "key": "iso-key", "value": "secret-of-A"})
        code, r = call(c, fn2, {"op": "get", "key": "iso-key"})
        check("another function cannot see the key", isinstance(r, dict) and r.get("value") is None, str(r))
        code, r = call(c, fn, {"op": "get", "key": "iso-key"})
        check("the owner still sees it", isinstance(r, dict) and r.get("value") == "secret-of-A")

        section("value size limit")
        code, r = call(c, fn, {"op": "big", "size": 60000})
        check("60 KB value (under the 64 KB limit) stored and read back",
              isinstance(r, dict) and r.get("stored") is True and r.get("len") == 60000, str(r)[:160])
        code, r = call(c, fn, {"op": "big", "size": 70000})
        check("a value just over 64 KB is refused with a validation error",
              isinstance(r, dict) and r.get("stored") is False and r.get("status") in (400, 413), str(r)[:200])
        code, r = call(c, fn, {"op": "big", "size": 5 * 1024 * 1024})
        check("over-limit value is refused with an error, not a crash",
              isinstance(r, dict) and r.get("stored") is False, str(r)[:200])
        code, r = call(c, fn, {"op": "basic"})
        check("the worker is still healthy after the refusal", code == 200)

        section("a call that outlives its request keeps kv access (module-scope semantics)")
        call(c, fn, {"op": "put", "key": "late", "value": "before"})
        code, r = call(c, fn, {"op": "late"})
        check("request returns immediately", code == 200)
        time.sleep(1.5)
        code, r = call(c, fn, {"op": "get", "key": "late"})
        # kv needs no live execution, so a timer firing after the response is served
        # as module scope rather than refused. Ops that do need one (invoke, jobs,
        # crons, spans) are refused with a stale lease; unit tests cover that.
        check("the background write after the response landed", isinstance(r, dict) and r.get("value") == "after-response", str(r))

        section("a worker killed mid-call is replaced")
        slow = deploy(c, NODE_SLOW, "node", SLOW_CODE, "handler.js", timeout_ms=1500)
        check("slow function active", bool(slow))
        if slow:
            code, r = call(c, slow, {"op": "spin"})
            check("spinning kv loop is stopped by the timeout (5xx), not hung", code >= 500, f"{code} {str(r)[:100]}")
            code, r = call(c, slow, {"op": "ok"})
            check("next request runs on a fresh worker",
                  code == 200 and isinstance(r, dict) and r.get("alive") is True, f"{code} {str(r)[:100]}")

        section("python: fork and subprocess")
        code, r = call(c, py, {"op": "fork"})
        check("forked child refuses the channel, parent keeps working",
              isinstance(r, dict) and str(r.get("child", "")).startswith("refused:") and r.get("parent_ok") is True, str(r))
        code, r = call(c, py, {"op": "subproc"})
        try:
            fds = ast.literal_eval((r or {}).get("fds", "[]")) if isinstance(r, dict) else []
        except (ValueError, SyntaxError):
            fds = []
        # The listing opens one directory fd itself, so 0,1,2 + that one = 4; an inherited channel would make it 5.
        check("a subprocess does not inherit the channel descriptor", 0 < len(fds) <= 4, str(fds))

        section("metrics")
        body = ""
        try:
            body = urllib.request.urlopen(c.base + "/metrics", timeout=10).read().decode()
        except Exception as e:  # noqa: BLE001
            check("metrics reachable", False, str(e))
        check("orva_sdk_calls_total{transport=channel,op=kv.get} present",
              'orva_sdk_calls_total{transport="channel",op="kv.get"' in body)
        check("orva_sdk_channels_open gauge present", "orva_sdk_channels_open" in body)
        viol = [ln for ln in body.splitlines() if ln.startswith("orva_sdk_protocol_violations_total ")]
        check("no protocol violations during the run", viol == ["orva_sdk_protocol_violations_total 0"], str(viol))

        section("egress functions also use only the SDK channel")
        eg = deploy(c, NODE_EGRESS, "node", EGRESS_CODE, "handler.js", net="egress")
        if eg:
            code, r = call(c, eg, {})
            check("egress function: channel kv and function-to-function invoke",
                  code == 200 and isinstance(r, dict) and r.get("viaKv") == 1 and r.get("f2f") == 200,
                   f"{code} {str(r)[:160]}")
            check("egress worker has channel fd/protocol and no legacy token/base",
                  isinstance(r, dict) and r.get('fd') == '3' and r.get('proto') == '1'
                  and r.get('exposed') == ['ORVA_SDK_FD', 'ORVA_SDK_PROTO'], str(r)[:200])
        else:
            check("egress function active", False)
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
