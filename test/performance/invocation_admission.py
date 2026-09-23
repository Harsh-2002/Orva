#!/usr/bin/env python3
"""Scratch-instance invocation benchmark; creates and removes only its own functions.

Run against an isolated Orva instance, never production. No external packages.
Example: python3 test/performance/invocation_admission.py --scratch --url http://127.0.0.1:18443 --api-key "$KEY"
"""

import argparse
import collections
import concurrent.futures
import http.client
import json
import os
import statistics
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


def api(base, key, method, path, payload=None):
    data = json.dumps(payload).encode() if payload is not None else None
    request = urllib.request.Request(
        base + path, data=data, method=method,
        headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def deploy(base, key, runtime, owned, code_override=None):
    suffix = uuid.uuid4().hex[:12]
    fn = api(base, key, "POST", "/api/v1/functions", {
        "name": f"admission-test-{runtime}-{suffix}", "runtime": runtime,
        "entrypoint": "handler.js" if runtime == "node" else "handler.py",
        "timeout_ms": 5000, "memory_mb": 64, "cpus": 0.25,
        "network_mode": "none", "auth_mode": "none",
    })
    fid = fn["id"]
    owned.append(fid)  # Own it even if the deploy call or readiness poll fails.
    code = code_override or (
        'exports.handler = async () => ({statusCode: 200, body: "ok"});'
        if runtime == "node" else
        'def handler(event):\n    return {"statusCode": 200, "body": "ok"}\n'
    )
    api(base, key, "POST", f"/api/v1/functions/{fid}/deploy-inline", {
        "code": code, "filename": "handler.js" if runtime == "node" else "handler.py",
    })
    for _ in range(120):
        status = api(base, key, "GET", f"/api/v1/functions/{fid}")["status"]
        if status == "active":
            return fid
        if status == "error":
            raise RuntimeError(f"{runtime} deployment failed")
        time.sleep(0.5)
    raise TimeoutError(f"{runtime} deployment did not become active")


def run_load(base, fid, count, concurrency):
    parsed = urllib.parse.urlsplit(base)
    path = f"/fn/{fid}/"
    per_worker = [count // concurrency] * concurrency
    for i in range(count % concurrency):
        per_worker[i] += 1

    def client(requests):
        statuses = collections.Counter()
        durations = []
        connection = None
        for _ in range(requests):
            started = time.monotonic()
            try:
                if connection is None:
                    kind = http.client.HTTPSConnection if parsed.scheme == "https" else http.client.HTTPConnection
                    connection = kind(parsed.hostname, parsed.port, timeout=15)
                connection.request("GET", path)
                response = connection.getresponse()
                body = response.read()
                statuses[response.status] += 1
                if response.status == 429:
                    try:
                        error_code = json.loads(body)["error"]["code"]
                    except (ValueError, KeyError, TypeError):
                        error_code = ""
                    if error_code != "INVOCATION_QUEUE_FULL" or response.getheader("Retry-After") != "1":
                        statuses["bad_429_contract"] += 1
            except (OSError, http.client.HTTPException):
                statuses["transport_error"] += 1
                if connection is not None:
                    connection.close()
                connection = None
            durations.append(time.monotonic() - started)
        if connection is not None:
            connection.close()
        return statuses, durations

    started = time.monotonic()
    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as executor:
        parts = list(executor.map(client, per_worker))
    elapsed = time.monotonic() - started
    statuses = sum((part[0] for part in parts), collections.Counter())
    durations = sorted(d for part in parts for d in part[1])
    p50 = statistics.median(durations)
    p95 = durations[int(0.95 * (len(durations) - 1))]
    p99 = durations[int(0.99 * (len(durations) - 1))]
    return elapsed, statuses, p50, p95, p99


def wait_writer_drain(base, key, timeout=30):
    """Wait for accepted writes, including in-flight batches, before cleanup."""
    deadline = time.monotonic() + timeout
    while True:
        # A client can finish reading before InvokeHandler enqueues its final
        # execution row. First wait for handler completion, then for the
        # writer's queued/in-flight bytes to reach zero.
        active = api(base, key, "GET", "/api/v1/system/metrics.json")["active_requests"]
        writer = api(base, key, "GET", "/api/v1/system/health")["writer"]
        if (active == 0 and writer["critical_queue_bytes"] == 0 and
                writer["activity_queue_bytes"] == 0 and
                writer["telemetry_queue_bytes"] == 0):
            time.sleep(0.1)
            active = api(base, key, "GET", "/api/v1/system/metrics.json")["active_requests"]
            writer = api(base, key, "GET", "/api/v1/system/health")["writer"]
            if (active == 0 and writer["critical_queue_bytes"] == 0 and
                    writer["activity_queue_bytes"] == 0 and
                    writer["telemetry_queue_bytes"] == 0):
                return writer
        if time.monotonic() >= deadline:
            raise TimeoutError(f"invocations/writer did not drain before scratch cleanup: "
                               f"active={active}, writer={writer}")
        time.sleep(0.05)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", required=True)
    parser.add_argument("--api-key", default=os.environ.get("ORVA_API_KEY"), required=False)
    parser.add_argument("--scratch", action="store_true",
                        help="confirm the target is a disposable test instance")
    parser.add_argument("--requests", type=int, default=5000)
    parser.add_argument("--concurrency", type=int, default=100)
    parser.add_argument("--extended", action="store_true",
                        help="also exercise mixed functions, CPU work, and bounded overload")
    args = parser.parse_args()
    if not args.api_key:
        parser.error("--api-key or ORVA_API_KEY is required")
    if not args.scratch:
        parser.error("this is a load test; pass --scratch only for a disposable instance")
    if args.requests <= 0 or args.concurrency <= 0:
        parser.error("requests and concurrency must be positive")
    base = args.url.rstrip("/")
    failures = 0
    functions = {}
    owned = []
    writer_start = api(base, args.api_key, "GET", "/api/v1/system/health")["writer"]
    try:
        for runtime in ("node", "python"):
            fid = deploy(base, args.api_key, runtime, owned)
            functions[runtime] = fid
            warm = run_load(base, fid, 100, 10)
            print(f"{runtime} warm-up: {dict(warm[1])}", flush=True)
            elapsed, statuses, p50, p95, p99 = run_load(
                base, fid, args.requests, args.concurrency,
            )
            print(f"{runtime}: {args.requests} requests, {args.concurrency} clients, "
                  f"{elapsed:.2f}s, {dict(statuses)}, "
                  f"p50={p50*1000:.1f}ms p95={p95*1000:.1f}ms p99={p99*1000:.1f}ms",
                  flush=True)
            if statuses[200] != args.requests or elapsed > 60:
                failures += 1
        if args.extended:
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
                node_run = executor.submit(run_load, base, functions["node"], 2500, 50)
                python_run = executor.submit(run_load, base, functions["python"], 2500, 50)
                for runtime, result in (("node", node_run), ("python", python_run)):
                    elapsed, statuses, p50, p95, p99 = result.result()
                    print(f"mixed {runtime}: {elapsed:.2f}s {dict(statuses)} "
                          f"p95={p95*1000:.1f}ms", flush=True)
                    if statuses[200] != 2500 or elapsed > 60:
                        failures += 1

            cpu_code = ('exports.handler = async () => {'
                        'const end = Date.now() + 20; while (Date.now() < end) {} '
                        'return {statusCode: 200, body: "ok"}; };')
            cpu_id = deploy(base, args.api_key, "node", owned, cpu_code)
            functions["cpu"] = cpu_id
            elapsed, statuses, *_ = run_load(base, cpu_id, 1000, 100)
            print(f"CPU-bound: {elapsed:.2f}s {dict(statuses)}", flush=True)
            if statuses[504] or statuses["transport_error"] or sum(statuses.values()) != 1000:
                failures += 1

            slow_code = ('exports.handler = async () => {'
                         'await new Promise(resolve => setTimeout(resolve, 1000)); '
                         'return {statusCode: 200, body: "ok"}; };')
            slow_id = deploy(base, args.api_key, "node", owned, slow_code)
            functions["slow"] = slow_id
            elapsed, statuses, *_ = run_load(base, slow_id, 500, 100)
            print(f"overload: {elapsed:.2f}s {dict(statuses)}", flush=True)
            if (statuses[504] or statuses["transport_error"] or
                    statuses["bad_429_contract"] or
                    statuses[200] + statuses[429] != 500):
                failures += 1
            if statuses[429] == 0:
                print("overload did not saturate admission; check test rig capacity", flush=True)
                failures += 1
    finally:
        # Deleting a function cascades its execution rows. Wait for accepted
        # completion/capture jobs before deletion so the load result cannot
        # mistake the harness's own cleanup race for a persistence failure.
        try:
            writer_before_delete = wait_writer_drain(base, args.api_key)
            failure_delta = (writer_before_delete["critical_failures"] -
                             writer_start["critical_failures"])
            telemetry_delta = (writer_before_delete["dropped_telemetry"] -
                               writer_start["dropped_telemetry"])
            activity_delta = (writer_before_delete["dropped_activity"] -
                              writer_start["dropped_activity"])
            deleted_delta = (writer_before_delete["deleted_function_writes"] -
                             writer_start["deleted_function_writes"])
            print(f"writer before cleanup: critical_failures={failure_delta} "
                  f"dropped_telemetry={telemetry_delta} "
                  f"dropped_activity={activity_delta} "
                  f"deleted_function_writes={deleted_delta}", flush=True)
            if failure_delta or deleted_delta:
                failures += 1
        except Exception as exc:
            print(f"writer drain check failed: {exc}", flush=True)
            failures += 1
        for fid in owned:
            for attempt in range(3):
                try:
                    api(base, args.api_key, "DELETE", f"/api/v1/functions/{fid}")
                    break
                except urllib.error.HTTPError as exc:
                    if exc.code == 404:
                        break
                    error = exc
                except Exception as exc:  # Cleanup must not hide the load failure.
                    error = exc
                if attempt < 2:
                    time.sleep(1)
            else:
                print(f"cleanup failed for scratch function {fid}: {error}", flush=True)
                failures += 1
    return failures != 0


if __name__ == "__main__":
    raise SystemExit(main())
