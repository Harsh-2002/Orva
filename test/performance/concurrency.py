#!/usr/bin/env python3
"""Scratch-only independent concurrency bursts, NOT sustained/dynamic-load proof.

Warm requires zero new spawns. Cold means initially empty pools with at least
one spawn per targeted pool; workers can be reused within the same burst.
Use invocation_admission.py or loadgen/ for sustained/dynamic workloads.

Usage:
  python3 test/performance/concurrency.py --url http://127.0.0.1:8443 \
      --scratch --api-key "$KEY" --label before --json before.json

Exit nonzero for correctness, classification or cleanup failures, never for a
latency threshold. Native and Docker scratch targets use the same HTTP checks.
"""
import argparse
import json
import math
import os
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from concurrent.futures import ThreadPoolExecutor

MODULE_COUNT = 40
TIMEOUT = 180
MAX_BURST = 64
BARRIER_TIMEOUT = 10


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
        if not 200 <= code < 300:
            raise HarnessError(f"{method} {path} -> HTTP {code}")
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
    k = max(0, min(len(s) - 1, math.ceil((p / 100.0) * len(s)) - 1))
    return round(s[k], 1)


def node_fixture(real):
    if not real:
        return {"code": "exports.handler = async () => ({statusCode:200, body:{ok:true}});\n",
                "filename": "handler.js", "dependencies": None, "extras": None,
                "expected": {"ok": True}}
    reqs = "\n".join(f"const m{i:02d} = require('./lib/m{i:02d}');" for i in range(MODULE_COUNT))
    body = (f"{reqs}\nconst express = require('express');\n\n"
            f"exports.handler = async () => ({{statusCode:200, body:{{n:{MODULE_COUNT}, express: typeof express === 'function'}}}});\n")
    return {"code": body, "filename": "handler.js",
            "dependencies": json.dumps({"name": "conc-node", "version": "1.0.0",
                                        "dependencies": {"express": "5.3.0"}}),
            "extras": {f"lib/m{i:02d}.js": f"module.exports = {{ id: {i} }};\n" for i in range(MODULE_COUNT)},
            "expected": {"n": MODULE_COUNT, "express": True}}


def python_fixture(real):
    if not real:
        return {"code": "def handler(event):\n    return {\"ok\": True}\n",
                "filename": "handler.py", "dependencies": None, "extras": None,
                "expected": {"ok": True}}
    imports = "\n".join(f"from lib import m{i:02d}" for i in range(MODULE_COUNT))
    body = (f"{imports}\n\nMODULES = [{', '.join(f'm{i:02d}' for i in range(MODULE_COUNT))}]\n\n\n"
            "def handler(event):\n    return {\"n\": len(MODULES)}\n")
    extras = {"lib/__init__.py": ""}
    extras.update({f"lib/m{i:02d}.py": f"BASE = {i}\n" for i in range(MODULE_COUNT)})
    return {"code": body, "filename": "handler.py", "dependencies": None, "extras": extras,
            "expected": {"n": MODULE_COUNT}}


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


def warm_pool(t, fn_id, expected, want=8, timeout=30):
    """Warm by issuing requests: the controller sizes from demand, so a floor
    alone does not guarantee `want` idle workers. Best effort preparation;
    any spawn in the measured warm burst makes that scenario INVALID."""
    t.ok("PUT", "/api/v1/pool/config", {
        "function_id": fn_id, "scale_to_zero": False, "min_warm": want, "max_warm": 0,
    })
    for _ in range(want):
        if not one(t, fn_id, expected)["ok"]:
            raise HarnessError(f"{fn_id}: warmup response failed validation")
    deadline = time.time() + timeout
    while time.time() < deadline:
        if int((pools(t).get(fn_id) or {}).get("idle") or 0) >= want:
            break
        time.sleep(0.2)
    return int((pools(t).get(fn_id) or {}).get("idle") or 0)


def one(t, fn_id, expected):
    started = time.monotonic()
    code, body = t.call("POST", f"/fn/{fn_id}", {})
    body_ok = json.dumps(body, sort_keys=True) == json.dumps(expected, sort_keys=True)
    return {"ms": (time.monotonic() - started) * 1000.0, "code": code,
            "ok": 200 <= code < 300 and body_ok,
            "body_mismatch": 200 <= code < 300 and not body_ok}


def burst(t, fn_ids, total, expected):
    """Fire `total` requests across `fn_ids` as simultaneously as possible."""
    if not isinstance(total, int) or not 1 <= total <= MAX_BURST or not fn_ids:
        raise HarnessError(f"burst requires 1..{MAX_BURST} requests and nonempty function ids")
    workers = total
    plan = [fn_ids[i % len(fn_ids)] for i in range(total)]
    barrier = threading.Barrier(len(plan))

    def task(fn_id):
        try:
            barrier.wait(timeout=BARRIER_TIMEOUT)
        except threading.BrokenBarrierError:
            raise HarnessError("burst start barrier timed out") from None
        return one(t, fn_id, expected[fn_id])

    started = time.monotonic()
    with ThreadPoolExecutor(max_workers=workers) as pool:
        results = list(pool.map(task, plan))
    wall = (time.monotonic() - started) * 1000.0
    return results, wall


def summarise(name, results, wall, spawn_delta, extra=None):
    lat = [r["ms"] for r in results if r["ok"]]
    errors = [r for r in results if not r["ok"]]
    out = {
        "scenario": name,
        "requests": len(results),
        "ok": len(lat),
        "errors": len(errors),
        "body_mismatches": sum(r["body_mismatch"] for r in results),
        "error_codes": sorted({r["code"] for r in errors}),
        "wall_ms": round(wall, 1),
        "burst_completion_rate_rps": round(len(results) / (wall / 1000.0), 2) if wall > 0 else None,
        "p50_ms": pct(lat, 50),
        "p95_ms": pct(lat, 95),
        "p99_ms": pct(lat, 99) if len(lat) >= 100 else None,
        "warnings": (["p99 suppressed: fewer than 100 valid samples; short-burst tails are not stable"]
                     if len(lat) < 100 else []),
        "max_ms": round(max(lat), 1) if lat else None,
        "spawned": spawn_delta,
        "spawns_per_request": round(spawn_delta / len(results), 2) if results else None,
    }
    if extra:
        out.update(extra)
    reasons = out.setdefault("invalid_reasons", [])
    if errors:
        reasons.append("invocation status/body validation failed")
    out["validity"] = "INVALID" if reasons else "VALID"
    return out


def classify(mode, fn_ids, before, after):
    reasons = []
    deltas = {}
    for fid in fn_ids:
        if fid not in after or "spawned" not in after[fid]:
            reasons.append(f"{fid}: missing post-burst spawn evidence")
        delta = int(after.get(fid, {}).get("spawned", 0)) - int(before.get(fid, {}).get("spawned", 0))
        deltas[fid] = delta
        if mode == "warm" and ("spawned" not in before.get(fid, {}) or delta != 0):
            reasons.append(f"{fid}: warm requires an existing pool and zero spawn delta")
        if mode == "cold" and (fid in before or delta < 1):
            reasons.append(f"{fid}: cold requires an initially absent pool and at least one spawn")
    return deltas, reasons


def measure(t, name, fn_ids, total, expected, mode):
    if mode == "cold":
        for fid in fn_ids:
            retire(t, fid)
    before = pools(t)
    results, wall = burst(t, fn_ids, total, expected)
    deltas, reasons = classify(mode, fn_ids, before, pools(t))
    return summarise(name, results, wall, sum(deltas.values()), {
        "pool_mode": mode, "spawned_by_function": deltas, "invalid_reasons": reasons,
        "pool_semantics": ("Initially empty pools; workers may be reused within this burst, "
                           "not a fresh worker per request." if mode == "cold" else
                           "Existing pools; zero new spawns required."),
    })


def run(t, args):
    if not args.scratch:
        raise HarnessError("refusing without --scratch")
    run_id = uuid.uuid4().hex[:12]
    out = {"label": args.label, "target": args.url, "run_id": run_id, "scenarios": [],
           "scope": "Independent bursts only; no sustained capacity or dynamic-load proof.",
           "harness_errors": [], "cleanup_errors": []}
    created = []
    expected = {}

    def add(runtime, fixture):
        fid = create(t, f"conc-{run_id}-{runtime}-{len(created)}", runtime)
        created.append(fid)
        expected[fid] = fixture["expected"]
        deploy(t, fid, fixture)
        wait_active(t, fid)
        return fid

    try:
        py = add("python", python_fixture(True))
        node = add("node", node_fixture(True))
        warm_pool(t, py, expected[py], want=16)
        out["scenarios"].append(measure(t, "warm 1 fn / 16 concurrent", [py], 16, expected, "warm"))
        out["scenarios"].append(measure(t, "cold 1 fn / 16 concurrent", [py], 16, expected, "cold"))
        fns = [py, node, add("python", python_fixture(True)), add("python", python_fixture(False))]
        out["scenarios"].append(measure(t, "cold 4 fn / 24 concurrent", fns, 24, expected, "cold"))
        for c in (1, 2, 4, 8, 16):
            out["scenarios"].append(measure(t, f"independent cold burst c={c}", [py], c, expected, "cold"))
    except Exception as e:
        out["harness_errors"].append(str(e) if isinstance(e, HarnessError) else type(e).__name__)
    finally:
        for fid in created:
            try:
                t.ok("DELETE", f"/api/v1/functions/{fid}")
            except Exception as e:
                out["cleanup_errors"].append(str(e) if isinstance(e, HarnessError) else type(e).__name__)
    out["request_errors"] = sum(s["errors"] for s in out["scenarios"])
    out["invalid_scenarios"] = sum(s["validity"] == "INVALID" for s in out["scenarios"])
    out["validity"] = "INVALID" if (out["harness_errors"] or out["cleanup_errors"] or out["invalid_scenarios"]) else "VALID"
    return out


def render(report):
    lines = [f"concurrency — label={report['label']}  target={report['target']}",
             report['scope'], "burst_completion_rate_rps is not sustained capacity; p99 suppressed below 100 valid samples.",
             f"validity={report['validity']} request_errors={report['request_errors']} "
             f"invalid_scenarios={report['invalid_scenarios']} harness_errors={len(report['harness_errors'])} "
             f"cleanup_errors={len(report['cleanup_errors'])}", ""]
    hdr = f"{'scenario':30} {'validity':>7} {'n':>3} {'ok':>3} {'err':>3} {'wall ms':>8} {'burst_completion_rate_rps':>25} " \
          f"{'p50':>7} {'p95':>7} {'p99':>7} {'spawn':>5}"
    lines.append(hdr)
    lines.append("-" * len(hdr))
    for s in report["scenarios"]:
        lines.append(
            f"{s['scenario']:30} {s['validity']:>7} {s['requests']:>3} {s['ok']:>3} {s['errors']:>3} "
            f"{_n(s['wall_ms']):>8} {_n(s['burst_completion_rate_rps']):>25} {_n(s['p50_ms']):>7} "
            f"{_n(s['p95_ms']):>7} {_n(s['p99_ms']):>7} {s['spawned']:>5}"
        )
        lines.append(f"  {s['pool_semantics']}")
        lines.extend(f"  INVALID: {reason}" for reason in s["invalid_reasons"])
    lines.extend(f"HARNESS ERROR: {e}" for e in report["harness_errors"])
    lines.extend(f"CLEANUP ERROR: {e}" for e in report["cleanup_errors"])
    return "\n".join(lines)


def _n(v):
    return "-" if v is None else str(v)


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default=os.environ.get("ORVA_ENDPOINT", "http://127.0.0.1:8443"))
    ap.add_argument("--api-key", default=os.environ.get("ORVA_API_KEY", ""))
    ap.add_argument("--label", default="run")
    ap.add_argument("--json", dest="json_path", default="")
    ap.add_argument("--scratch", action="store_true", help="confirm target is a disposable native/Docker instance")
    args = ap.parse_args(argv)
    if not args.scratch:
        print("refusing without --scratch: target must be disposable", file=sys.stderr)
        return 2
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
    return 0 if report["validity"] == "VALID" else 1


if __name__ == "__main__":
    sys.exit(main())
