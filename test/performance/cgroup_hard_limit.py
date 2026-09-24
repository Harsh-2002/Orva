#!/usr/bin/env python3
"""Scratch-only proof of real memory, CPU, and PID cgroup enforcement.

Run inside a disposable delegated Linux VM/container, never against an operator
instance. The explicit --scratch flag is required. The script creates and
deletes its own functions. The memory case intentionally
crashes a sandbox; all cases require a kernel counter change, not just an HTTP
status that could come from an unrelated sandbox or protocol failure.
"""

import argparse
import json
import pathlib
import time
import urllib.error
import urllib.request
import uuid


def request(base, key, method, path, payload=None):
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(
        base + path,
        data=data,
        method=method,
        headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            raw = resp.read()
            try:
                return resp.status, json.loads(raw or b"{}")
            except json.JSONDecodeError:
                return resp.status, {"body": raw.decode(errors="replace")}
    except urllib.error.HTTPError as exc:
        try:
            body = json.loads(exc.read() or b"{}")
        except json.JSONDecodeError:
            body = {}
        return exc.code, body


def counter(path, name):
    return int(dict(line.split() for line in path.read_text().splitlines()).get(name, "0"))


def probe_counter(worker_dir, kind, stat_file, stat_name):
    if kind == "memory":
        # memory.events is hierarchical, so it survives an OOM-killed leaf.
        return counter(worker_dir / stat_file, stat_name)
    total = 0
    for child in worker_dir.glob("NSJAIL.*"):
        try:
            if not (child / "cgroup.procs").read_text().strip():
                continue
            if kind == "cpu" and (child / "cpu.max").read_text().split()[0] != "250000":
                continue
            if kind == "pids" and (child / "pids.max").read_text().strip() != "32":
                continue
            total += counter(child / stat_file, stat_name)
        except (FileNotFoundError, ProcessLookupError):
            # A worker may retire while we inspect its cgroup.
            continue
    return total


def check(args, key, worker_dir, kind, memory_mb, cpus, handler, stat_file,
          stat_name, expected_status, runtime="node"):
    name = "cgroup-hard-limit-" + kind + "-" + uuid.uuid4().hex[:8]
    entrypoint = "handler.py" if runtime == "python" else "handler.js"
    code, created = request(args.endpoint, key, "POST", "/api/v1/functions", {
        "name": name, "runtime": runtime, "entrypoint": entrypoint,
        "timeout_ms": 30000, "memory_mb": memory_mb, "cpus": cpus,
        "network_mode": "none", "auth_mode": "none",
    })
    if code not in (200, 201) or not created.get("id"):
        raise RuntimeError(f"{kind}: create failed: HTTP {code} {created}")
    fid = created["id"]
    try:
        code, deployed = request(args.endpoint, key, "POST", f"/api/v1/functions/{fid}/deploy-inline",
                                 {"code": handler, "filename": entrypoint})
        if code not in (200, 202):
            raise RuntimeError(f"{kind}: deploy failed: HTTP {code} {deployed}")
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            code, function = request(args.endpoint, key, "GET", f"/api/v1/functions/{fid}")
            if code == 200 and function.get("status") in ("active", "error"):
                break
            time.sleep(0.5)
        if code != 200 or function.get("status") != "active":
            raise RuntimeError(f"{kind}: function not active: {function}")

        before = probe_counter(worker_dir, kind, stat_file, stat_name)
        code, response = request(args.endpoint, key, "POST", f"/fn/{fid}", {})
        deadline = time.monotonic() + 5
        after = probe_counter(worker_dir, kind, stat_file, stat_name)
        while after == before and time.monotonic() < deadline:
            time.sleep(0.05)
            after = probe_counter(worker_dir, kind, stat_file, stat_name)
        if code != expected_status or after <= before:
            raise RuntimeError(f"{kind}: hard cap not proved: HTTP {code}, "
                               f"{stat_name} {before}->{after}, body={response}")
        if kind == "pids" and int(response.get("failed", 0)) < 1:
            raise RuntimeError(f"pids: kernel recorded exhaustion but handler did not: {response}")
        return {"result": "pass", "check": kind, "http_status": code,
                "counter": stat_name, "before": before, "after": after}
    finally:
        # Let accepted completion writes drain before the function FK is gone.
        time.sleep(1)
        request(args.endpoint, key, "DELETE", f"/api/v1/functions/{fid}")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--endpoint", default="http://127.0.0.1:8443")
    ap.add_argument("--key-file", default="/var/lib/orva/.admin-key")
    ap.add_argument("--worker-cgroup", required=True,
                    help="Orva-owned orva.workers cgroup path in a disposable VM/container")
    ap.add_argument("--scratch", action="store_true",
                    help="confirm that this is a disposable instance")
    ap.add_argument("--checks", nargs="+", choices=("memory", "cpu", "pids"),
                    default=("memory", "cpu", "pids"),
                    help="specific hard-limit proofs to run (default: all)")
    args = ap.parse_args()
    if not args.scratch:
        raise SystemExit("refusing to mutate an instance without --scratch")
    key = pathlib.Path(args.key_file).read_text().strip()
    worker_dir = pathlib.Path(args.worker_cgroup)
    if worker_dir.name != "orva.workers" or not (worker_dir / "memory.events").is_file():
        raise SystemExit("worker cgroup must be an existing Orva-owned orva.workers group")
    health_status, health = request(args.endpoint, key, "GET", "/api/v1/system/health")
    if health_status != 200 or health.get("sandbox", {}).get("resource_limits") != "cgroup_v2":
        raise SystemExit("Orva does not report cgroup-v2 resource enforcement")

    if "memory" in args.checks:
        handler = """exports.handler = async () => {
  const held = [];
  for (let i = 0; i < 512; i++) held.push(Buffer.alloc(1024 * 1024, 0xff));
  return {statusCode: 200, body: String(held.length)};
};"""
        print(json.dumps(check(args, key, worker_dir, "memory", 80, 1, handler,
                               "memory.events", "oom_kill", 502)))
    if "cpu" in args.checks:
        handler = """exports.handler = async () => {
  const start = process.cpuUsage();
  while (process.cpuUsage(start).user < 1000000) Math.sqrt(12345);
  return {statusCode: 200, body: 'cpu-done'};
};"""
        print(json.dumps(check(args, key, worker_dir, "cpu", 256, 0.25, handler,
                               "cpu.stat", "nr_throttled", 200)))
    if "pids" in args.checks:
        handler = """import ctypes
import errno
import json
import os
import platform
import time

def handler(event):
    libc = ctypes.CDLL(None, use_errno=True)
    clone_nr = {'x86_64': 56, 'aarch64': 220}.get(platform.machine())
    if clone_nr is None:
        raise RuntimeError('PID probe supports only Linux x86_64 and aarch64')
    children = []
    errors = {}
    try:
        for _ in range(64):
            # clone(SIGCHLD): fork-like task, no exec or extra pipes.
            pid = libc.syscall(clone_nr, 17, 0, 0, 0, 0)
            if pid == 0:
                time.sleep(3)
                os._exit(0)
            if pid < 0:
                name = errno.errorcode.get(ctypes.get_errno(), 'UNKNOWN')
                errors[name] = errors.get(name, 0) + 1
            else:
                children.append(pid)
        return {'statusCode': 200,
                'body': json.dumps({'failed': sum(errors.values()), 'errors': errors})}
    finally:
        for pid in children:
            os.waitpid(pid, 0)
"""
        print(json.dumps(check(args, key, worker_dir, "pids", 256, 1, handler,
                               "pids.events", "max", 200, runtime="python")))


if __name__ == "__main__":
    main()
