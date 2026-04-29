import{C as L,c as q}from"./clipboard-BeIRJU-r.js";import{A as M}from"./activity-C1zNri6B.js";import{c as j,a as b,b as e,k as s,t as c,d as n,h as l,f as o,p as m,K as $,a1 as g,y as a,j as f,F as k,n as S,q as A,m as F,M as V,ac as E,g as G,r as R}from"./index-DPogQ5zG.js";import{V as X}from"./variable-BU1Z6aJO.js";import{K as z}from"./key-round-Csg_JiRj.js";import{G as I}from"./globe-CWpI5IiW.js";import{S as P}from"./shield-check-Bsoeze07.js";import{L as Y}from"./lock-BGYOPMnd.js";import{G as Z}from"./gauge-CRCf-Ntq.js";import{C as Q}from"./copy-BEHZe3r5.js";const ee=j("arrow-down",[["path",{d:"M12 5v14",key:"s699le"}],["path",{d:"m19 12-7 7-7-7",key:"1idqje"}]]);const te=j("rocket",[["path",{d:"M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z",key:"m3kijz"}],["path",{d:"m12 15-3-3a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.35 22.35 0 0 1-4 2z",key:"1fmvmk"}],["path",{d:"M9 12H4s.55-3.03 2-4c1.62-1.08 5 0 5 0",key:"1f8sc4"}],["path",{d:"M12 15v5s3.03-.55 4-2c1.08-1.62 0-5 0-5",key:"qeys4"}]]),se={class:"docs-root"},oe={class:"hero"},ne={class:"hero-lede"},ae={class:"origin-pill"},re={class:"hero-cta"},ie=["href"],le={class:"step-grid"},de={class:"step-num"},ce={class:"step-title"},ue={class:"step-body"},pe={class:"runtime-grid"},he={class:"runtime-icon"},ve={class:"runtime-id"},ye={class:"runtime-name"},me={class:"runtime-meta"},fe={class:"deploy-flow"},be={class:"flow-step"},ge={class:"flow-arrow"},we={class:"flow-step"},ke={class:"hint"},Se={class:"dual-card"},Ce={class:"dual-pane"},Te={class:"dual-icon env"},_e={class:"dual-pane"},Oe={class:"dual-icon secret"},Ae={class:"dual-card"},Ee={class:"dual-pane"},Re={class:"dual-icon env"},Ie={class:"dual-pane"},Pe={class:"dual-icon secret"},je={class:"dual-card"},xe={class:"dual-pane"},De={class:"dual-icon env"},We={class:"dual-pane"},Ne={class:"dual-icon secret"},Ke={class:"timeline"},Ue={class:"timeline-dot"},Be={class:"timeline-body"},Je={class:"timeline-title"},He={key:0,class:"timeline-pill"},Le={class:"timeline-meta"},qe={class:"errors-grid"},Me={class:"error-code"},$e={class:"error-when"},Fe=`import os, jwt
from jwt import PyJWKClient

JWKS = PyJWKClient(os.environ["JWT_JWKS_URL"])
AUD  = os.environ["JWT_AUDIENCE"]
ISS  = os.environ["JWT_ISSUER"]

def handler(event):
    auth = event["headers"].get("authorization", "")
    if not auth.startswith("Bearer "):
        return {"statusCode": 401, "body": "missing bearer token"}
    try:
        key = JWKS.get_signing_key_from_jwt(auth[7:]).key
        claims = jwt.decode(auth[7:], key, algorithms=["RS256"],
                            audience=AUD, issuer=ISS)
    except jwt.PyJWTError as e:
        return {"statusCode": 401, "body": f"invalid token: {e}"}
    user_id = claims["sub"]
    return {"statusCode": 200, "body": f"hello {user_id}"}`,Ve=`import os, hmac, hashlib, time

SECRET = os.environ["STRIPE_WEBHOOK_SECRET"].encode()

def handler(event):
    sig_header = event["headers"].get("stripe-signature", "")
    body = event["body"].encode() if isinstance(event["body"], str) else event["body"]
    parts = dict(p.split("=", 1) for p in sig_header.split(","))
    ts = parts.get("t")
    sig = parts.get("v1")
    if not (ts and sig):
        return {"statusCode": 400, "body": "missing signature"}
    if abs(int(time.time()) - int(ts)) > 300:
        return {"statusCode": 400, "body": "timestamp too old"}
    payload = ts.encode() + b"." + body
    expected = hmac.new(SECRET, payload, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(expected, sig):
        return {"statusCode": 400, "body": "signature mismatch"}
    # Process the event …
    return {"statusCode": 200, "body": "ok"}`,Ge=`import os, jwt
from jwt import PyJWKClient

ALLOWED = {"https://myapp.com", "https://staging.myapp.com"}
JWKS = PyJWKClient(os.environ["JWT_JWKS_URL"])

def cors_headers(origin):
    allow = origin if origin in ALLOWED else "null"
    return {
        "Access-Control-Allow-Origin": allow,
        "Access-Control-Allow-Credentials": "true",
        "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
        "Access-Control-Allow-Headers": "Content-Type, Authorization",
        "Access-Control-Max-Age": "86400",
        "Vary": "Origin",
    }

def handler(event, context):
    origin = event["headers"].get("origin", "")
    cors = cors_headers(origin)

    # 1. Preflight: answer BEFORE any auth check.
    if event["method"] == "OPTIONS":
        return {"statusCode": 204, "headers": cors, "body": ""}

    # 2. Auth — keep CORS headers on the failure response too,
    #    or the browser will hide the real error from your app.
    auth = event["headers"].get("authorization", "")
    if not auth.startswith("Bearer "):
        return {"statusCode": 401, "headers": cors, "body": "missing bearer"}
    try:
        key = JWKS.get_signing_key_from_jwt(auth[7:]).key
        claims = jwt.decode(auth[7:], key, algorithms=["RS256"],
                            audience=os.environ["JWT_AUDIENCE"],
                            issuer=os.environ["JWT_ISSUER"])
    except jwt.PyJWTError as e:
        return {"statusCode": 401, "headers": cors, "body": f"invalid: {e}"}

    # 3. Real handler — also returns CORS headers.
    return {"statusCode": 200,
            "headers": {**cors, "Content-Type": "application/json"},
            "body": '{"user": "' + claims["sub"] + '"}'}`,Xe=`{
  "error": {
    "code": "VALIDATION",
    "message": "name must be lowercase and dash-separated",
    "request_id": "req_abc123"
  }
}`,it={__name:"Docs",setup(ze){const p=m(()=>window.location.origin),x=[{title:"Pick a runtime",body:"Node 22 / 24 or Python 3.13 / 3.14. Auto-detected from the code you paste."},{title:"Write the handler",body:"A single function that accepts an event and returns { statusCode, headers, body }."},{title:"Deploy",body:"One click. Code is content-addressed, the prior version stays available for rollback."},{title:"Invoke",body:"Curl the URL printed under the editor, or wire it up to a custom route or cron schedule."}],u=g({name:"DocSection",props:{id:{type:String,required:!0},eyebrow:{type:String,default:""},title:{type:String,required:!0},kicker:{type:String,default:""}},setup(r,{slots:t}){return()=>a("section",{id:r.id,class:"doc-section"},[a("div",{class:"sec-head"},[r.eyebrow?a("div",{class:"sec-eyebrow"},r.eyebrow):null,a("h2",{class:"sec-title"},r.title),r.kicker?a("p",{class:"sec-kicker"},r.kicker):null]),a("div",{class:"sec-body"},t.default?.())])}}),h=g({name:"CodeBlock",props:{code:{type:String,required:!0},lang:{type:String,default:""}},setup(r){const t=R(!1),v=async()=>{await q(r.code)&&(t.value=!0,setTimeout(()=>{t.value=!1},1200))};return()=>a("div",{class:"codeblock"},[a("div",{class:"codeblock-bar"},[a("span",{class:"codeblock-lang"},r.lang),a("button",{class:"codeblock-copy",onClick:v,title:"Copy code"},[t.value?a(L,{class:"w-3 h-3"}):a(Q,{class:"w-3 h-3"}),t.value?"Copied":"Copy"])]),a("pre",{class:"codeblock-pre"},a("code",null,r.code))])}}),C=g({name:"TabbedCode",props:{tabs:{type:Array,required:!0},storageKey:{type:String,default:""}},setup(r){const t=(()=>{try{if(r.storageKey){const d=localStorage.getItem(r.storageKey);if(d&&r.tabs.some(y=>y.label===d))return d}}catch{}return r.tabs[0]?.label})(),v=R(t),i=d=>{v.value=d;try{r.storageKey&&localStorage.setItem(r.storageKey,d)}catch{}};return()=>{const d=r.tabs.find(y=>y.label===v.value)||r.tabs[0];return a("div",{class:"tabbed"},[a("div",{class:"tabbed-tabs"},r.tabs.map(y=>a("button",{key:y.label,class:["tabbed-tab",{active:y.label===v.value}],onClick:()=>i(y.label)},y.label))),a(h,{code:d.code,lang:d.lang})])}}}),T=g({setup(){return()=>a("svg",{viewBox:"0 0 32 32",width:"20",height:"20",fill:"none",stroke:"currentColor","stroke-width":"1.6","stroke-linecap":"round","stroke-linejoin":"round"},[a("path",{d:"M11 6h6a4 4 0 0 1 4 4v3H11a4 4 0 0 0-4 4v3a4 4 0 0 0 4 4h2"}),a("path",{d:"M21 26h-6a4 4 0 0 1-4-4v-3h10a4 4 0 0 0 4-4v-3a4 4 0 0 0-4-4h-2"})])}}),_=g({setup(){return()=>a("svg",{viewBox:"0 0 32 32",width:"20",height:"20",fill:"none",stroke:"currentColor","stroke-width":"1.6","stroke-linecap":"round","stroke-linejoin":"round"},[a("path",{d:"M16 3 4 10v12l12 7 12-7V10z"}),a("path",{d:"M16 3v26"})])}}),D=m(()=>[{label:"Python",lang:"python",code:`def handler(event):
    body = event.get("body") or {}
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": {"hello": body.get("name", "world")},
    }`},{label:"Node.js",lang:"js",code:`exports.handler = async (event) => {
  const body = event.body || {};
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: { hello: body.name || 'world' },
  };
};`}]),W=m(()=>[{label:"curl",lang:"bash",code:`curl -X POST ${p.value}/api/v1/invoke/<function_id>/ \\
  -H 'Content-Type: application/json' \\
  -d '{"name": "Orva"}'`},{label:"fetch",lang:"js",code:`const res = await fetch('${p.value}/api/v1/invoke/<function_id>/', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ name: 'Orva' }),
});
console.log(await res.json());`},{label:"Python",lang:"python",code:`import httpx

r = httpx.post(
    "${p.value}/api/v1/invoke/<function_id>/",
    json={"name": "Orva"},
)
print(r.json())`}]),N=[{id:"python314",name:"Python 3.14",entry:"handler.py",deps:"requirements.txt",icon:T,flavor:"flavor-py"},{id:"python313",name:"Python 3.13",entry:"handler.py",deps:"requirements.txt",icon:T,flavor:"flavor-py"},{id:"node24",name:"Node.js 24",entry:"handler.js",deps:"package.json",icon:_,flavor:"flavor-node"},{id:"node22",name:"Node.js 22",entry:"handler.js",deps:"package.json",icon:_,flavor:"flavor-node"}],K=m(()=>`curl -X POST ${p.value}/api/v1/functions \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"name":"hello","runtime":"python314","memory_mb":128,"cpus":0.5}'`),U=m(()=>`tar czf code.tar.gz handler.py requirements.txt
curl -X POST ${p.value}/api/v1/functions/<function_id>/deploy \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -F code=@code.tar.gz`),B=m(()=>`curl -X POST ${p.value}/api/v1/functions/<function_id>/secrets \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"key":"DATABASE_URL","value":"postgres://…"}'`),J=m(()=>`# generate signature
SECRET='your-shared-secret-stored-in-function-secrets'
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')

curl -X POST ${p.value}/api/v1/invoke/<function_id>/ \\
  -H "X-Orva-Timestamp: $TS" \\
  -H "X-Orva-Signature: sha256=$SIG" \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`),H=[{code:"VALIDATION",when:"Bad request body or path parameter."},{code:"UNAUTHORIZED",when:"Missing or invalid API key / session cookie."},{code:"NOT_FOUND",when:"Function, deployment, or secret doesn't exist."},{code:"RATE_LIMITED",when:"Too many requests — check the Retry-After header."},{code:"VERSION_GCD",when:"Rollback target was garbage-collected."},{code:"INSUFFICIENT_DISK",when:"Host is below min_free_disk_mb."}],O=[{label:"v3 — abc123def",meta:"Deployed 2m ago",active:!0},{label:"v2 — 4f5e6a",meta:"Yesterday",active:!1},{label:"v1 — 9c2b1f",meta:"2 days ago",active:!1}],w=g({name:"Callout",props:{title:{type:String,default:""},tone:{type:String,default:"info"},icon:{type:[Object,Function],default:null}},setup(r,{slots:t}){return()=>a("div",{class:["callout",`tone-${r.tone}`]},[a("div",{class:"callout-head"},[r.icon?a(r.icon,{class:"w-4 h-4"}):null,r.title?a("span",null,r.title):null]),a("div",{class:"callout-body"},t.default?.())])}});return(r,t)=>{const v=$("router-link");return f(),b("div",se,[e("header",oe,[t[4]||(t[4]=e("div",{class:"hero-eyebrow"}," Orva · Documentation ",-1)),t[5]||(t[5]=e("h1",{class:"hero-title"},[s(" Build serverless functions "),e("span",{class:"hero-accent"},"in minutes.")],-1)),e("p",ne,[t[0]||(t[0]=s(" A practical guide for this Orva instance. Every code example below runs against ",-1)),e("code",ae,c(p.value),1),t[1]||(t[1]=s(" — what you copy is what works. ",-1))]),e("div",re,[n(v,{to:"/deploy",class:"cta-primary"},{default:l(()=>[n(o(te),{class:"w-4 h-4"}),t[2]||(t[2]=s(" Create your first function ",-1))]),_:1}),e("a",{href:`${p.value}/api/v1/system/health`,target:"_blank",rel:"noopener",class:"cta-secondary"},[n(o(M),{class:"w-4 h-4"}),t[3]||(t[3]=s(" Check API health ",-1))],8,ie)])]),n(o(u),{id:"quickstart",eyebrow:"01",title:"Quick start",kicker:"From empty editor to first invocation in under a minute."},{default:l(()=>[e("div",le,[(f(),b(k,null,S(x,(i,d)=>e("div",{key:i.title,class:"step-card"},[e("div",de,c(String(d+1).padStart(2,"0")),1),e("div",ce,c(i.title),1),e("p",ue,c(i.body),1)])),64))])]),_:1}),n(o(u),{id:"handler",eyebrow:"02",title:"The handler",kicker:"Export one function. Return an HTTP-shaped object. That's the contract."},{default:l(()=>[n(o(C),{tabs:D.value,"storage-key":"docs.handler"},null,8,["tabs"]),t[6]||(t[6]=e("div",{class:"kv-grid"},[e("div",{class:"kv"},[e("div",{class:"kv-label"}," Input "),e("div",{class:"kv-value"},[e("code",null,"event.method"),s(", "),e("code",null,"event.path"),s(", "),e("code",null,"event.headers"),s(", "),e("code",null,"event.query"),s(", "),e("code",null,"event.body")])]),e("div",{class:"kv"},[e("div",{class:"kv-label"}," Output "),e("div",{class:"kv-value"},[e("code",null,"{ statusCode, headers, body }"),s(" — body can be a string or any JSON-serialisable value. ")])]),e("div",{class:"kv"},[e("div",{class:"kv-label"}," Injected "),e("div",{class:"kv-value"},[s(" Env vars and decrypted secrets are exposed via "),e("code",null,"process.env"),s(" / "),e("code",null,"os.environ"),s(" at spawn time. ")])])],-1))]),_:1}),n(o(u),{id:"runtimes",eyebrow:"03",title:"Runtimes",kicker:"Latest two majors per language. Older minor versions auto-migrate."},{default:l(()=>[e("div",pe,[(f(),b(k,null,S(N,i=>e("div",{key:i.id,class:A(["runtime-card",i.flavor])},[e("div",he,[(f(),F(V(i.icon)))]),e("div",ve,c(i.id),1),e("div",ye,c(i.name),1),e("ul",me,[e("li",null,[t[7]||(t[7]=e("span",null,"Entry",-1)),e("code",null,c(i.entry),1)]),e("li",null,[t[8]||(t[8]=e("span",null,"Deps",-1)),e("code",null,c(i.deps),1)])])],2)),64))])]),_:1}),n(o(u),{id:"invoke",eyebrow:"04",title:"Invoking a function",kicker:"Each function gets a stable URL. Send a body, return whatever the handler returns."},{default:l(()=>[n(o(C),{tabs:W.value,"storage-key":"docs.invoke"},null,8,["tabs"]),n(o(w),{title:"Custom routes"},{default:l(()=>[...t[9]||(t[9]=[s(" Want a friendly path like ",-1),e("code",null,"/webhooks/stripe",-1),s("? Attach a route via ",-1),e("code",null,"POST /api/v1/routes",-1),s(". Reserved prefixes (",-1),e("code",null,"/api/",-1),s(", ",-1),e("code",null,"/auth/",-1),s(", ",-1),e("code",null,"/web/",-1),s(", ",-1),e("code",null,"/_orva/",-1),s(") are off-limits. ",-1)])]),_:1})]),_:1}),n(o(u),{id:"deploy",eyebrow:"05",title:"Deploying via API",kicker:"Two-step from CI: create the function row, upload a tarball."},{default:l(()=>[e("div",fe,[e("div",be,[t[10]||(t[10]=e("div",{class:"flow-step-head"},[e("span",{class:"flow-num"},"1"),e("span",{class:"flow-title"},"Create the function")],-1)),n(o(h),{code:K.value,lang:"bash"},null,8,["code"])]),e("div",ge,[n(o(ee),{class:"w-4 h-4"})]),e("div",we,[t[11]||(t[11]=e("div",{class:"flow-step-head"},[e("span",{class:"flow-num"},"2"),e("span",{class:"flow-title"},"Upload code")],-1)),n(o(h),{code:U.value,lang:"bash"},null,8,["code"])])]),e("p",ke,[t[13]||(t[13]=s(" Mint a key on the ",-1)),n(v,{to:"/api-keys",class:"link"},{default:l(()=>[...t[12]||(t[12]=[s(" Access Keys ",-1)])]),_:1}),t[14]||(t[14]=s(" page. Builds run async — poll ",-1)),t[15]||(t[15]=e("code",null,"/api/v1/deployments/<id>",-1)),t[16]||(t[16]=s(" or watch the SSE stream until ",-1)),t[17]||(t[17]=e("code",null,"phase: succeeded",-1)),t[18]||(t[18]=s(". ",-1))])]),_:1}),n(o(u),{id:"secrets",eyebrow:"06",title:"Secrets & environment",kicker:"Plaintext for config, encrypted for credentials. Both reach your handler the same way."},{default:l(()=>[e("div",Se,[e("div",Ce,[e("div",Te,[n(o(X),{class:"w-4 h-4"})]),t[19]||(t[19]=e("div",{class:"dual-title"}," Environment variables ",-1)),t[20]||(t[20]=e("p",{class:"dual-body"},[s(" Plaintext, set on the function record. Use for "),e("em",null,"build flags"),s(", "),e("em",null,"feature toggles"),s(", anything safe to read from the DB. ")],-1))]),e("div",_e,[e("div",Oe,[n(o(z),{class:"w-4 h-4"})]),t[21]||(t[21]=e("div",{class:"dual-title"}," Secrets ",-1)),t[22]||(t[22]=e("p",{class:"dual-body"},[s(" AES-256-GCM at rest, decrypted only into the sandbox process. Use for "),e("em",null,"API keys"),s(", "),e("em",null,"DB URLs"),s(", anything that shouldn't appear in the API. ")],-1))])]),n(o(h),{code:B.value,lang:"bash"},null,8,["code"]),t[23]||(t[23]=e("p",{class:"hint"}," Adding or removing a secret triggers a warm-pool refresh, so the next invocation sees the new value within seconds. ",-1))]),_:1}),n(o(u),{id:"network",eyebrow:"07",title:"Network access",kicker:"Off by default. Opt-in per function — most handlers are pure compute and don't need it."},{default:l(()=>[e("div",Ae,[e("div",Ee,[e("div",Re,[n(o(I),{class:"w-4 h-4"})]),t[24]||(t[24]=e("div",{class:"dual-title"},[s(" none "),e("span",{class:"text-foreground-muted font-normal"},"(default)")],-1)),t[25]||(t[25]=e("p",{class:"dual-body"},[s(" Function runs in an isolated network namespace with only loopback. "),e("em",null,"No DNS, no outbound TCP/UDP."),s(" Best for pure-compute handlers and a strong containment guarantee. ")],-1))]),e("div",Ie,[e("div",Pe,[n(o(I),{class:"w-4 h-4"})]),t[26]||(t[26]=e("div",{class:"dual-title"}," egress ",-1)),t[27]||(t[27]=e("p",{class:"dual-body"},[s(" Userspace TCP/UDP stack via nsjail's "),e("code",null,"--user_net"),s(". The function can call "),e("em",null,"external HTTPS APIs"),s(" — Stripe, OpenAI, your DB. Host network interfaces are still not exposed. ")],-1))])]),n(o(w),{icon:o(E),tone:"warn",title:"Why off by default"},{default:l(()=>[...t[28]||(t[28]=[s(" A serverless platform is exactly where one buggy or compromised function shouldn't be able to phone home. The toggle is per-function so you can grant network access only where it's needed and audit it via the egress badge on the Functions list. ",-1)])]),_:1},8,["icon"]),t[29]||(t[29]=e("p",{class:"hint"},[s(" Toggle from the editor's "),e("span",{class:"text-white"},"Settings"),s(' modal ("Allow outbound network"). Changing the toggle drains warm workers, so the next invocation respawns with the new mode within seconds. ')],-1))]),_:1}),n(o(u),{id:"securing",eyebrow:"08",title:"Securing your function",kicker:"Functions are public by default — same posture as Cloudflare Workers, Vercel Functions, and Lambda Function URLs. Auth is your function's job; the platform gives you opt-in guardrails."},{default:l(()=>[n(o(w),{icon:o(P),tone:"info",title:"The mental model"},{default:l(()=>[...t[30]||(t[30]=[s(" Your ",-1),e("span",{class:"text-white"},"platform API key",-1),s(" never ships to a browser — it deploys functions and manages config. The credential a browser sends is the ",-1),e("span",{class:"text-white"},"end user's",-1),s(" JWT or session cookie, and your handler verifies it. This is how every modern serverless platform works in production. ",-1)])]),_:1},8,["icon"]),t[36]||(t[36]=e("h3",{class:"recipe-title"},"Recipe 1 — Verify a JWT (Auth0 / Clerk / Supabase / Firebase)",-1)),t[37]||(t[37]=e("p",{class:"recipe-body"},[s(" Most user-facing apps ship a JWT to the browser at login. The browser attaches it as "),e("code",null,"Authorization: Bearer <jwt>"),s(" on every invoke. Your handler verifies the signature against the issuer's JWKS URL — store the issuer + audience as function secrets. ")],-1)),n(o(h),{code:Fe,lang:"python"}),t[38]||(t[38]=e("h3",{class:"recipe-title"},"Recipe 2 — Verify a Stripe webhook signature",-1)),t[39]||(t[39]=e("p",{class:"recipe-body"},[s(" Webhook senders can't carry an Orva session. They sign each request with a shared secret instead — the canonical pattern. Store "),e("code",null,"STRIPE_WEBHOOK_SECRET"),s(" in function secrets and verify the "),e("code",null,"Stripe-Signature"),s(" header. ")],-1)),n(o(h),{code:Ve,lang:"python"}),t[40]||(t[40]=e("h3",{class:"recipe-title"},"Recipe 3 — Platform-managed gates",-1)),t[41]||(t[41]=e("p",{class:"recipe-body"},[s(" For internal-only functions (cron jobs, server-to-server) flip "),e("span",{class:"text-white"},"Invoke gate"),s(" in the editor's Settings modal. Two modes are built in so you don't have to write the code: ")],-1)),e("div",je,[e("div",xe,[e("div",De,[n(o(Y),{class:"w-4 h-4"})]),t[31]||(t[31]=e("div",{class:"dual-title"}," platform_key ",-1)),t[32]||(t[32]=e("p",{class:"dual-body"},[s(" Caller must send "),e("code",null,"X-Orva-API-Key"),s(' or a valid Orva session cookie. Useful for "only my CI / cron / backend can hit this." Returns '),e("code",null,"401 UNAUTHORIZED"),s(" otherwise. ")],-1))]),e("div",We,[e("div",Ne,[n(o(P),{class:"w-4 h-4"})]),t[33]||(t[33]=e("div",{class:"dual-title"}," signed ",-1)),t[34]||(t[34]=e("p",{class:"dual-body"},[s(" Caller must send "),e("code",null,"X-Orva-Signature: sha256=<hex>"),s(" and "),e("code",null,"X-Orva-Timestamp: <unix-secs>"),s(" computed as "),e("code",null,'HMAC(secret, "<ts>.<body>")'),s(". Secret lives in the function's secret store under "),e("code",null,"ORVA_SIGNING_SECRET"),s(". ±5 min skew window. ")],-1))])]),n(o(h),{code:J.value,lang:"bash"},null,8,["code"]),n(o(w),{icon:o(Z),tone:"info",title:"Rate limit (always available)"},{default:l(()=>[...t[35]||(t[35]=[s(" Public functions can still be abuse magnets. Set ",-1),e("span",{class:"text-white"},"Rate limit",-1),s(" in the editor to a per-IP-per-minute cap. Bursts up to the cap are allowed, then refill at ",-1),e("em",null,"cap",-1),s("/60 per second. Returns ",-1),e("code",null,"429 RATE_LIMITED",-1),s(" with ",-1),e("code",null,"Retry-After: 60",-1),s(". ",-1)])]),_:1},8,["icon"]),t[42]||(t[42]=e("h3",{class:"recipe-title"},"Recipe 4 — CORS for browser callers",-1)),t[43]||(t[43]=e("p",{class:"recipe-body"},[s(" The platform stays out of the response — your handler controls every header. That means CORS lives in your code, where it can change per-route or per-user without a config rebuild. Three rules: answer "),e("code",null,"OPTIONS"),s(" preflights without auth, set CORS headers on every response (including "),e("code",null,"401"),s(" / "),e("code",null,"500"),s(" — otherwise the browser hides the real error), and allowlist origins rather than wildcarding when credentials are involved. ")],-1)),n(o(h),{code:Ge,lang:"python"}),t[44]||(t[44]=e("p",{class:"hint"},[s(" Anti-pattern: do not put your platform API key in browser JavaScript. It would be visible in DevTools within minutes. CORS is not auth — it only restricts "),e("em",null,"other websites"),s(" from reading your response in a user's browser, never blocks direct curl/Postman calls. ")],-1))]),_:1}),n(o(u),{id:"versions",eyebrow:"09",title:"Versions & rollback",kicker:"Every deploy is content-addressed and kept on disk. Rollback is a symlink retarget — no rebuild."},{default:l(()=>[e("div",Ke,[(f(),b(k,null,S(O,(i,d)=>e("div",{key:i.label,class:A(["timeline-item",{active:i.active}])},[e("div",Ue,[e("span",null,c(O.length-d),1)]),e("div",Be,[e("div",Je,[s(c(i.label),1),i.active?(f(),b("span",He,"active")):G("",!0)]),e("div",Le,c(i.meta),1)])],2)),64))]),n(o(w),{icon:o(E),tone:"warn",title:"GC retention"},{default:l(()=>[...t[45]||(t[45]=[s(" Default retention is the last ",-1),e("strong",null,"5",-1),s(" versions per function. Older ones get pruned. A rollback to a GC'd hash returns ",-1),e("code",null,"VERSION_GCD",-1),s(" (HTTP 410) — re-deploy that code if you need it back. ",-1)])]),_:1},8,["icon"])]),_:1}),n(o(u),{id:"errors",eyebrow:"10",title:"Error envelope",kicker:"Every error has a stable code, a human message, and a request id. Surface the message; switch on the code."},{default:l(()=>[n(o(h),{code:Xe,lang:"json"}),e("div",qe,[(f(),b(k,null,S(H,i=>e("div",{key:i.code,class:"error-card"},[e("code",Me,c(i.code),1),e("div",$e,c(i.when),1)])),64))])]),_:1})])}}};export{it as default};
