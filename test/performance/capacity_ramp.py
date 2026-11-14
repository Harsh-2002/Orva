#!/usr/bin/env python3
"""Scratch-only open-loop capacity ramp and recovery series.

Needs the loadgen binary (CGO_ENABLED=0 go build ./test/performance/loadgen) and
a disposable instance that reports sandbox_resource_limits=cgroup_v2. Pin the
server and the load generator to different CPUs so the client cannot starve it
(see docs/CAPACITY.md for the rig). It deploys and removes only functions named
cap-*. Results are measurements on that rig, never a capacity promise.

  capacity_ramp.py --scratch --key-file KEY ramp [node-hello py-hello ...]
  capacity_ramp.py --scratch --key-file KEY series py-hello 1200

ramp: per workload, 10 s open-loop steps at rising rates until a step misses a
criterion, then a 2x overload step and a half-rate recovery step.
series: a steady control, then a 4x overload, each followed by 5 s windows at
the same rate, with the pool's spawn/kill deltas beside each window.

A step is GOOD when achieved >= 95% of scheduled, nothing unsent, no transport
errors, non-2xx <= 0.5%, p99 < 1000 ms and no writer loss. Writer loss mixes
best-effort telemetry drops with critical failures; `series` reports them apart.
"""
import argparse
import json
import subprocess
import sys
import time
import urllib.request

RATES = [100, 200, 400, 800, 1200, 1600, 2400, 3200, 4800, 6400]
SPECS = {
    "cap-node-hello": ("node", "handler.js", "exports.handler = async () => ({statusCode: 200, body: 'ok'});\n"),
    "cap-py-hello": ("python", "handler.py", "def handler(event):\n    return {'statusCode': 200, 'body': 'ok'}\n"),
    # ~3 ms of wall-clock spin per call, so a CPU limit is exercised.
    "cap-node-cpu": ("node", "handler.js",
                     "exports.handler = async () => {\n"
                     "  const end = process.hrtime.bigint() + 3000000n; let x = 0;\n"
                     "  while (process.hrtime.bigint() < end) x += Math.sqrt(x + 1);\n"
                     "  return {statusCode: 200, body: 'ok'};\n};\n"),
}
WORKLOADS = {
    "node-hello": ["cap-node-hello"], "py-hello": ["cap-py-hello"],
    "node-cpu-3ms": ["cap-node-cpu"], "mixed": ["cap-node-hello", "cap-py-hello", "cap-node-cpu"],
}


class Rig:
    def __init__(self, a):
        self.a = a
        self.key = open(a.key_file).read().strip()
        self.urls = {}

    def req(self, method, path, body=None):
        r = urllib.request.Request(self.a.url + path, method=method, data=None if body is None else json.dumps(body).encode(),
                                   headers={"Content-Type": "application/json", "X-Orva-API-Key": self.key})
        with urllib.request.urlopen(r, timeout=60) as resp:
            raw = resp.read().decode()
            return json.loads(raw) if raw[:1] in "{[" else raw

    def cleanup(self):
        for f in self.req("GET", "/api/v1/functions").get("functions") or []:
            if f["name"] in SPECS:
                self.req("DELETE", f"/api/v1/functions/{f['id']}")

    def deploy(self):
        self.cleanup()
        for name, (runtime, fname, code) in SPECS.items():
            fn = self.req("POST", "/api/v1/functions", {"name": name, "runtime": runtime, "entrypoint": fname,
                          "memory_mb": 128, "timeout_ms": 10000, "cpus": 0.5, "network_mode": "none", "auth_mode": "none"})
            self.req("POST", f"/api/v1/functions/{fn['id']}/deploy-inline", {"code": code, "filename": fname})
            for _ in range(240):
                if self.req("GET", f"/api/v1/functions/{fn['id']}").get("status") == "active":
                    break
                time.sleep(0.5)
            else:
                raise SystemExit("deploy timed out: " + name)
            self.urls[name] = f"{self.a.url}/fn/{fn['id']}"

    def health(self):
        return self.req("GET", "/api/v1/system/health")

    def pool(self, fn):
        for p in self.req("GET", "/api/v1/system/metrics.json").get("pools", []):
            if p["function_name"] == fn:
                return p
        return {}

    def loadgen(self, names, rate, seconds):
        cmd = ["taskset", "-c", self.a.client_cpus, self.a.loadgen, "-rate", str(rate),
               "-requests", str(int(rate * seconds)), "-concurrency", "2000", "-timeout", "15s",
               "-require-cgroup-v2", "-observe-url", self.a.url, "-drain-timeout", "60s"]
        for n in names:
            cmd += ["-url", self.urls[n]]
        # loadgen exits 1 when it ran and measured but saw errors, transport failures
        # or unsent requests, which is what an overload step is for; 2 means it never
        # ran (bad flags, unreachable server, unenforced cgroups).
        p = subprocess.run(cmd, capture_output=True, text=True)
        if p.returncode not in (0, 1) or not p.stdout.strip():
            raise SystemExit(f"loadgen did not run (exit {p.returncode}): {p.stderr.strip()[:300]}")
        return json.loads(p.stdout)


def cpu_times():
    out = {}
    for line in open("/proc/stat"):
        if line.startswith("cpu") and line[3].isdigit():
            v = list(map(int, line.split()[1:9]))
            out[int(line.split()[0][3:])] = (sum(v) - v[3] - v[4], sum(v))
    return out


def ramp_step(rig, workload, rate, seconds, label):
    before = cpu_times()
    d = rig.loadgen(WORKLOADS[workload], rate, seconds)
    after = cpu_times()
    util = {c: round(100 * (after[c][0] - before[c][0]) / max(1, after[c][1] - before[c][1]), 1) for c in before}
    lat = d["latency_by_status"].get("200", {})
    non2xx = sum(c for s, c in d["status"].items() if not s.startswith("2"))
    transport = sum((d["transport_errors"] or {}).values())
    w = d["writer"]
    lost = w["critical_failures"] + w["critical_timeouts"] + w["dropped_telemetry"] + w["dropped_activity"]
    ach = d["successes_per_second"]
    good = (d["unsent"] == 0 and transport == 0 and lost == 0 and w["observation_complete"] and ach >= 0.95 * rate
            and non2xx <= 0.005 * d["attempted"] and lat.get("p99_ms", 1e9) < 1000)
    print(json.dumps({"workload": workload, "label": label, "rate": rate, "achieved": round(ach, 1),
                      "p50": round(lat.get("p50_ms", 0), 2), "p95": round(lat.get("p95_ms", 0), 2),
                      "p99": round(lat.get("p99_ms", 0), 2), "status": d["status"], "error_codes": d["error_codes"],
                      "unsent": d["unsent"], "transport_errors": transport, "writer_lost": lost,
                      "cpu_busy_pct": util, "limits": w["sandbox_resource_limits"], "good": good}), flush=True)
    return good


def do_ramp(rig, names):
    for wl in names or list(WORKLOADS):
        ramp_step(rig, wl, 50, 4, "warmup")
        last = 0
        for rate in RATES:
            if not ramp_step(rig, wl, rate, 10, "ramp"):
                break
            last = rate
        if last:
            ramp_step(rig, wl, last * 2, 15, "overload")
            time.sleep(3)
            ramp_step(rig, wl, max(50, last // 2), 15, "recovery")
        print(json.dumps({"workload": wl, "last_good_rate": last}), flush=True)


def window(rig, fn, tag, i, rate, seconds, prev):
    d = rig.loadgen([fn], rate, seconds)
    p = rig.pool(fn)
    lat = d["latency_by_status"].get("200", {})
    w = d["writer"]
    print(json.dumps({"tag": tag, "i": i, "rate": rate, "status": d["status"], "unsent": d["unsent"],
                      "p50": round(lat.get("p50_ms", 0), 1), "p95": round(lat.get("p95_ms", 0), 1),
                      "p99": round(lat.get("p99_ms", 0), 1),
                      "spawn_d": p.get("spawned", 0) - prev.get("spawned", 0), "kill_d": p.get("killed", 0) - prev.get("killed", 0),
                      "idle": p.get("idle"), "queued": p.get("queued"), "desired": p.get("desired_workers"),
                      "max": p.get("effective_max"), "limit": p.get("limiting_reason"), "cold_p95": p.get("cold_start_p95_ms"),
                      "crit_fail": w["critical_failures"], "crit_timeout": w["critical_timeouts"],
                      "drop_tel": w["dropped_telemetry"], "drop_act": w["dropped_activity"]}), flush=True)
    return p


def do_series(rig, workload, rate):
    fn = WORKLOADS[workload][0]
    prev = rig.pool(fn)
    prev = window(rig, fn, "control", -1, 50, 4, prev)
    for i in range(18):
        prev = window(rig, fn, "control", i, rate, 5, prev)
    time.sleep(45)  # let the pool scale down so the two phases are independent
    prev = rig.pool(fn)
    prev = window(rig, fn, "overload", 0, rate * 4, 15, prev)
    time.sleep(3)
    for i in range(18):
        prev = window(rig, fn, "recovery", i, rate, 5, prev)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--scratch", action="store_true", help="confirm the target is a disposable instance")
    ap.add_argument("--url", default="http://127.0.0.1:8443")
    ap.add_argument("--key-file", required=True)
    ap.add_argument("--loadgen", default="loadgen")
    ap.add_argument("--client-cpus", default="2,3", help="CPU list for the load generator (taskset)")
    ap.add_argument("mode", choices=["ramp", "series"])
    ap.add_argument("args", nargs="*")
    a = ap.parse_args()
    if not a.scratch:
        sys.exit("refusing to run without --scratch: this deploys functions and drives heavy load")
    rig = Rig(a)
    if rig.health()["sandbox"]["resource_limits"] != "cgroup_v2":
        sys.exit("refusing: the instance does not report cgroup_v2 sandbox limits")
    rig.deploy()
    try:
        if a.mode == "ramp":
            do_ramp(rig, a.args)
        else:
            do_series(rig, a.args[0] if a.args else "py-hello", int(a.args[1]) if len(a.args) > 1 else 1200)
    finally:
        rig.cleanup()


if __name__ == "__main__":
    main()
