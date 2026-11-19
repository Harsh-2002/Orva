"""Correlate actual public HTTP, SQLite execution, SDK and policy-audit evidence."""
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
browser = json.loads((root / "browser-result.json").read_text())
events = json.loads((root / "events.json").read_text())
assert len(browser["journeys"]) == 2 and not browser["pageErrors"] and not browser["overflow"]
summary = []
for journey in browser["journeys"]:
    worker = journey["before"]["worker"]
    artifact = journey["before"]["artifact"]
    executions = []
    for stage, decision, sdk_code in [
        ("before", "IAM_DENIED", "IAM_DENIED"),
        ("allowed", "ALLOWED", "OK"),
        ("after", "IAM_DENIED", "IAM_DENIED"),
    ]:
        result = journey[stage]
        assert result["worker"] == worker and result["artifact"] == artifact
        execution = result["execution"]
        executions.append(execution)
        sdk = [e for e in events if e["kind"] == "sdk" and e["execution"] == execution]
        invokes = [e for e in events if e["kind"] == "invoke" and e["execution"] == execution]
        assert len(sdk) == len(invokes) == 1
        sdk, invoke = sdk[0], invokes[0]
        assert sdk["identity"] == invoke["worker"]
        assert sdk["identity"]["WorkerID"] == worker and sdk["identity"]["ArtifactRevision"] == artifact
        assert sdk["phase"] == "execution" and sdk["ready"] and sdk["alive"] and sdk["has_lease"]
        assert sdk["code"] == sdk_code and invoke["response"]["decision"] == decision
        row = invoke["record"]
        assert row["id"] == execution and row["function_id"] == sdk["identity"]["FunctionID"]
        assert row["trigger"] == "http" and row["status"] == "success" and row["status_code"] == 200
        assert result["response"]["configOK"] and result["response"]["secretOK"] and result["response"]["credentialAbsent"]
        assert result["response"]["method"] == "POST" and result["response"]["path"] == "/demo"
        assert json.loads(result["response"]["body"]) == {"marker": "kv-workflow"}
    assert len(set(executions)) == 3
    for snapshot in [journey["savedSnapshot"], journey["revokedSnapshot"]]:
        assert snapshot["host"]["currentArtifact"] == artifact
        revision = snapshot["host"]["revision"]
        assert isinstance(revision, str)
        assert any(row["revision"] == revision and row["actor"]["kind"] == "api_key" for row in snapshot["history"])
    summary.append({"runtime": journey["runtime"], "worker": worker, "artifact": artifact, "executions": executions})
(root / "verified-correlation.json").write_text(json.dumps(summary, indent=2))
print("PASS: both public HTTP/header/SQLite executions match exact private SDK worker/artifact/lease observations and policy audit")
