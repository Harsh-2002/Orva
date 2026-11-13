#!/usr/bin/env python3
"""Cold-start measurement harness (report-only).

One sample per retired pool generation. Retirement deletes the pool object
(pool.go retirePool), and the spawn ring lives on that object, so a generation
holds exactly one spawn sample; the median/p95 here are computed across
generations, not read from a single ColdStartP95MS.

Each sample is proven cold, not assumed: the pool must be absent before the
invoke, the invoke must return 200, and the recreated pool's spawned counter
must be exactly 1. A sample with 0 or >1 spawns is discarded and counted,
because a prewarm spawn would silently mix a second start into the number.

Usage:
  python3 test/coldstart/coldstart.py --url http://127.0.0.1:8443 \
      --api-key "$KEY" --samples 7 --json coldstart.json

Exit 0 whatever the timings are. A non-zero exit means the harness itself
failed (bad target, unparseable metrics), not that the numbers were bad.
"""
import argparse
import json
import os
import random
import statistics
import sys
import time
import urllib.error
import urllib.request

MODULE_COUNT = 40
INVOKE_TIMEOUT = 120
HTTP_TIMEOUT = 120


class Target:
    def __init__(self, base, key):
        self.base = base.rstrip("/")
        self.key = key

    def call(self, method, path, body=None, headers=None, raw=False):
        data = None
        hdrs = {"X-Orva-API-Key": self.key}
        if body is not None:
            data = json.dumps(body).encode()
            hdrs["Content-Type"] = "application/json"
        if headers:
            hdrs.update(headers)
        req = urllib.request.Request(self.base + path, data=data, headers=hdrs, method=method)
        try:
            with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as r:
                text = r.read().decode()
                return r.status, (text if raw else _json_or_text(text))
        except urllib.error.HTTPError as e:
            text = e.read().decode()
            return e.code, (text if raw else _json_or_text(text))

    def ok(self, method, path, body=None):
        code, payload = self.call(method, path, body)
        if code >= 300:
            raise HarnessError(f"{method} {path} -> HTTP {code}: {str(payload)[:300]}")
        return payload


class HarnessError(Exception):
    pass


def _json_or_text(text):
    try:
        return json.loads(text)
    except ValueError:
        return text


def percentile(values, p):
    """Nearest-rank percentile over the samples actually accepted."""
    s = sorted(values)
    if not s:
        return None
    k = max(0, min(len(s) - 1, int(round((p / 100.0) * len(s) + 0.5)) - 1))
    return s[k]


def npm_module(i):
    return (
        f"// module {i}: a small but real dependency edge\n"
        f"const base = {i};\n"
        f"module.exports = {{ id: base, scale: (x) => x * 2 + base }};\n"
    )


def py_module(i):
    return f"BASE = {i}\n\n\ndef scale(x):\n    return x * 2 + BASE\n"


def node_fixture(real):
    if not real:
        return {
            "code": "exports.handler = async () => ({statusCode: 200, body: {ok: true}});\n",
            "filename": "handler.js",
            "dependencies": None,
            "extras": None,
        }
    lines = [f"const m{i:02d} = require('./lib/m{i:02d}');" for i in range(MODULE_COUNT)]
    lines += [
        "const express = require('express');",
        "",
        "exports.handler = async () => ({",
        "  statusCode: 200,",
        f"  body: {{ modules: [{', '.join(f'm{i:02d}' for i in range(MODULE_COUNT))}].length, "
        "express: typeof express === 'function' },",
        "});",
        "",
    ]
    extras = {f"lib/m{i:02d}.js": npm_module(i) for i in range(MODULE_COUNT)}
    return {
        "code": "\n".join(lines),
        "filename": "handler.js",
        "dependencies": json.dumps({
            "name": "coldstart-node-real",
            "version": "1.0.0",
            "dependencies": {"express": "5.3.0"},
        }),
        "extras": extras,
    }


def python_fixture(real):
    if not real:
        return {
            "code": "def handler(event):\n    return {\"ok\": True}\n",
            "filename": "handler.py",
            "dependencies": None,
            "extras": None,
        }
    imports = "\n".join(f"from lib import m{i:02d}" for i in range(MODULE_COUNT))
    body = (
        f"{imports}\n\n"
        f"MODULES = [{', '.join(f'm{i:02d}' for i in range(MODULE_COUNT))}]\n\n\n"
        "def handler(event):\n"
        "    return {\"modules\": len(MODULES)}\n"
    )
    extras = {"lib/__init__.py": ""}
    extras.update({f"lib/m{i:02d}.py": py_module(i) for i in range(MODULE_COUNT)})
    return {
        "code": body,
        "filename": "handler.py",
        "dependencies": None,
        "extras": extras,
    }


def create_function(t, name, runtime):
    payload = {
        "name": name,
        "runtime": runtime,
        "memory_mb": 256,
        "cpus": 1,
        # npm/pip install during the build needs the jailed egress policy.
        "network_mode": "egress",
    }
    try:
        return t.ok("POST", "/api/v1/functions", payload)
    except HarnessError as e:
        raise HarnessError(f"create {name}: {e}") from e


def deploy(t, fn_id, fx):
    body = {"code": fx["code"], "filename": fx["filename"]}
    if fx["dependencies"]:
        body["dependencies"] = fx["dependencies"]
    if fx["extras"]:
        body["extras"] = fx["extras"]
    t.ok("POST", f"/api/v1/functions/{fn_id}/deploy-inline", body)


def wait_active(t, fn_id, timeout=300):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        fn = t.ok("GET", f"/api/v1/functions/{fn_id}")
        last = fn.get("status")
        if last == "active":
            return True
        if last == "error":
            return False
        time.sleep(1)
    raise HarnessError(f"{fn_id} stayed {last} for {timeout}s")


def pool_row(t, fn_id):
    data = t.ok("GET", "/api/v1/system/metrics.json")
    for p in data.get("pools") or []:
        if p.get("function_id") == fn_id:
            return p
    return None


def global_spawn_histogram(t):
    """(count, sum_ms) of the process-wide, unlabelled spawn histogram."""
    code, text = t.call("GET", "/metrics", raw=True)
    if code != 200:
        raise HarnessError(f"GET /metrics -> HTTP {code}")
    count = total = None
    for line in str(text).splitlines():
        if line.startswith("orva_sandbox_spawn_duration_ms_count "):
            count = int(float(line.split()[1]))
        elif line.startswith("orva_sandbox_spawn_duration_ms_sum "):
            total = float(line.split()[1])
    return count, total


def retire(t, fn_id):
    """Remove the pool generation so the next invoke must spawn a worker."""
    t.ok("PUT", "/api/v1/pool/config", {
        "function_id": fn_id,
        "scale_to_zero": True,
        "min_warm": 0,
    })


def one_sample(t, fn_id):
    retire(t, fn_id)
    # Precondition, not an assumption: the generation is gone.
    for _ in range(50):
        if pool_row(t, fn_id) is None:
            break
        time.sleep(0.1)
    else:
        return {"status": "discarded", "why": "pool survived retirement"}

    before_count, before_sum = global_spawn_histogram(t)
    started = time.monotonic()
    code, _ = t.call("POST", f"/fn/{fn_id}", {})
    wall_ms = (time.monotonic() - started) * 1000.0
    after_count, after_sum = global_spawn_histogram(t)

    row = pool_row(t, fn_id)
    if code != 200:
        return {"status": "failed", "why": f"invoke HTTP {code}"}
    if row is None:
        return {"status": "failed", "why": "no pool row after invoke"}

    spawned = int(row.get("spawned") or 0)
    if spawned == 0:
        return {"status": "discarded", "why": "no spawn (warm worker served it)"}
    if spawned > 1:
        return {"status": "discarded", "why": f"{spawned} spawns in one generation"}

    delta_count = (after_count or 0) - (before_count or 0)
    delta_sum = (after_sum or 0.0) - (before_sum or 0.0)
    global_avg = (delta_sum / delta_count) if delta_count == 1 else None
    return {
        "status": "accepted",
        "spawn_ms": round(float(row.get("cold_start_p95_ms") or 0.0), 1),
        "wall_ms": round(wall_ms, 1),
        "global_avg_ms": round(global_avg, 1) if global_avg is not None else None,
    }


def measure(t, fn_id, samples, max_attempts):
    accepted, discarded, failed = [], {"zero": 0, "multi": 0, "other": 0}, 0
    notes = []
    attempts = 0
    while len(accepted) < samples and attempts < max_attempts:
        attempts += 1
        r = one_sample(t, fn_id)
        if r["status"] == "accepted":
            accepted.append(r)
        elif r["status"] == "discarded":
            if "no spawn" in r["why"]:
                discarded["zero"] += 1
            elif "spawns in one generation" in r["why"]:
                discarded["multi"] += 1
            else:
                discarded["other"] += 1
            notes.append(r["why"])
        else:
            failed += 1
            notes.append(r["why"])

    spawn = [r["spawn_ms"] for r in accepted]
    glob = [r["global_avg_ms"] for r in accepted if r.get("global_avg_ms") is not None]
    median = round(statistics.median(spawn), 1) if spawn else None
    p95 = round(percentile(spawn, 95), 1) if spawn else None
    global_median = round(statistics.median(glob), 1) if glob else None
    discrepancy = None
    if median and global_median:
        discrepancy = round(abs(median - global_median) / global_median * 100.0, 1)
    return {
        "accepted": len(accepted),
        "attempts": attempts,
        "discarded": discarded,
        "failed": failed,
        "median_ms": median,
        "p95_ms": p95,
        "min_ms": min(spawn) if spawn else None,
        "max_ms": max(spawn) if spawn else None,
        "global_median_ms": global_median,
        "discrepancy_pct": discrepancy,
        "samples_ms": spawn,
        "notes": sorted(set(notes))[:5],
    }


def run(t, samples, max_attempts):
    run_id = "".join(random.choice("0123456789abcdef") for _ in range(4))
    created = []
    fixtures = [
        ("node", "real", "node", node_fixture(True)),
        ("node", "trivial", "node", node_fixture(False)),
        ("python", "real", "python", python_fixture(True)),
        ("python", "trivial", "python", python_fixture(False)),
    ]
    results = {}
    deploy_failed = []
    try:
        for runtime, kind, api_runtime, fx in fixtures:
            name = f"coldstart-{runtime}-{kind}-{run_id}"
            try:
                fn = create_function(t, name, api_runtime)
                fn_id = fn["id"]
                created.append(fn_id)
                deploy(t, fn_id, fx)
            except HarnessError as e:
                deploy_failed.append(f"{runtime}/{kind}: {e}")
                continue
            if not wait_active(t, fn_id):
                deploy_failed.append(f"{runtime}/{kind}: build failed")
                continue
            results.setdefault(runtime, {})[kind] = measure(t, fn_id, samples, max_attempts)

        for runtime, kinds in results.items():
            real, trivial = kinds.get("real"), kinds.get("trivial")
            if real and trivial and real["median_ms"] and trivial["median_ms"]:
                kinds["ratio"] = round(real["median_ms"] / trivial["median_ms"], 2)
    finally:
        for fn_id in created:
            try:
                t.call("DELETE", f"/api/v1/functions/{fn_id}")
            except Exception:
                pass
    return {"runtimes": results, "deploy_failed": deploy_failed, "run_id": run_id}


def render(report, target):
    out = []
    out.append(f"cold-start harness — {target}")
    out.append(f"run {report['run_id']} — report only, no threshold")
    out.append("")
    out.append(f"{'runtime':8} {'fixture':8} {'n':>2} {'p50 ms':>7} {'p95 ms':>7} "
               f"{'min':>7} {'max':>7} {'discard 0/>1':>12} {'global p50':>10} {'delta':>6}")
    for runtime in ("node", "python"):
        kinds = report["runtimes"].get(runtime) or {}
        for kind in ("real", "trivial"):
            m = kinds.get(kind)
            if not m:
                out.append(f"{runtime:8} {kind:8}  (not measured)")
                continue
            d = m["discarded"]
            out.append(
                f"{runtime:8} {kind:8} {m['accepted']:>2} {_n(m['median_ms']):>7} {_n(m['p95_ms']):>7} "
                f"{_n(m['min_ms']):>7} {_n(m['max_ms']):>7} "
                f"{str(d['zero']) + '/' + str(d['multi']):>12} "
                f"{_n(m['global_median_ms']):>10} {_n(m['discrepancy_pct']):>6}"
            )
        if kinds.get("ratio"):
            out.append(f"{runtime:8} real/trivial median ratio: {kinds['ratio']}x")
        out.append("")
    for f in report["deploy_failed"]:
        out.append(f"deploy failed: {f}")
    out.append("")
    out.append("delta = |pool sample - process-wide histogram| / histogram, accepted samples only.")
    return "\n".join(out)


def _n(v):
    return "-" if v is None else str(v)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default=os.environ.get("ORVA_ENDPOINT", "http://127.0.0.1:8443"))
    ap.add_argument("--api-key", default=os.environ.get("ORVA_API_KEY", ""))
    ap.add_argument("--samples", type=int, default=7)
    ap.add_argument("--max-attempts", type=int, default=0,
                    help="default: 3x samples")
    ap.add_argument("--json", dest="json_path", default="")
    args = ap.parse_args()

    if args.samples < 5:
        print("--samples must be >= 5", file=sys.stderr)
        return 2
    key = args.api_key
    if not key:
        print("--api-key or ORVA_API_KEY is required", file=sys.stderr)
        return 2

    t = Target(args.url, key)
    try:
        t.ok("GET", "/api/v1/system/health")
        attempt_cap = args.max_attempts or args.samples * 3
        report = run(t, args.samples, attempt_cap)
    except HarnessError as e:
        print(f"HARNESS ERROR: {e}", file=sys.stderr)
        return 1

    report["target"] = args.url
    report["generated_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    print(render(report, args.url))
    if args.json_path:
        with open(args.json_path, "w", encoding="utf-8") as f:
            json.dump(report, f, indent=2, sort_keys=True)
            f.write("\n")
        print(f"\njson: {args.json_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
