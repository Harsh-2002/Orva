#!/usr/bin/env python3
"""Real legacy -> managed enrollment acceptance; disposable offline VM only."""

import hashlib
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen

NODE = """
const { kv } = require('orva');
exports.handler = async (event) => {
  if (event.path === '/probe') {
    try {
      await kv.put('enrollment-check', 'legacy');
      return {statusCode: 200, body: 'kv-allowed'};
    } catch (error) {
      return {statusCode: 200, body: error.status === 403 && error.message.includes('IAM_DENIED')
        ? 'IAM_DENIED' : 'uncategorized'};
    }
  }
  return {statusCode: 200, body: 'node-current'};
};
"""
PYTHON = """
def handler(event, context):
    return {"statusCode": 200, "body": "python-current"}
"""
EMPTY = '{"schema_version":1,"allow":[],"inbound":[]}'
ADDR = "http://127.0.0.1:18443"


def until(check, description):
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        try:
            if check():
                return
        except OSError:
            pass
        time.sleep(0.1)
    raise AssertionError(f"timed out: {description}")


def tree_hash(data):
    digest = hashlib.sha256()
    for entry in sorted(data.rglob("*")):
        info = entry.lstat()
        digest.update(f"{entry.relative_to(data)}:{info.st_mode}:{info.st_mtime_ns}\n".encode())
        if entry.is_symlink():
            digest.update(os.readlink(entry).encode())
        elif entry.is_file():
            with entry.open("rb") as f:
                digest.update(hashlib.file_digest(f, "sha256").digest())
    return digest.hexdigest()


def main():
    assert os.environ.get("ORVA_ENROLL_VM_TEST") == "disposable-vm"
    binary = str(Path(os.environ["ORVA_ENROLL_BINARY"]).resolve())
    budget = Path(os.environ["ORVA_ENROLL_BUDGET"])
    data = Path(os.environ["ORVA_ENROLL_DATA"]).resolve()
    scratch = data.parent
    witness = scratch / "witness-host" / "witness"
    env = {**os.environ, "ORVA_DATA_DIR": str(data), "ORVA_SERVING_MODE": "legacy",
           "ORVA_HOST": "127.0.0.1", "ORVA_PORT": "18443",
           "ORVA_MANAGED_WITNESS_PATH": str(witness),
           "ORVA_MANAGED_WORKLOAD_PARENT": str(budget / "orva.workers")}
    key = None

    def run(*args, mode="legacy", group=None, expect=0, log=None):
        argv = [binary, *args]
        if group:
            argv = ["/bin/sh", "-ec", 'echo "$$" > "$1/cgroup.procs"; shift; exec "$@"',
                    "sh", str(group), *argv]
        if log:
            return subprocess.Popen(argv, env={**env, "ORVA_SERVING_MODE": mode},
                                    stdout=log, stderr=log)
        result = subprocess.run(argv, env={**env, "ORVA_SERVING_MODE": mode},
                                capture_output=True, timeout=180)
        assert result.returncode == expect, (
            f"{args[:2]} exited {result.returncode}, expected {expect}: " +
            result.stderr.decode(errors="replace")[:800]
        )
        return result

    def request(method, path, value=None):
        headers = {"Content-Type": "application/json"}
        if key:
            headers["X-Orva-API-Key"] = key
        body = json.dumps(value).encode() if value is not None else None
        try:
            response = urlopen(Request(ADDR + path, method=method, headers=headers, data=body), timeout=8)
        except HTTPError as exc:
            response = exc
        with response:
            return response.status, response.read()

    def api(method, path, value=None):
        code, raw = request(method, path, value)
        assert 200 <= code < 300, f"{method} {path}: HTTP {code}: {raw[:300]!r}"
        return json.loads(raw)

    def deploy(fn, entry, code):
        dep = api("POST", f"/api/v1/functions/{fn}/deploy-inline",
                  {"filename": entry, "code": code})["deployment_id"]

        def built():
            row = api("GET", f"/api/v1/deployments/{dep}")
            assert row["status"] != "failed", f"legacy deployment failed: {row.get('error_message')}"
            return row["status"] == "succeeded" and api("GET", f"/api/v1/functions/{fn}")["status"] == "active"

        until(built, "legacy deployment")
        return dep

    def stop(proc):
        if proc.poll() is None:
            proc.send_signal(signal.SIGTERM)
        assert proc.wait(timeout=40) == 0, "daemon did not shut down cleanly"

    def boot(mode, group, log):
        proc = run("serve", mode=mode, group=group, log=log)
        try:
            until(lambda: request("GET", "/api/v1/auth/status")[0] == 200,
                  f"{mode} daemon listening")
            return proc
        except BaseException:
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=40)
            raise

    run("setup", "--managed-witness", str(witness))
    with (scratch / "legacy.private.log").open("wb") as log:
        daemon = boot("legacy", budget, log)
        try:
            key = (data / ".admin-key").read_text().strip()
            owners, old_bodies = {}, {}
            for lang, entry, code in (("node", "handler.js", NODE), ("python", "handler.py", PYTHON)):
                fn = api("POST", "/api/v1/functions", {"name": f"enroll-{lang}", "runtime": lang,
                         "entrypoint": entry, "sdk": lang == "node", "network_mode": "none",
                         "auth_mode": "none", "memory_mb": 256})["id"]
                owners[lang] = fn
                if lang == "node":
                    old = deploy(fn, entry, NODE.replace("node-current", "node-historical"))
                    old_hash = api("GET", f"/api/v1/functions/{fn}")["code_hash"]
                deploy(fn, entry, code)
                status, body = request("GET", f"/fn/{fn}")
                assert (status, body) == (200, f"{lang}-current".encode()), (status, body)
                old_bodies[lang] = body
            assert request("GET", f"/fn/{owners['node']}/probe") == (200, b"kv-allowed")
            excluded = api("POST", "/api/v1/functions", {"name": "enroll-draft", "runtime": "node",
                           "entrypoint": "handler.js", "auth_mode": "none"})["id"]
            run("managed", "enroll", expect=1)
        finally:
            stop(daemon)
    print("legacy Node and Python deployed and invoked; SDK write permitted", flush=True)

    plan = scratch / "plan.json"
    before = tree_hash(data)
    result = run("managed", "enroll", "--dry-run", "--plan", str(plan))
    assert result.stdout == plan.read_bytes() and tree_hash(data) == before
    report = json.loads(result.stdout)
    assert {row["id"] for row in report["functions"] if row["enrollable"]} == set(owners.values()), report
    assert {row["id"] for row in report["functions"] if not row["enrollable"]} == {excluded}, report
    refused = run("managed", "enroll", "--apply", "--plan", str(plan), "--confirm",
                  group=budget / "orva.daemon", expect=1)
    assert b"explicit --exclude" in refused.stderr and tree_hash(data) == before
    run("managed", "enroll", "--apply", "--plan", str(plan), "--confirm", "--exclude", excluded,
        group=budget / "orva.daemon")
    assert (data / "orva.db").stat().st_mode & 0o777 == 0o500
    assert (data / "orva.db").is_dir()
    archive, = data.glob("legacy-functions.enrolled-*")
    assert (archive / owners["node"] / "versions" / old_hash / "handler.js").is_file()
    run("serve", group=budget / "orva.daemon", expect=78)
    print("dry run left the entire legacy tree unchanged; apply preserved history; legacy serve exited 78", flush=True)

    with (scratch / "managed.private.log").open("wb") as log:
        daemon = boot("managed", budget / "orva.daemon", log)
        try:
            for lang, fn in owners.items():
                assert request("GET", f"/fn/{fn}") == (200, old_bodies[lang])
            assert request("GET", f"/fn/{owners['node']}/probe") == (200, b"IAM_DENIED")
            assert request("GET", f"/fn/{excluded}")[0] == 409
        finally:
            stop(daemon)
    with sqlite3.connect(f"file:{data}/managed/orva.db?mode=ro", uri=True) as db:
        assert db.execute("SELECT policy_json,review_required FROM workload_iam_policies WHERE function_id=?",
                          (owners["node"],)).fetchone() == (EMPTY, 1)
        assert db.execute("SELECT policy_json,review_required FROM workload_iam_policies WHERE function_id=?",
                          (owners["python"],)).fetchone() == (EMPTY, 0)
        assert db.execute("SELECT count(*) FROM workload_deployment_capture WHERE deployment_id=?",
                          (old,)).fetchone() == (0,)
        assert db.execute("SELECT count(*) FROM workload_enrollment_runtimes").fetchone() == (2,)
        assert db.execute("SELECT excluded FROM workload_enrollment_functions WHERE function_id=?",
                          (excluded,)).fetchone() == (1,)
    print("managed Node and Python returned the same results; protected SDK write denied; only legacy SDK opt-in requires review")


if __name__ == "__main__":
    main()
