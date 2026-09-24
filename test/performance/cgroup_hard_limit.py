#!/usr/bin/env python3
"""Scratch-only proof that a real Orva function is killed by memory.max.

Run inside a delegated Linux test VM, never against an operator instance. The
script creates and deletes one function, but it intentionally crashes its
sandbox with a large allocation. A positive cgroup oom_kill delta is required;
an HTTP error alone could be a JavaScript or worker-protocol failure.
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
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as exc:
        try:
            body = json.loads(exc.read() or b"{}")
        except json.JSONDecodeError:
            body = {}
        return exc.code, body


def oom_kills(events):
    return int(dict(line.split() for line in events.read_text().splitlines()).get("oom_kill", "0"))


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--endpoint", default="http://127.0.0.1:8443")
    ap.add_argument("--key-file", default="/var/lib/orva/.admin-key")
    ap.add_argument("--worker-cgroup", required=True,
                    help="Orva-owned orva.workers cgroup path in a disposable VM")
    args = ap.parse_args()
    key = pathlib.Path(args.key_file).read_text().strip()
    worker_dir = pathlib.Path(args.worker_cgroup)
    if worker_dir.name != "orva.workers" or not (worker_dir / "memory.events").is_file():
        raise SystemExit("worker cgroup must be an existing Orva-owned orva.workers group")
    health_status, health = request(args.endpoint, key, "GET", "/api/v1/system/health")
    if health_status != 200 or health.get("sandbox", {}).get("resource_limits") != "cgroup_v2":
        raise SystemExit("Orva does not report cgroup-v2 resource enforcement")

    name = "cgroup-hard-limit-" + uuid.uuid4().hex[:10]
    code, created = request(args.endpoint, key, "POST", "/api/v1/functions", {
        "name": name, "runtime": "node", "entrypoint": "handler.js",
        "timeout_ms": 15000, "memory_mb": 80, "cpus": 1,
        "network_mode": "none", "auth_mode": "none",
    })
    if code not in (200, 201) or not created.get("id"):
        raise SystemExit(f"create failed: HTTP {code} {created}")
    fid = created["id"]
    try:
        handler = """exports.handler = async () => {
  const held = [];
  for (let i = 0; i < 512; i++) held.push(Buffer.alloc(1024 * 1024, 0xff));
  return {statusCode: 200, body: String(held.length)};
};"""
        code, deployed = request(args.endpoint, key, "POST", f"/api/v1/functions/{fid}/deploy-inline",
                                 {"code": handler, "filename": "handler.js"})
        if code not in (200, 202):
            raise RuntimeError(f"deploy failed: HTTP {code} {deployed}")
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            code, function = request(args.endpoint, key, "GET", f"/api/v1/functions/{fid}")
            if code == 200 and function.get("status") in ("active", "error"):
                break
            time.sleep(0.5)
        if code != 200 or function.get("status") != "active":
            raise RuntimeError(f"function not active: {function}")

        before = oom_kills(worker_dir / "memory.events")
        code, response = request(args.endpoint, key, "POST", f"/fn/{fid}", {})
        deadline = time.monotonic() + 5
        after = oom_kills(worker_dir / "memory.events")
        while after == before and time.monotonic() < deadline:
            time.sleep(0.05)
            after = oom_kills(worker_dir / "memory.events")
        if code == 200 or after <= before:
            raise RuntimeError(f"hard cap not proved: HTTP {code}, oom_kill {before}->{after}, body={response}")
        print(json.dumps({"result": "pass", "http_status": code,
                          "oom_kill_before": before, "oom_kill_after": after}))
    finally:
        # Let accepted completion writes drain before the function FK is gone.
        time.sleep(1)
        request(args.endpoint, key, "DELETE", f"/api/v1/functions/{fid}")


if __name__ == "__main__":
    main()
