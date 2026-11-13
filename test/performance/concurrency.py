#!/usr/bin/env python3
"""Concurrency benchmark: warm, cold, multi-function and ramp scenarios.

Measures what a runtime change actually does under concurrent load, not just
one sample at a time. Every scenario is explicit about whether the pool is warm
(warm hits only) or retired (every request has to cause a spawn), and records
the spawned count from the server so "cold" is proven rather than assumed.

Usage:
  python3 test/performance/concurrency.py --url http://127.0.0.1:8443 \
      --api-key "$KEY" --label before --json before.json

Exit 0 on completion. Non-zero means the harness failed, not that a number was
bad; per-request errors are reported in the results instead.
"""
import argparse
import json
import os
import statistics
import sys
import threading
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor

MODULE_COUNT = 40
TIMEOUT = 180


class Target:
    def __init__(self, base, key):
        self.base = base.rstrip("/")
        self.key = key

    def call(self, method, path, body=None, raw=False):
        data = json.dumps(body).encode() if body is not None else None
        hdrs = {"X-Orva-API-Key": self.key}
        if data:
            hdrs["Content-Type"] = "application/json"
        req = urllib.request.Request(self.base + path, data=data, headers=hdrs, method=method)
        try:
            with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
                text = r.read().decode()
                return r.status, (text if raw else _maybe_json(text))
        except urllib.error.HTTPError as e:
            return e.code, _maybe_json(e.read().decode())
        except Exception as e:  # connection reset, timeout, ...
            return 0, str(e)

    def ok(self, method, path, body=None):
        code, payload = self.call(method, path, body)
        if code >= 300:
            raise HarnessError(f"{method} {path} -> HTTP {code}: {str(payload)[:200]}")
        return payload


class HarnessError(Exception):
    pass


def _maybe_json(text):
    try:
        return json.loads(text)
    except ValueError:
        return text


def pct(values, p):
    if not values:
        return None
    s = sorted(values)
    k = max(0, min(len(s) - 1, int(round((p / 100.0) * len(s) + 0.5)) - 1))
    return round(s[k], 1)


def node_fixture(real):
    if not real:
        return {"code": "exports.handler = async () => ({statusCode:200, body:{ok:true}});\n",
                "filename": "handler.js", "dependencies": None, "extras": None}
    reqs = "\n".join(f"const m{i:02d} = require('./lib/m{i:02d}');" for i in range(MODULE_COUNT))
    body = (f"{reqs}\nconst express = require('express');\n\n"
            f"exports.handler = async () => ({{statusCode:200, body:{{n:{MODULE_COUNT}, express: typeof express === 'function'}}}});\n")
    return {"code": body, "filename": "handler.js",
            "dependencies": json.dumps({"name": "conc-node", "version": "1.0.0",
                                        "dependencies": {"express": "5.3.0"}}),
            "extras": {f"lib/m{i:02d}.js": f"module.exports = {{ id: {i} }};\n" for i in range(MODULE_COUNT)}}


def python_fixture(real):
    if not real:
        return {"code": "def handler(event):\n    return {\"ok\": True}\n",
                "filename": "handler.py", "dependencies": None, "extras": None}
    imports = "\n".join(f"from lib import m{i:02d}" for i in range(MODULE_COUNT))
    body = (f"{imports}\n\nMODULES = [{', '.join(f'm{i:02d}' for i in range(MODULE_COUNT))}]\n\n\n"
            "def handler(event):\n    return {\"n\": len(MODULES)}\n")
    extras = {"lib/__init__.py": ""}
    extras.update({f"lib/m{i:02d}.py": f"BASE = {i}\n" for i in range(MODULE_COUNT)})
    return {"code": body, "filename": "handler.py", "dependencies": None, "extras": extras}


def create(t, name, runtime):
    fn = t.ok("POST", "/api/v1/functions", {
        "name": name, "runtime": runtime, "memory_mb": 256, "cpus": 1,
        "network_mode": "egress",
    })
    return fn["id"]


def deploy(t, fn_id, fx):
    body = {"code": fx["code"], "filename": fx["filename"]}
    if fx["dependencies"]:
        body["dependencies"] = fx["dependencies"]
    if fx["extras"]:
        body["extras"] = fx["extras"]
    t.ok("POST", f"/api/v1/functions/{fn_id}/deploy-inline", body)


def wait_active(t, fn_id, timeout=600):
    deadline = time.time() + timeout
    while time.time() < deadline:
        fn = t.ok("GET", f"/api/v1/functions/{fn_id}")
        if fn.get("status") == "active":
            return True
        if fn.get("status") == "error":
            raise HarnessError(f"{fn_id} build failed")
        time.sleep(1)
    raise HarnessError(f"{fn_id} never became active")


def pools(t):
    data = t.ok("GET", "/api/v1/system/metrics.json")
    return {p["function_id"]: p for p in (data.get("pools") or [])}


def spawned(t, fn_id):
    return int((pools(t).get(fn_id) or {}).get("spawned") or 0)


def retire(t, fn_id):
    """scale_to_zero + min_warm 0 retires the generation; max_warm 0 leaves the
    controller free to scale up to whatever the burst asks for."""
    t.ok("PUT", "/api/v1/pool/config", {
        "function_id": fn_id, "scale_to_zero": True, "min_warm": 0, "max_warm": 0,
    })
    for _ in range(100):
        if fn_id not in pools(t):
            return
        time.sleep(0.05)
    raise HarnessError(f"{fn_id}: pool survived retirement")


def warm_pool(t, fn_id, want=8, timeout=30):
    """Warm by issuing requests: the controller sizes from demand, so a floor
    alone does not guarantee `want` idle workers. Best effort by design — the
    scenario reports the spawn delta it actually observed."""
    t.ok("PUT", "/api/v1/pool/config", {
        "function_id": fn_id, "scale_to_zero": False, "min_warm": want, "max_warm": 0,
    })
    for _ in range(want):
        one(t, fn_id)
    deadline = time.time() + timeout
    while time.time() < deadline:
        if int((pools(t).get(fn_id) or {}).get("idle") or 0) >= want:
            break
        time.sleep(0.2)
    return int((pools(t).get(fn_id) or {}).get("idle") or 0)


def one(t, fn_id):
    started = time.monotonic()
    code, _ = t.call("POST", f"/fn/{fn_id}", {})
    return {"ms": (time.monotonic() - started) * 1000.0, "code": code}


def burst(t, fn_ids, total):
    """Fire `total` requests across `fn_ids` as simultaneously as possible."""
    workers = max(1, min(total, 64))
    plan = [fn_ids[i % len(fn_ids)] for i in range(total)]
    barrier = threading.Barrier(len(plan))

    def task(fn_id):
        barrier.wait()
        return one(t, fn_id)

    started = time.monotonic()
    with ThreadPoolExecutor(max_workers=workers) as pool:
        results = list(pool.map(task, plan))
    wall = (time.monotonic() - started) * 1000.0
    return results, wall


def summarise(name, results, wall, spawn_delta, extra=None):
    lat = [r["ms"] for r in results if r["code"] == 200]
    errors = [r for r in results if r["code"] != 200]
    out = {
        "scenario": name,
        "requests": len(results),
        "ok": len(lat),
        "errors": len(errors),
        "error_codes": sorted({r["code"] for r in errors}),
        "wall_ms": round(wall, 1),
        "rps": round(len(results) / (wall / 1000.0), 2) if wall > 0 else None,
        "p50_ms": pct(lat, 50),
        "p95_ms": pct(lat, 95),
        "p99_ms": pct(lat, 99),
        "max_ms": round(max(lat), 1) if lat else None,
        "spawned": spawn_delta,
        "spawns_per_request": round(spawn_delta / len(results), 2) if results else None,
    }
    if extra:
        out.update(extra)
    return out


def run(t, args):
    out = {"label": args.label, "target": args.url, "scenarios": []}
    py = create(t, f"conc-py-{args.label}", "python")
    node = create(t, f"conc-node-{args.label}", "node")
    created = [py, node]
    try:
        deploy(t, py, python_fixture(True))
        deploy(t, node, node_fixture(True))
        wait_active(t, py)
        wait_active(t, node)

        # 1. warm hits, one function: no spawns, pure handler+proxy cost
        warm_pool(t, py, want=8)
        before = spawned(t, py)
        results, wall = burst(t, [py], 16)
        out["scenarios"].append(summarise("warm 1 fn / 16 concurrent", results, wall,
                                         spawned(t, py) - before))

        # 2. cold, one function: pool retired, 16 concurrent all need a worker
        retire(t, py)
        before = spawned(t, py)
        results, wall = burst(t, [py], 16)
        out["scenarios"].append(summarise("cold 1 fn / 16 concurrent", results, wall,
                                         spawned(t, py) - before))

        # 3. cold, four functions: 24 requests spread over 4 independent pools
        extra_fns = []
        for i in range(2):
            fid = create(t, f"conc-py-extra{i}-{args.label}", "python")
            created.append(fid)
            deploy(t, fid, python_fixture(i == 0))
            wait_active(t, fid)
            extra_fns.append(fid)
        fns = [py, node] + extra_fns
        for fid in fns:
            retire(t, fid)
        before = {fid: spawned(t, fid) for fid in fns}
        results, wall = burst(t, fns, 24)
        delta = sum(spawned(t, fid) - before[fid] for fid in fns)
        out["scenarios"].append(summarise("cold 4 fn / 24 concurrent", results, wall, delta))

        # 4. ramp on one cold pool: how the tail grows with concurrency
        for c in (1, 2, 4, 8, 16):
            retire(t, py)
            before = spawned(t, py)
            results, wall = burst(t, [py], c)
            out["scenarios"].append(summarise(f"cold ramp c={c}", results, wall,
                                             spawned(t, py) - before))
    finally:
        for fid in created:
            t.call("DELETE", f"/api/v1/functions/{fid}")
    return out


def render(report):
    lines = [f"concurrency — label={report['label']}  target={report['target']}", ""]
    hdr = f"{'scenario':30} {'n':>3} {'ok':>3} {'err':>3} {'wall ms':>8} {'rps':>6} " \
          f"{'p50':>7} {'p95':>7} {'p99':>7} {'spawn':>5}"
    lines.append(hdr)
    lines.append("-" * len(hdr))
    for s in report["scenarios"]:
        lines.append(
            f"{s['scenario']:30} {s['requests']:>3} {s['ok']:>3} {s['errors']:>3} "
            f"{_n(s['wall_ms']):>8} {_n(s['rps']):>6} {_n(s['p50_ms']):>7} "
            f"{_n(s['p95_ms']):>7} {_n(s['p99_ms']):>7} {s['spawned']:>5}"
        )
    return "\n".join(lines)


def _n(v):
    return "-" if v is None else str(v)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default=os.environ.get("ORVA_ENDPOINT", "http://127.0.0.1:8443"))
    ap.add_argument("--api-key", default=os.environ.get("ORVA_API_KEY", ""))
    ap.add_argument("--label", default="run")
    ap.add_argument("--json", dest="json_path", default="")
    args = ap.parse_args()
    if not args.api_key:
        print("--api-key or ORVA_API_KEY required", file=sys.stderr)
        return 2
    t = Target(args.url, args.api_key)
    try:
        t.ok("GET", "/api/v1/system/health")
        report = run(t, args)
    except HarnessError as e:
        print(f"HARNESS ERROR: {e}", file=sys.stderr)
        return 1
    print(render(report))
    if args.json_path:
        with open(args.json_path, "w", encoding="utf-8") as f:
            json.dump(report, f, indent=2, sort_keys=True)
            f.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
