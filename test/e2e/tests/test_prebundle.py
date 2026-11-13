#!/usr/bin/env python3
"""Author-built artifacts through the existing deploy/source/diff/rollback paths.

Uses only committed bundles; never installs or executes a bundler on the server.
Sandbox failure is a failure (including ORVA_REQUIRE_SANDBOX=1), never a skip.
"""
import io
import json
from pathlib import Path
import sys
import tarfile
import time
import urllib.error
import urllib.request
import uuid

from harness import OrvaClient, check, section, summary

FIXTURE = Path(__file__).resolve().parents[2] / "fixtures" / "node-prebundle"
UNBUNDLED = b"exports.handler = async () => ({statusCode: 202, body: 'unbundled-B'});\n"


def upload(c, fid, files):
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode="w:gz") as tar:
        for path, data in sorted(files.items()):
            entry = tarfile.TarInfo(path)
            entry.size = len(data)
            tar.addfile(entry, io.BytesIO(data))
    boundary = "prebundle-" + uuid.uuid4().hex
    body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"code\"; "
            'filename="code.tar.gz"\r\nContent-Type: application/gzip\r\n\r\n').encode()
    body += archive.getvalue() + f"\r\n--{boundary}--\r\n".encode()
    headers = c._headers()
    headers["Content-Type"] = f"multipart/form-data; boundary={boundary}"
    request = urllib.request.Request(c.base + f"/api/v1/functions/{fid}/deploy",
                                     data=body, headers=headers, method="POST")
    with urllib.request.urlopen(request, timeout=60) as response:
        assert response.status == 202, response.status
        submitted = json.load(response)
    dep_id = submitted.get("deployment_id") or submitted.get("id")
    assert dep_id, submitted
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        deployment = c.get(f"/api/v1/deployments/{dep_id}")
        if deployment.get("status") in ("succeeded", "failed"):
            assert deployment["status"] == "succeeded", deployment
            check("specific deployment succeeded", deployment["id"] == dep_id)
            return deployment
        time.sleep(0.2)
    raise AssertionError(f"deployment {dep_id} never reached terminal status")


def source_matches(c, fid, code):
    source = c.get(f"/api/v1/functions/{fid}/source")
    check("source is the uploaded JavaScript bytes", source.get("code", "").encode() == code)
    fn = c.get(f"/api/v1/functions/{fid}")
    check("entrypoint remains dist/handler.js", fn.get("entrypoint") == "dist/handler.js")


def invoke_bundle(c, fid, language, name):
    status, body = c.req("POST", f"/fn/{fid}", {"name": name}, expect=range(100, 600))
    expected = {"revision": "bundle-A", "language": language,
                "message": "Hello " + name.rsplit("/", 1)[-1], "saved": name}
    check(f"{language} exact HTTP status and body, builtin and SDK roundtrip",
          status == 201 and body == expected, f"{status}: {body}")
    value = c.get(f"/api/v1/functions/{fid}/kv/prebundle:last")
    check("operator observes SDK KV write", value.get("value") == name, str(value))


def history(c, fid):
    rows = c.get(f"/api/v1/functions/{fid}/deployments")["deployments"]
    return sorted((row["id"], row["version"], row["code_hash"]) for row in rows)


def drain(c):
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        active = c.get("/api/v1/system/metrics.json")["active_requests"]
        writer = c.get("/api/v1/system/health")["writer"]
        if active == 0 and all(writer[k] == 0 for k in (
                "critical_queue_bytes", "activity_queue_bytes", "telemetry_queue_bytes")):
            return True
        time.sleep(0.1)
    return False


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    owned = []
    try:
        section("prebundled static JS and TS, no runtime dependency manifest")
        fn = c.post("/api/v1/functions", {
            "name": "e2e-prebundle-" + uuid.uuid4().hex[:12], "runtime": "node",
            "entrypoint": "dist/handler.js", "memory_mb": 128, "timeout_ms": 10000,
            "network_mode": "egress", "auth_mode": "none",
        })
        fid = fn["id"]
        owned.append(fid)
        bundles = {}
        deployments = {}
        for language in ("js", "ts"):
            root = FIXTURE / language / "upload"
            files = {p.relative_to(root).as_posix(): p.read_bytes()
                     for p in root.rglob("*") if p.is_file()}
            assert set(files) == {"dist/handler.js", "dist/handler.js.map"}, files.keys()
            bundles[language] = files["dist/handler.js"]
            deployments[language] = upload(c, fid, files)
            source_matches(c, fid, bundles[language])
            invoke_bundle(c, fid, language, "authors/Ada")
            invoke_bundle(c, fid, language, "authors/Grace")

        section("diff exposes uploaded bundles, not original TypeScript")
        diff = c.get(f"/api/v1/functions/{fid}/diff?from={deployments['js']['id']}"
                     f"&to={deployments['ts']['id']}&format=json")
        files = diff.get("files", [])
        check("diff contains only the uploaded entrypoint", len(files) == 1
              and files[0].get("path") == "dist/handler.js", str(files)[:200])
        if files:
            check("diff before/after are exact bundle bytes",
                  files[0].get("before", "").encode() == bundles["js"]
                  and files[0].get("after", "").encode() == bundles["ts"])

        section("bundle A -> unbundled B -> rollback A -> rollback B")
        b = upload(c, fid, {"dist/handler.js": UNBUNDLED})
        source_matches(c, fid, UNBUNDLED)
        status, body = c.req("GET", f"/fn/{fid}", expect=range(100, 600))
        check("unbundled B exact response", (status, body) == (202, "unbundled-B"))
        before = history(c, fid)
        check("three distinct versions retained", len(before) == 3
              and len({row[2] for row in before}) == 3)
        for label, deployment, code in (("A", deployments["ts"], bundles["ts"]),
                                         ("B", b, UNBUNDLED)):
            c.post(f"/api/v1/functions/{fid}/rollback", {"deployment_id": deployment["id"]})
            source_matches(c, fid, code)
            live = c.get(f"/api/v1/functions/{fid}")
            check(f"rollback {label} selects original code hash",
                  live.get("code_hash") == deployment["code_hash"])
            if label == "A":
                invoke_bundle(c, fid, "ts", "authors/Linus")
            else:
                status, body = c.req("GET", f"/fn/{fid}", expect=range(100, 600))
                check("rollback B exact response", (status, body) == (202, "unbundled-B"))
            check("rollback preserves version ids, numbers and hashes", history(c, fid) == before)
    except Exception as exc:
        check("prebundle journey completed", False, str(exc))
    finally:
        try:
            check("accepted invocation writes drain before cleanup", drain(c))
        except Exception as exc:
            check("writer drain observed", False, str(exc))
        for fid in owned:
            try:
                status, _ = c.req("DELETE", f"/api/v1/functions/{fid}", expect=(200, 204, 404))
                check("owned function cleaned up", status in (200, 204, 404))
            except Exception as exc:
                check("owned function cleanup", False, str(exc))
    return summary()


if __name__ == "__main__":
    sys.exit(main())
