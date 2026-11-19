#!/usr/bin/env python3
"""Real legacy -> managed enrollment acceptance; disposable offline VM only."""

import hashlib
import hmac
import io
import ipaddress
import json
import os
from pathlib import Path
import shutil
import signal
import sqlite3
import subprocess
import tarfile
import time
from urllib.error import HTTPError
from urllib.parse import urlencode
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
from orva import OrvaError, kv


def handler(event, context):
    if event["path"] == "/probe":
        try:
            kv.put("enrollment-check", "legacy")
            return {"statusCode": 200, "body": "kv-allowed"}
        except OrvaError as error:
            denied = error.status == 403 and "IAM_DENIED" in str(error)
            return {"statusCode": 200, "body": "IAM_DENIED" if denied else "uncategorized"}
    return {"statusCode": 200, "body": "python-current"}
"""
DEPENDENCIES = {
    "npm": ("node", "handler.js", {
        "handler.js": """const semver = require('semver');
exports.handler = () => ({statusCode: 200, body: semver.clean('v7.6.3')});
""",
        "package.json": '{"dependencies":{"semver":"7.6.3"}}',
    }, b"7.6.3", ""),
    "pip": ("python", "handler.py", {
        "handler.py": """import idna
def handler(event):
    return {"statusCode": 200, "body": idna.encode("bücher.de").decode()}
""",
        "requirements.txt": "idna==3.10\n",
    }, b"xn--bcher-kva.de", ""),
    "tsc": ("node", "handler.ts", {
        "handler.ts": """export function handler() {
  const body: string = 'tsc-built';
  return {statusCode: 200, body};
}
""",
        "package.json": '{"devDependencies":{"typescript":"5.9.3"}}',
        "tsconfig.json": '{"compilerOptions":{"target":"ES2022","module":"CommonJS","outDir":"dist","strict":true},"files":["handler.ts"]}',
    }, b"tsc-built", "dist/handler.js"),
}
EMPTY = '{"schema_version":1,"allow":[],"inbound":[]}'
ADDR = "http://127.0.0.1:18443"


def until(check, description, seconds=20):
    deadline = time.monotonic() + seconds
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
    dependency_cases = DEPENDENCIES if os.environ.get("ORVA_APPROVED_REGISTRIES") == "1" else {}

    def run(*args, mode="legacy", group=None, expect=0, log=None, host=None):
        argv = [binary, *args]
        if group:
            argv = ["/bin/sh", "-ec", 'echo "$$" > "$1/cgroup.procs"; shift; exec "$@"',
                    "sh", str(group), *argv]
        run_env = {**env, "ORVA_SERVING_MODE": mode, **(host or {})}
        if log:
            return subprocess.Popen(argv, env=run_env,
                                    stdout=log, stderr=log)
        result = subprocess.run(argv, env=run_env,
                                capture_output=True, timeout=180)
        assert result.returncode == expect, (
            f"{args[:2]} exited {result.returncode}, expected {expect}: " +
            result.stderr.decode(errors="replace")[:800]
        )
        return result

    def request(method, path, value=None, extra_headers=None, raw_body=None):
        headers = {"Content-Type": "application/json", **(extra_headers or {})}
        if key:
            headers["X-Orva-API-Key"] = key
        body = json.dumps(value).encode() if value is not None else raw_body
        try:
            response = urlopen(Request(ADDR + path, method=method, headers=headers, data=body), timeout=30)
        except HTTPError as exc:
            response = exc
        with response:
            return response.status, response.read()

    def api(method, path, value=None):
        code, raw = request(method, path, value)
        assert 200 <= code < 300, f"{method} {path}: HTTP {code}: {raw[:300]!r}"
        return json.loads(raw)

    def deploy(fn, entry, code, files=None, managed=False):
        if files is None:
            dep = api("POST", f"/api/v1/functions/{fn}/deploy-inline",
                      {"filename": entry, "code": code})["deployment_id"]
        else:
            archive = io.BytesIO()
            with tarfile.open(fileobj=archive, mode="w:gz", format=tarfile.GNU_FORMAT) as tar:
                for name, value in files.items():
                    raw = value.encode()
                    info = tarfile.TarInfo(name)
                    info.size, info.mode = len(raw), 0o644
                    tar.addfile(info, io.BytesIO(raw))
            boundary = "orva-enrollment-dependency-fixture"
            body = (f"--{boundary}\r\n"
                    'Content-Disposition: form-data; name="code"; filename="code.tar.gz"\r\n'
                    "Content-Type: application/gzip\r\n\r\n").encode()
            status, response = request("POST", f"/api/v1/functions/{fn}/deploy",
                                       extra_headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
                                       raw_body=body + archive.getvalue() + f"\r\n--{boundary}--\r\n".encode())
            assert status == 202, (status, response)
            dep = json.loads(response)["deployment_id"]

        def built():
            row = api("GET", f"/api/v1/deployments/{dep}")
            assert row["status"] != "failed", f"legacy deployment failed: {row.get('error_message')}"
            return row["status"] == "succeeded" and (managed or api("GET", f"/api/v1/functions/{fn}")["status"] == "active")

        until(built, "dependency deployment" if files else "legacy deployment", seconds=145 if files else 20)
        if managed:
            api("POST", f"/api/v1/deployments/{dep}/install")
        return dep

    def stop(proc):
        if proc.poll() is None:
            proc.send_signal(signal.SIGTERM)
        assert proc.wait(timeout=40) == 0, "daemon did not shut down cleanly"

    def boot(mode, group, log, host=None):
        proc = run("serve", mode=mode, group=group, log=log, host=host)
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
            if dependency_cases:
                dns = [words[1] for line in Path("/etc/resolv.conf").read_text().splitlines()
                       if len(words := line.split()) == 2 and words[0] == "nameserver"
                       and ipaddress.ip_address(words[1]).version == 4]
                assert dns, "VM IPv4 DNS is required"
                api("PUT", "/api/v1/firewall/dns", {"servers": dns, "search": "", "records": []})
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
            for name, (lang, entry, files, expected, run_entry) in dependency_cases.items():
                fn = api("POST", "/api/v1/functions", {"name": f"enroll-{name}", "runtime": lang,
                         "entrypoint": entry, "auth_mode": "none", "memory_mb": 256})["id"]
                deploy(fn, entry, None, files)
                assert request("GET", f"/fn/{fn}") == (200, expected), name
                assert (api("GET", f"/api/v1/functions/{fn}").get("run_entrypoint") or "") == run_entry, name
                owners[name] = fn
                old_bodies[name] = expected
                print(f"legacy {name}: deployed and invoked", flush=True)
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
                if lang in dependency_cases:
                    row = api("GET", f"/api/v1/functions/{fn}")
                    assert row["entrypoint"] == dependency_cases[lang][1], lang
                    assert (row.get("run_entrypoint") or "") == dependency_cases[lang][4], lang
                    print(f"enrolled {lang}: managed invocation returned the legacy result", flush=True)
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

    if dependency_cases:
        with (scratch / "managed-deploy.private.log").open("wb") as log:
            daemon = boot("managed", budget / "orva.daemon", log)
            try:
                for name, (lang, entry, files, expected, run_entry) in dependency_cases.items():
                    fn = api("POST", "/api/v1/functions", {"name": f"managed-{name}", "runtime": lang,
                             "entrypoint": entry})["id"]
                    deploy(fn, entry, None, files, managed=True)
                    assert request("GET", f"/fn/{fn}") == (200, expected), name
                    row = api("GET", f"/api/v1/functions/{fn}")
                    assert row["entrypoint"] == entry and (row.get("run_entrypoint") or "") == run_entry, name
                    assert api("GET", f"/api/v1/functions/{fn}/source")["code"] == files[entry], name
                    owners[f"managed-{name}"], old_bodies[f"managed-{name}"] = fn, expected
                    print(f"managed {name}: uploaded, built, installed and invoked", flush=True)
            finally:
                stop(daemon)
    if os.environ.get("ORVA_PORTABLE_VM_TEST") == "disposable-vm":
        portable(run, boot, stop, request, api, data, witness, budget, owners, old_bodies)


def portable(run, boot, stop, request, api, data, witness, budget, owners, old_bodies):
    scratch = data.parent
    b = scratch / "host-b"
    w = scratch / "witness-b" / "witness"
    g = budget.with_name(budget.name + "-host-b")
    g.mkdir()
    shutil.copytree(data / "rootfs", b / "rootfs", symlinks=True)
    host_b = {"ORVA_DATA_DIR": str(b), "ORVA_MANAGED_WITNESS_PATH": str(w),
              "ORVA_MANAGED_WORKLOAD_PARENT": str(g / "orva.workers")}
    run("setup", "--managed-witness", str(w), host=host_b)
    assert witness.read_bytes() != w.read_bytes()
    archive = scratch / "portable.tar.gz"
    node = owners["node"]
    base = f"/api/v1/functions/{node}"
    route = "/portable-managed/*"
    route_invoke = "/portable-managed/check"
    body = {"event": "portable"}

    def exposure(kind, key=None):
        params = {"kind": kind, "function_id": node}
        if key is not None:
            params["key"] = key
        return api("GET", "/api/v1/exposures?" + urlencode(params))

    def approval(fn):
        s = api("GET", f"/api/v1/functions/{fn}/permissions")["host"]
        return {k: s[k] for k in ("revision", "current_artifact", "approved_artifact")}

    with (scratch / "portable-a.private.log").open("wb") as log:
        daemon = boot("managed", budget / "orva.daemon", log)
        try:
            for fn in (owners["node"], owners["python"]):
                perms = f"/api/v1/functions/{fn}/permissions"
                api("PUT", perms, {"expected": approval(fn), "policy": {
                    "schema_version": 1, "allow": [{"actions": ["kv.put"],
                        "resource": {"own_kv": {"key": "enrollment-check"}}, "context": "execution"}], "inbound": []}})
                api("POST", perms + "/review", {"expected": approval(fn)})
                assert request("GET", f"/fn/{fn}/probe") == (200, b"kv-allowed")
            r = exposure("route", route)
            api("POST", "/api/v1/routes", {"path": route, "function_id": node, "methods": "GET",
                "version": r["version"], "expected": r["expected"]})
            assert request("GET", route_invoke) == (200, b"node-current")
            r = exposure("inbound")
            hook = api("POST", base + "/inbound-webhooks", {"name": "portable-hook", "version": r["version"],
                "expected": r["expected"]})
            hook_id = hook["inbound_webhook"]["id"]
            sig = hmac.new(hook["secret"].encode(), json.dumps(body).encode(), "sha256").hexdigest()
            webhook = f"/webhook/{hook_id}"
            sig_header = {hook["inbound_webhook"]["signature_header"]: sig}
            assert request("POST", webhook, body, sig_header) == (200, b"node-current")
            status, payload = request("GET", "/api/v1/backup")
            assert status == 200
            archive.write_bytes(payload)
        finally:
            stop(daemon)
    with tarfile.open(fileobj=io.BytesIO(archive.read_bytes()), mode="r:gz") as tar:
        manifest = json.load(tar.extractfile("manifest.json"))
        assert manifest["format_version"] == 2 and len(manifest["deployment_sources"]) == len(owners)
        assert set(tar.getnames()) == {"manifest.json", *manifest["files"]}
        assert all(name == "orva.db" or name in {"keys/master.key", "keys/admin.key"} or
                   (name.startswith("sources/") and name.endswith(".tar.gz")) for name in manifest["files"])
        for name in manifest["deployment_sources"].values():
            source = tar.extractfile(f"sources/{name}.tar.gz").read()
            with tarfile.open(fileobj=io.BytesIO(source), mode="r:gz") as authored:
                assert not any(p.startswith(("node_modules/", "dist/", "idna/")) or ".dist-info/" in p
                               for p in authored.getnames())
    print("A: authorized SDK write, live route and signed webhook; v2 archive lists only the allowlisted files", flush=True)

    before = tree_hash(b)
    run("managed", "restore", "--archive", str(archive), host=host_b)
    assert before == tree_hash(b)
    broken = scratch / "broken.tar.gz"
    broken.write_bytes(archive.read_bytes()[:-1])
    run("managed", "restore", "--archive", str(broken), "--confirm", host=host_b, expect=1)
    assert not (b / ".orva-portable-restore-intent").exists()
    run("managed", "restore", "--archive", str(archive), "--confirm", host=host_b, group=g)
    run("serve", mode="legacy", host=host_b, group=g / "orva.daemon", expect=78)

    def verify(group, logname, host=None):
        with (scratch / logname).open("wb") as log:
            daemon = boot("managed", group, log, host)
            try:
                for lang, fn in owners.items():
                    assert request("GET", f"/fn/{fn}") == (200, old_bodies[lang]), lang
                    if lang in {"node", "python"}:
                        assert request("GET", f"/fn/{fn}/probe") == (200, b"IAM_DENIED")
                    else:
                        print(f"{logname}: {lang} invoked", flush=True)
                assert request("GET", route_invoke)[0] == 404
                assert request("POST", webhook, body, sig_header)[0] == 404
            finally:
                stop(daemon)

    verify(g / "orva.daemon", "portable-b.private.log", host_b)
    with sqlite3.connect(f"file:{b}/managed/orva.db?mode=ro", uri=True) as db:
        rows = db.execute("SELECT quarantined,review_required,approved_artifact FROM workload_iam_policies").fetchall()
        assert len(rows) == len(owners) + 1 and all(row == (1, 1, "0" * 64) for row in rows)
        prior, next_owner, epoch = db.execute(
            "SELECT prior_owner,new_owner,restore_epoch FROM workload_portable_restore_audit").fetchone()
        assert prior == manifest["prior_owner"] and prior != next_owner and epoch == 1
        assert db.execute("SELECT owner FROM workload_owner_binding").fetchone() == (next_owner,)
    print("B: separate witness/Owner, unchanged Node/Python responses, deny-only SDK, review required, exposures unpublished", flush=True)

    run("managed", "restore", "--same-owner", "--archive", str(archive), "--confirm",
        mode="managed", group=budget / "orva.daemon")
    verify(budget / "orva.daemon", "portable-a-replay.private.log")
    with sqlite3.connect(f"file:{data}/managed/orva.db?mode=ro", uri=True) as db:
        assert db.execute("SELECT owner FROM workload_owner_binding").fetchone() == (manifest["prior_owner"],)
        assert db.execute("SELECT epoch FROM workload_recovery").fetchone() == (1,)
    print("A replay: same Owner and seals, same Node/Python responses, existing restore quarantine applied", flush=True)

    with (scratch / "portable-b-review.private.log").open("wb") as log:
        daemon = boot("managed", g / "orva.daemon", log, host_b)
        try:
            api("POST", base + "/permissions/review", {"expected": approval(node)})
            assert request("GET", f"/fn/{node}/probe") == (200, b"kv-allowed")
            assert request("GET", f"/fn/{owners['python']}/probe") == (200, b"IAM_DENIED")
            assert request("GET", route_invoke)[0] == 404
            assert request("POST", webhook, body, sig_header)[0] == 404
        finally:
            stop(daemon)
    print("B review: explicit Node review restored only its grant and did not republish either exposure", flush=True)


if __name__ == "__main__":
    main()
