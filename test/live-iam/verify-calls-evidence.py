"""Verify real public parent/private-SDK/acquired-target/F2F/audit correlation."""
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
browser = json.loads((root / "calls-browser-result.json").read_text())
events = json.loads((root / "events.json").read_text())
assert len(browser["journeys"]) == 2 and browser["streamEnabled"] is False
assert not browser["pageErrors"] and not browser["overflow"]
artifacts = {e["owner"]: e["artifact"] for e in events if e["kind"] == "factory"}
proof = []
for journey in browser["journeys"]:
    caller, callee = journey["caller"]["id"], journey["callee"]["id"]
    rows = []
    for stage in ["outboundOnly", "inboundOnly", "both", "inboundRemoved", "restored", "callerRemoved"]:
        result = journey[stage]
        execution = result["execution"]
        allowed = stage in ("both", "restored")
        sdk = [e for e in events if e["kind"] == "sdk" and e["op"] == "invoke" and e["execution"] == execution]
        inv = [e for e in events if e["kind"] == "invoke" and e["execution"] == execution]
        trace = [e for e in events if e["kind"] == "trace" and e["execution"] == execution]
        assert len(sdk) == len(inv) == len(trace) == 1
        sdk, inv, trace = sdk[0], inv[0], trace[0]["rows"]
        assert sdk["code"] == ("OK" if allowed else "IAM_DENIED")
        assert sdk["known"] and sdk["ready"] and sdk["alive"] and sdk["has_lease"] and sdk["has_deadline"]
        assert sdk["phase"] == "execution" and sdk["depth"] == 0
        assert sdk["identity"] == inv["worker"]
        assert sdk["identity"]["FunctionID"] == caller and sdk["identity"]["WorkerID"] == result["worker"]
        assert result["artifact"] == artifacts[caller] == sdk["identity"]["ArtifactRevision"]
        parent = inv["record"]
        assert parent["id"] == execution and parent["function_id"] == caller and parent["trigger"] == "http"
        assert parent["status"] == "success" and parent["status_code"] == 200
        assert parent["trace_id"] == sdk["trace"] == result["response"]["context"]["trace"]
        assert result["response"]["context"]["credentialAbsent"]
        assert parent["span_id"] == sdk["span"]
        assert len(trace) == (2 if allowed else 1)
        row = {"stage": stage, "parent": execution, "trace": parent["trace_id"], "sdk_code": sdk["code"]}
        if allowed:
            echo = result["response"]["callee"]
            context = echo["context"]
            child = next(r for r in trace if r["id"] == context["execution"])
            assert child["function_id"] == callee and child["parent_function_id"] == caller and child["trigger"] == "f2f"
            assert child["trace_id"] == parent["trace_id"] and child["parent_span_id"] == parent["span_id"]
            assert child["span_id"] == context["span"] and child["status"] == "success" and child["status_code"] == 200
            assert context["depth"] == "1" and context["trigger"] == "f2f" and context["credentialAbsent"]
            assert context["trace"] != "author-not-authority" and echo["marker"] == "calls-workflow"
            target = next(t for t in sdk["acquired_targets"] if t["owner"] == callee)
            assert target["ready"] and target["alive"]
            assert target["identity"]["FunctionID"] == callee and target["identity"]["ArtifactRevision"] == artifacts[callee]
            assert json.loads(sdk["reply"]["body"]) == echo
            row["child"], row["callee_worker"], row["callee_artifact"] = child["id"], target["identity"]["WorkerID"], artifacts[callee]
        else:
            assert sdk["message"] == "SDK operation denied" and "callee" not in result["response"]
        rows.append(row)
    revisions = {}
    for key in ["outboundOnlySave", "outboundRemovedSave", "inboundOnlySave", "bothSave",
                "inboundRemovedSave", "restoredInboundSave", "callerRemovedSave"]:
        snapshot = journey[key]
        owner, host = snapshot["function"]["id"], snapshot["host"]
        assert isinstance(host["revision"], str) and host["currentArtifact"] == artifacts[owner]
        revision = int(host["revision"])
        assert revision > revisions.get(owner, 0)
        revisions[owner] = revision
        assert any(a["revision"] == host["revision"] and a["actor"]["kind"] == "api_key" for a in snapshot["history"])
        assert all(t["state"] == "resolved" and t["id"] in artifacts for t in snapshot["targets"])
    rule = journey["bothSave"]["policy"]["allow"][0]
    assert rule == {"actions": ["functions.invoke"], "context": "execution", "resource": {"function": {"id": callee}}}
    assert journey["inboundOnlySave"]["policy"]["inbound"] == [{"caller_id": caller, "triggers": ["f2f"]}]
    assert not journey["outboundRemovedSave"]["policy"]["allow"] and not journey["callerRemovedSave"]["policy"]["allow"]
    assert not journey["inboundRemovedSave"]["policy"]["inbound"]
    proof.append({"caller": caller, "callee": callee, "calls": rows, "audit_revisions": revisions})
(root / "verified-calls-correlation.json").write_text(json.dumps(proof, indent=2))
print("PASS: both cross-runtime UI journeys match real public parent/private SDK/acquired target/F2F trace rows and two-sided audit; no denied callee execution")
