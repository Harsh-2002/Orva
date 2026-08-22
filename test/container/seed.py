#!/usr/bin/env python3
"""Populate an instance so the browser suite sees rows, not empty states.

Every list view renders differently once it has data: the row controls only
exist when there is a row. A KV key button that measured 102x15 -- under the
44px touch floor -- went unnoticed through several full runs simply because no
run had ever put a key in the store, so the button never rendered to be
measured.

This seeds one of each resource. It is deliberately small: the point is to make
each list non-empty, not to simulate load.
"""
import json
import sys
import time
import urllib.error
import urllib.request

BASE = sys.argv[1].rstrip("/")
KEY = sys.argv[2]


def api(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    headers = {"X-Orva-API-Key": KEY}
    if data:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw) if raw else {}
        except Exception:
            return e.code, {"raw": raw.decode(errors="replace")}


def ok(status):
    # 409 means the resource is already there from a previous run, which is the
    # state this script exists to produce.
    return 200 <= status < 300 or status == 409


created = []


def note(what, status, detail=""):
    mark = "seeded " if ok(status) else "SKIPPED"
    created.append((what, ok(status)))
    print(f"  {mark} {what}" + (f" ({detail})" if detail and not ok(status) else ""))


# A function to hang everything else off.
st, fn = api("POST", "/api/v1/functions", {
    "name": "seed-fn", "runtime": "node", "entrypoint": "handler.js",
    "description": "Seeded so list views render rows.",
    "memory_mb": 128,
})
# Strict 2xx here: ok() tolerates 409 for the reporting below, but a 409 body
# carries no id, so the already-exists case has to go look the function up.
if not (200 <= st < 300):
    st, existing = api("GET", "/api/v1/functions")
    match = [f for f in (existing.get("functions") or []) if f["name"] == "seed-fn"]
    if not match:
        print("could not create or find seed-fn:", st, fn)
        sys.exit(2)
    fn = match[0]
FN = fn.get("id") or fn["function"]["id"]
print(f"function {FN}")

st, _ = api("POST", f"/api/v1/functions/{FN}/deploy-inline", {
    "code": "exports.handler = async () => ({ statusCode: 200, body: '{\"seeded\":true}' })",
    "filename": "handler.js",
})
note("deployment", st)

# Wait for the build so an invocation has something to run.
for _ in range(90):
    _, d = api("GET", f"/api/v1/functions/{FN}/deployments")
    deps = d.get("deployments") or []
    if deps and deps[0].get("status") in ("succeeded", "failed"):
        break
    time.sleep(1)

# KV: the store whose row button was never measured.
st, _ = api("PUT", f"/api/v1/functions/{FN}/kv/seeded-key", {"value": {"hello": "world"}})
note("kv entry", st)
st, _ = api("PUT", f"/api/v1/functions/{FN}/kv/seeded-string", {"value": "123"})
note("kv entry (type-preserving probe)", st)

st, _ = api("POST", "/api/v1/keys", {"name": "seeded-key", "permissions": ["read"]})
note("api key", st)

st, _ = api("POST", f"/api/v1/functions/{FN}/cron", {
    "cron_expr": "0 3 * * *", "enabled": True,
})
note("cron schedule", st)

st, _ = api("POST", "/api/v1/jobs", {"function_id": FN, "payload": {"seeded": True}})
note("job", st)

st, _ = api("POST", f"/api/v1/functions/{FN}/inbound-webhooks", {
    "name": "seeded-trigger", "signature_format": "stripe",
})
note("inbound webhook (stripe, a format the browser could not sign)", st)

st, _ = api("POST", "/api/v1/webhooks", {
    "name": "seeded-subscription",
    "url": "https://example.invalid/hook",
    "events": ["deployment.succeeded", "deployment.failed"],
})
note("outbound webhook", st)

st, _ = api("POST", "/api/v1/channels", {"name": "seeded-channel", "function_ids": [FN]})
note("channel", st)

st, _ = api("POST", "/api/v1/firewall/rules", {
    "rule_type": "cidr", "value": "203.0.113.0/24", "label": "seeded probe",
})
note("firewall rule", st)

# An invocation, so Invocations and Activity have rows and a trace exists.
try:
    req = urllib.request.Request(
        f"{BASE}/fn/{FN}", data=b"{}",
        headers={"X-Orva-API-Key": KEY, "Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(req, timeout=60) as r:
        note("invocation", r.status)
except urllib.error.HTTPError as e:
    note("invocation", e.code, f"HTTP {e.code}")

seeded = sum(1 for _, good in created if good)
print(f"{seeded}/{len(created)} seeded")
# Seeding is best-effort: a resource that will not create should not stop the
# browser suite from running against everything that did.
sys.exit(0)
