//go:build linux

package workloadspawn_test

import (
	"strings"
	"testing"
)

// The authored fixture preserves KV as the default and exposes buffered calls
// only. Streaming remains deliberately unavailable until independently exercised.
func liveCallsSource(lang string) (string, string) {
	entry, kvSource := liveSource(lang)
	if lang == "node" {
		return entry, strings.Replace(kvSource, "const {kv} = require('orva');", `const {kv,invoke} = require('orva');`, 1) + `
const kvHandler=exports.handler;
exports.handler=async event=>{
 const input=JSON.parse(event.body||'{}')||{};
 const h=event.headers||{};
 const context={execution:h['x-orva-execution-id'],functionID:h['x-orva-function-id'],trace:h['x-orva-trace-id'],span:h['x-orva-span-id'],depth:h['x-orva-call-depth'],trigger:h['x-orva-trigger'],credentialAbsent:!h['x-orva-api-key']&&!h.authorization&&!h.cookie};
 if(input.mode==='echo') return {statusCode:200,body:JSON.stringify({decision:'ECHO',marker:input.marker,context})};
 if(input.mode==='call') {
   try {const reply=await invoke(input.target,{mode:'echo',marker:'calls-workflow',depth:9999,trace:'author-not-authority'});return {statusCode:200,body:JSON.stringify({decision:'ALLOWED',callee:reply.body,context})};}
   catch(e){return {statusCode:200,body:JSON.stringify({decision:e.status===403?'IAM_DENIED':'CALL_ERROR',status:e.status,context})};}
 }
 if(input.mode && input.mode!=='kv') return {statusCode:400,body:JSON.stringify({decision:'MODE_UNAVAILABLE'})};
 return kvHandler(event);
};`
	}
	return entry, kvSource + `
from orva import invoke
kv_handler=handler
def handler(event):
    input=json.loads(event.get("body") or "{}") or {}
    h=event.get("headers") or {}
    context={"execution":h.get("x-orva-execution-id"),"functionID":h.get("x-orva-function-id"),"trace":h.get("x-orva-trace-id"),"span":h.get("x-orva-span-id"),"depth":h.get("x-orva-call-depth"),"trigger":h.get("x-orva-trigger"),"credentialAbsent":not any(k in h for k in ("x-orva-api-key","authorization","cookie"))}
    if input.get("mode")=="echo":
        return {"statusCode":200,"body":json.dumps({"decision":"ECHO","marker":input.get("marker"),"context":context})}
    if input.get("mode")=="call":
        try:
            reply=invoke(input["target"],{"mode":"echo","marker":"calls-workflow","depth":9999,"trace":"author-not-authority"})
            return {"statusCode":200,"body":json.dumps({"decision":"ALLOWED","callee":reply["body"],"context":context})}
        except Exception as e:
            return {"statusCode":200,"body":json.dumps({"decision":"IAM_DENIED" if getattr(e,"status",0)==403 else "CALL_ERROR","status":getattr(e,"status",0),"context":context})}
    if input.get("mode") not in (None,"kv"):
        return {"statusCode":400,"body":json.dumps({"decision":"MODE_UNAVAILABLE"})}
    return kv_handler(event)
`
}

func liveCallsChecks(t *testing.T, call func(string, string, string, any, int) map[string]any, keys, owners map[string]string) {
	t.Helper()
	set := func(id string, allow, inbound []any) map[string]any {
		p := "/api/v1/functions/" + id + "/permissions"
		s := call("GET", p, keys["admin"], nil, 200)
		h := s["host"].(map[string]any)
		return call("PUT", p, keys["admin"], map[string]any{
			"expected": map[string]any{"revision": h["revision"], "current_artifact": h["currentArtifact"], "approved_artifact": h["approvedArtifact"]},
			"policy":   map[string]any{"schema_version": 1, "allow": allow, "inbound": inbound}}, 200)
	}
	for _, pair := range [][2]string{{"node", "python"}, {"python", "node"}} {
		caller, callee := owners[pair[0]], owners[pair[1]]
		empty := []any{}
		out := []any{map[string]any{"actions": []string{"functions.invoke"}, "context": "execution", "resource": map[string]any{"function": map[string]string{"id": callee}}}}
		in := []any{map[string]any{"caller_id": caller, "triggers": []string{"f2f"}}}
		run := func(want string) map[string]any {
			t.Helper()
			result := call("POST", "/fn/"+caller+"/demo", keys["admin"], map[string]string{"mode": "call", "target": "scratch-" + pair[1]}, 200)
			if result["decision"] != want {
				t.Fatalf("actual %s->%s got %v", pair[0], pair[1], result)
			}
			return result
		}
		set(caller, out, empty)
		set(callee, empty, empty)
		run("IAM_DENIED")
		set(caller, empty, empty)
		set(callee, empty, in)
		run("IAM_DENIED")
		set(caller, out, empty)
		result := run("ALLOWED")
		child := result["callee"].(map[string]any)
		c := child["context"].(map[string]any)
		parent := result["context"].(map[string]any)
		if child["decision"] != "ECHO" || child["marker"] != "calls-workflow" || c["functionID"] != callee ||
			c["depth"] != "1" || c["trigger"] != "f2f" || c["credentialAbsent"] != true ||
			c["trace"] != parent["trace"] || c["trace"] == "" || c["trace"] == "author-not-authority" || c["execution"] == parent["execution"] {
			t.Fatal("actual pooled F2F context did not come from host dispatch")
		}
		set(caller, empty, empty)
		run("IAM_DENIED")
		set(caller, out, empty)
		set(callee, empty, empty)
		run("IAM_DENIED")
		hidden := call("POST", "/fn/"+caller+"/demo", keys["admin"], map[string]string{"mode": "call", "target": "not-visible-or-existing"}, 200)
		if hidden["decision"] != "IAM_DENIED" || hidden["status"] != float64(403) {
			t.Fatal("hidden target did not deny generically")
		}
		set(caller, empty, empty)
		set(callee, empty, empty)
		t.Logf("PASS actual %s->%s: outbound-only/inbound-only deny; both allow; either removal denies; host execution/depth/trace; hidden target generic", pair[0], pair[1])
	}
}
