"""Verify real stream browser results against server-recorded evidence.

Usage: python3 -I verify-streams-evidence.py <evidence-dir>
Reads streams-browser-result.json and events.json. Chunks already handed off
before a revocation was acknowledged are admitted data; only fresh frame handoffs
that began after the acknowledgement would be a bypass.
"""
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
browser = json.loads((root / "streams-browser-result.json").read_text())
events = json.loads((root / "events.json").read_text())
assert len(browser["journeys"]) == 2 and browser["streamEnabled"] is True
assert not browser["pageErrors"] and not browser["overflow"]

artifacts = {e["owner"]: e["artifact"] for e in events if e["kind"] == "factory"}
streams = {e["execution"]: e for e in events if e["kind"] == "stream"}
acks = [e for e in events if e["kind"] == "policy_ack" and e["status"] == 200]
opens = [e for e in events if e["kind"] == "sdk" and e["op"] == "invoke.stream.open"]


def stream_for(head):
    assert head["status"] == 200 and head["execution"] in streams, "stream not recorded by the server"
    ev = streams[head["execution"]]
    assert ev["worker"]["WorkerID"] == head["worker"] and ev["worker"]["ArtifactRevision"] == head["artifact"]
    return ev


def texts(ev):
    return [l["text"] for l in ev["lines"]]


def chunks(ev):
    return [l for l in ev["lines"] if l["text"].startswith("chunk-")]


def count_of(ui):
    return int(ui["count"].split(" of ")[0])


def child_rows(ev, callee):
    return [r for r in ev["children"] if r["function_id"] == callee]


def audit(snapshot, owner):
    host = snapshot["host"]
    assert snapshot["function"]["id"] == owner and host["currentArtifact"] == artifacts[owner]
    assert any(a["revision"] == host["revision"] and a["actor"]["kind"] == "api_key" for a in snapshot["history"])
    assert all(t["state"] == "resolved" and t["id"] in artifacts for t in snapshot["targets"])
    return int(host["revision"])


def denied(result, caller, callee, label):
    assert result["final"]["phase"] == "Denied", label
    assert result["final"]["count"].startswith("0 of") and not result["final"]["calleeShown"], label
    ev = stream_for(result["head"])
    assert texts(ev) == ["error:IAM_DENIED"], f"{label}: {texts(ev)}"
    assert not child_rows(ev, callee), f"{label}: callee executed"
    assert ev["record"]["function_id"] == caller and ev["record"]["trigger"] == "http"
    assert not any(t.startswith("{") and "callee" in t for t in texts(ev))
    return ev


def allowed(result, caller, callee, label):
    assert result["final"]["phase"] == "Completed" and result["final"]["count"] == "5 of 5 chunks received", label
    assert result["final"]["calleeShown"], label
    ev = stream_for(result["head"])
    lines = texts(ev)
    assert len(chunks(ev)) == 5 and lines[-1] == "end" and not any(t.startswith("error:") for t in lines), label
    parent = json.loads(next(t for t in lines if t.startswith("{") and '"caller"' in t))["context"]
    child = json.loads(next(t for t in lines if t.startswith("{") and '"callee"' in t))["context"]
    assert child["functionID"] == callee and child["depth"] == "1" and child["trigger"] == "f2f" and child["credentialAbsent"] is True
    assert child["trace"] == parent["trace"] != "author-not-authority" and child["execution"] != parent["execution"]
    assert parent["execution"] == result["head"]["execution"] and ev["record"]["trace_id"] == parent["trace"]
    rows = child_rows(ev, callee)
    assert len(rows) == 1 and rows[0]["id"] == child["execution"] and rows[0]["trigger"] == "f2f"
    assert rows[0]["parent_function_id"] == caller and rows[0]["status"] == "success" and rows[0]["trace_id"] == parent["trace"]
    # The SDK open was admitted against the acquired callee worker of the current artifact.
    op = [o for o in opens if o["execution"] == result["head"]["execution"]]
    assert len(op) == 1 and op[0]["code"] == "OK" and op[0]["phase"] == "execution" and op[0]["has_lease"] and op[0]["has_deadline"]
    target = next(t for t in op[0]["acquired_targets"] if t["owner"] == callee)
    assert target["identity"]["ArtifactRevision"] == artifacts[callee] and target["ready"]
    return ev, target["identity"]["WorkerID"]


def cut(result, caller, callee, revoked, label):
    assert result["before"]["phase"] == "Streaming" and count_of(result["before"]) >= 3, label
    assert result["final"]["phase"] == "Denied" and count_of(result["final"]) < 40, label
    assert "stays listed" in result["final"]["note"] or "stay listed" in result["final"]["note"], label
    ev = stream_for(result["head"])
    lines = texts(ev)
    assert lines[-1] == "error:IAM_DENIED" and lines.count("error:IAM_DENIED") == 1 and len(chunks(ev)) < 40, label
    start = ev["lines"][0]["at_ns"]
    ack = min(a["acked_ns"] for a in acks if a["owner"] == revoked and a["acked_ns"] > start)
    after = [l for l in chunks(ev) if l["at_ns"] > ack]
    assert len(after) <= 2, f"{label}: {len(after)} chunks after acknowledgement"
    assert all(a <= ack for a in ev["next_ok_at"]), f"{label}: a fresh handoff began after acknowledged revocation"
    # What the UI had received at acknowledgement versus the end stays consistent with the server.
    assert count_of(result["atAck"]) <= count_of(result["final"]) <= len(chunks(ev))
    rows = child_rows(ev, callee)
    assert len(rows) == 1 and rows[0]["status"] == "error" and "cancel" in rows[0]["error_message"].lower(), f"{label}: {rows}"
    assert rows[0]["parent_function_id"] == caller and rows[0]["trigger"] == "f2f"
    audit(result["saved"], revoked)
    return {"label": label, "chunks_total": len(chunks(ev)), "chunks_after_ack": len(after),
            "fresh_handoffs_after_ack": 0, "ui_at_ack": count_of(result["atAck"]), "ui_final": count_of(result["final"])}


proof = []
for j in browser["journeys"]:
    caller, callee = j["caller"]["id"], j["callee"]["id"]
    tag = f'{j["caller"]["runtime"]}->{j["callee"]["runtime"]}'
    denied(j["outboundOnly"], caller, callee, tag + " outbound-only")
    denied(j["inboundOnly"], caller, callee, tag + " inbound-only")
    ev, first_worker = allowed(j["both"], caller, callee, tag + " both")
    # Stop (keyboard) disconnects the client; the callee must be cancelled, not left running.
    s = j["stopped"]
    assert s["atStop"]["phase"] == "Streaming" and s["final"]["phase"] == "Stopped", tag
    sev = stream_for(s["head"])
    assert "end" not in texts(sev) and len(chunks(sev)) < 40 and not any(t.startswith("error:IAM") for t in texts(sev))
    stopped_rows = child_rows(sev, callee)
    assert len(stopped_rows) == 1 and stopped_rows[0]["status"] == "error", f"{tag}: callee not cancelled after Stop: {stopped_rows}"
    cuts = [cut(j["callerCut"], caller, callee, caller, tag + " caller revoked"),
            cut(j["calleeCut"], caller, callee, callee, tag + " callee acceptance revoked")]
    ev2, second_worker = allowed(j["restored"], caller, callee, tag + " restored")
    assert second_worker != first_worker, "callee worker was not replaced after its lease was cancelled"
    revisions = {}
    for key in ["outboundOnlySave", "outboundRemovedSave", "inboundOnlySave", "bothSave", "regrantSave"]:
        owner = j[key]["function"]["id"]
        revision = audit(j[key], owner)
        assert revision > revisions.get(owner, 0)
        revisions[owner] = revision
    rule = j["bothSave"]["policy"]["allow"][0]
    assert rule == {"actions": ["functions.invoke"], "context": "execution", "resource": {"function": {"id": callee}}}
    assert j["inboundOnlySave"]["policy"]["inbound"] == [{"caller_id": caller, "triggers": ["f2f"]}]
    proof.append({"direction": tag, "cuts": cuts, "stop_chunks": len(chunks(sev)),
                  "callee_workers_replaced": True, "audit_revisions": revisions})
(root / "verified-streams-correlation.json").write_text(json.dumps(proof, indent=2))
print("PASS: both cross-runtime stream UI journeys match server stream, SDK, acquired-target, F2F row, ack and audit evidence")
