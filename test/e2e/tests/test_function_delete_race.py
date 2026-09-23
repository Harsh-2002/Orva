#!/usr/bin/env python3
"""Deleting an invoked function must not turn its final record into an FK failure."""

import concurrent.futures
import os
import sys
import time

from harness import OrvaClient, section, check, summary, skip

NAME = "e2e-function-delete-race"
REQUIRE_SANDBOX = os.environ.get("ORVA_REQUIRE_SANDBOX", "") in ("1", "true", "yes")
HANDLER = """exports.handler = async () => {
  await new Promise(resolve => setTimeout(resolve, 2000));
  return {statusCode: 200, body: 'done'};
};
"""


def wait_until(predicate, timeout=15):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if predicate():
            return True
        time.sleep(0.02)
    return predicate()


def cleanup(c):
    rows = c.get("/api/v1/functions?limit=10000") or {}
    for fn in rows.get("functions") or []:
        if fn.get("name") == NAME:
            c.req("DELETE", f"/api/v1/functions/{fn['id']}", expect=(200, 204, 404))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    fid = None
    try:
        section("deploy slow handler")
        status, fn = c.req("POST", "/api/v1/functions", {
            "name": NAME, "runtime": "node", "entrypoint": "handler.js",
            "timeout_ms": 5000, "memory_mb": 64, "cpus": 0.25,
            "network_mode": "none", "auth_mode": "none",
        }, expect=range(200, 599))
        fid = fn.get("id") if isinstance(fn, dict) else None
        check("function created", 200 <= status < 300 and bool(fid))
        if not fid:
            return summary()
        status, _ = c.req("POST", f"/api/v1/functions/{fid}/deploy-inline", {
            "code": HANDLER, "filename": "handler.js",
        }, expect=range(200, 599))
        check("deployment accepted", status in (200, 202))
        ready = wait_until(lambda: c.get(f"/api/v1/functions/{fid}").get("status") == "active", 60)
        if not ready:
            if REQUIRE_SANDBOX:
                check("sandbox deployment required", False)
                return summary()
            return skip("sandbox deployment unavailable")

        section("delete during invocation")
        before = c.get("/api/v1/system/health")["writer"]
        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as executor:
            future = executor.submit(c.req, "GET", f"/fn/{fid}", None, range(200, 599))
            def worker_busy():
                pools = c.get("/api/v1/system/metrics.json").get("pools") or []
                return any(p.get("function_id") == fid and p.get("busy", 0) > 0 for p in pools)

            started = wait_until(worker_busy)
            check("invocation acquired a worker", started and not future.done())
            if started and not future.done():
                status, _ = c.req("DELETE", f"/api/v1/functions/{fid}", expect=range(200, 599))
                check("delete succeeded while invoked", status in (200, 204))
                fid = None
            response, _ = future.result(timeout=20)
            check("in-flight response completed", response == 200, f"status {response}")

        def writer_drained():
            active = c.get("/api/v1/system/metrics.json")["active_requests"]
            writer = c.get("/api/v1/system/health")["writer"]
            return active == 0 and all(writer[k] == 0 for k in (
                "critical_queue_bytes", "activity_queue_bytes", "telemetry_queue_bytes"))

        check("writer drained", wait_until(writer_drained))
        after = c.get("/api/v1/system/health")["writer"]
        check("no critical FK write failure", after["critical_failures"] == before["critical_failures"])
        check("deleted execution writes counted", after["deleted_function_writes"] > before["deleted_function_writes"])
    finally:
        if fid:
            c.req("DELETE", f"/api/v1/functions/{fid}", expect=(200, 204, 404))
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
