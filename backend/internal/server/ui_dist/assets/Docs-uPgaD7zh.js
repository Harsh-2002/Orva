import{E as W}from"./format-CsU4_SPu.js";import{c as Y}from"./clipboard-CmSw2rR-.js";import{a as Ee,b as De}from"./aiPrompts-Dgb3jxRL.js";import{G as Re,o as J,J as He,a as g,b as e,s as P,n as j,f as n,F as _,p as A,d,l as b,k as a,h as C,bb as $e,t as l,g as Me,r as k,q as v,aF as x,z as s,a5 as Le,D as qe,j as u,K as Z,a1 as Ne}from"./index-CmY58qNN.js";import{H as f,p as Be,j as Q,a as ze,b as N}from"./github-dark-BrynTfs3.js";import{C as B}from"./check-z8qyH5P-.js";import{C as z}from"./copy-BzujsGZw.js";import{G as K}from"./globe-D5WsrNJb.js";import{C as ee}from"./chevron-right-9PDhxIr8.js";import{K as M}from"./key-round-ByFVPOPP.js";import{C as Ke}from"./chevron-down-BMYhN6hn.js";import{V as Ue}from"./variable-CG75qovQ.js";import{L as Fe}from"./lock-DHycfcno.js";function Xe(L){const q=L.regex,r="HTTP/([32]|1\\.[01])",I=/[A-Za-z][A-Za-z0-9-]*/,E={className:"attribute",begin:q.concat("^",I,"(?=\\:\\s)"),starts:{contains:[{className:"punctuation",begin:/: /,relevance:0,starts:{end:"$",relevance:0}}]}},T=[E,{begin:"\\n\\n",starts:{subLanguage:[],endsWithParent:!0}}];return{name:"HTTP",aliases:["https"],illegal:/\S/,contains:[{begin:"^(?="+r+" \\d{3})",end:/$/,contains:[{className:"meta",begin:r},{className:"number",begin:"\\b\\d{3}\\b"}],starts:{end:/\b\B/,illegal:/\S/,contains:T}},{begin:"(?=^[A-Z]+ (.*?) "+r+"$)",end:/$/,contains:[{className:"string",begin:" ",end:" ",excludeBegin:!0,excludeEnd:!0},{className:"meta",begin:r},{className:"keyword",begin:"[A-Z]+"}],starts:{end:/\b\B/,illegal:/\S/,contains:T}},L.inherit(E,{relevance:0})]}}const Ve={class:"space-y-12 pb-16"},Ge={class:"docs-hero"},We={class:"docs-hero-content"},Ye={class:"docs-hero-row"},Je={class:"docs-hero-actions"},Ze=["title","aria-label"],Qe={class:"docs-hero-toc","aria-label":"Jump to docs section"},eo=["href"],oo={class:"docs-hero-toc-num"},so={id:"handler",class:"space-y-5 scroll-mt-6"},to={class:"doc-table-wrap"},ao={class:"doc-table"},no={class:"doc-cell-key"},co={class:"doc-cell-mono"},ro={class:"doc-cell-mono hidden sm:table-cell"},io={class:"doc-cell-mono hidden md:table-cell"},lo={id:"deploy",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},po={class:"grid grid-cols-1 lg:grid-cols-2 gap-3"},uo={class:"space-y-2"},ho={class:"space-y-2"},vo={class:"space-y-2"},mo={id:"config",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},bo={class:"doc-table-wrap"},go={class:"doc-table"},yo={class:"doc-cell-key whitespace-nowrap"},fo={class:"doc-cell-mono hidden sm:table-cell whitespace-nowrap"},ko={class:"doc-cell-body"},wo={class:"space-y-2"},Co={class:"doc-details group"},xo={class:"doc-details-summary"},To={class:"doc-details-body"},So={id:"sdk",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},_o={class:"space-y-2"},Ao={class:"space-y-2"},Oo={class:"space-y-2"},Po={id:"schedules",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},jo={class:"doc-section-head"},Io={class:"doc-lede"},Eo={id:"webhooks",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Do={class:"doc-section-head"},Ro={class:"doc-lede"},Ho={class:"doc-table-wrap"},$o={class:"doc-table"},Mo={class:"doc-cell-key whitespace-nowrap"},Lo={class:"doc-cell-body"},qo={class:"space-y-2"},No={id:"mcp",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Bo={class:"grid grid-cols-1 md:grid-cols-3 gap-3"},zo={class:"doc-card"},Ko={class:"doc-card-body"},Uo={class:"doc-chip break-all"},Fo={class:"doc-token-bar"},Xo={class:"flex items-center gap-2 min-w-0 flex-1"},Vo={key:0,class:"text-sm text-foreground-muted truncate"},Go={key:1,class:"text-sm text-success truncate"},Wo={class:"doc-chip"},Yo=["disabled"],Jo={class:"doc-details group"},Zo={class:"doc-details-summary"},Qo={class:"doc-details-body space-y-4"},es={id:"generate",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},os={class:"ai-prompt-actions"},ss={key:0,class:"prompt-collapse-fade","aria-hidden":"true"},ts=["aria-expanded"],as={id:"tracing",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},ns={class:"doc-table-wrap"},cs={class:"doc-table"},ds={class:"doc-cell-key whitespace-nowrap"},rs={class:"doc-cell-body"},is={id:"errors",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},ls={class:"doc-table-wrap"},ps={class:"doc-table"},us={class:"doc-cell-key whitespace-nowrap"},hs={class:"doc-cell-body"},vs={id:"cli",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},ms={class:"doc-prose"},bs={class:"doc-table-wrap"},gs={class:"doc-table"},ys={class:"doc-cell-key whitespace-nowrap"},fs={class:"doc-cell-mono"},ks={class:"doc-cell-body hidden md:table-cell"},ws={class:"space-y-2"},Cs={class:"space-y-2"},xs={class:"space-y-2"},Ts={class:"space-y-2"},Ss={class:"space-y-2"},_s=`# Available inside every running function — refresh per-invocation:
ORVA_TRACE_ID=tr_3e39f6991c66f140577c6021da7dd13b   # one per causal chain
ORVA_SPAN_ID=sp_4ceba57f6b1c982e                    # this execution

# Python:        os.environ["ORVA_TRACE_ID"]
# Node.js:       process.env.ORVA_TRACE_ID
# Reading them is optional — the platform records the trace for you.`,As=`// Function A — calls B via the SDK. Trace context flows automatically.
const { invoke, jobs } = require('orva')

module.exports.handler = async (event) => {
  // F2F call — B becomes a child span under A.
  const result = await invoke('send_email', { to: event.email })

  // Job enqueue — when this job runs (now or in 6 hours), the resulting
  // execution lands in the SAME trace as A.
  await jobs.enqueue('audit_log', { action: 'sent', to: event.email })

  return { statusCode: 200, body: 'ok' }
}`,Os=`# Send the W3C traceparent header — Orva will adopt it as the trace root.
curl -H "traceparent: 00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01" \\
     https://orva.example.com/fn/myfn/

# Response always echoes:
# X-Trace-Id: tr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`,Ps=`{
  "error": {
    "code": "VALIDATION",
    "message": "name must be lowercase and dash-separated",
    "request_id": "req_abc123"
  }
}`,js=`# 1. Generate an API key in the dashboard (Keys page) or via the API
# 2. Tell the CLI where to find your Orva and which key to use
orva login \\
  --endpoint https://orva.example.com \\
  --api-key  orva_xxx_your_key_here

# Writes ~/.orva/config.yaml. Subsequent commands need no flags.
orva system health      # smoke test`,Is=`# Init a project in cwd (creates orva.yaml + handler stub)
orva init

# Deploy from a directory. Auto-detects handler.ts when tsconfig.json
# is present; else uses the runtime default (handler.js / handler.py).
orva deploy ./my-fn \\
  --name    resize-image \\
  --runtime node24

# Override the entrypoint explicitly:
orva deploy ./my-fn --name api --runtime python314 --entrypoint app.py`,Es=`# Invoke a function by name or fn_<id>:
orva invoke resize-image --data '{"url":"https://example.com/cat.jpg"}'

# Recent executions:
orva logs resize-image

# Single execution, with stdout/stderr:
orva logs resize-image --exec-id exec_abc123

# Live tail — SSE stream, Ctrl-C to stop:
orva logs resize-image --tail`,Ds=`# List keys (optionally by prefix)
orva kv list resize-image
orva kv list resize-image --prefix user:

# Read / write / delete
orva kv get  resize-image cache:home
orva kv put  resize-image cache:home '{"hits":42}' --ttl 3600
orva kv delete resize-image cache:home`,Rs=`# Secrets — encrypted at rest, injected as env vars at spawn:
orva secrets set    resize-image S3_BUCKET my-bucket
orva secrets list   resize-image
orva secrets delete resize-image S3_BUCKET

# Cron — fire a function on a schedule:
orva cron create --fn daily-report --expr '0 9 * * *' --tz Asia/Kolkata
orva cron list
orva cron update <cron_id> --enabled false   # pause
orva cron delete <cron_id>

# Jobs — fire-and-forget background queue:
orva jobs enqueue --fn send-email --data '{"to":"a@b.c"}'
orva jobs list --status pending
orva jobs retry  <job_id>
orva jobs delete <job_id>

# Outbound webhooks (system events):
orva webhooks create --url https://hooks.slack.com/... --events deployment.failed,job.failed
orva webhooks test   <webhook_id>

# Inbound webhook triggers (external POST → function):
orva webhooks inbound create --fn order-handler --signature stripe`,Hs=`orva system health        # daemon up + DB ok
orva system metrics       # JSON metrics snapshot
orva system db-stats      # on-disk breakdown (orva.db, WAL, functions/)
orva system vacuum        # rewrite SQLite to reclaim freelist pages

orva activity                          # last 50 activity rows
orva activity --tail                   # live feed (Ctrl-C)
orva activity --source mcp --limit 200 # MCP-only, last 200`,oe="<YOUR_ORVA_TOKEN>",Ys={__name:"Docs",setup(L){const q=Re();f.registerLanguage("python",Be),f.registerLanguage("javascript",Q),f.registerLanguage("js",Q),f.registerLanguage("json",ze),f.registerLanguage("bash",N),f.registerLanguage("shell",N),f.registerLanguage("sh",N),f.registerLanguage("http",Xe);const r=v(()=>window.location.origin),I=[{id:"handler",num:"01",label:"Handler"},{id:"deploy",num:"02",label:"Deploy"},{id:"config",num:"03",label:"Config"},{id:"sdk",num:"04",label:"SDK"},{id:"schedules",num:"05",label:"Schedules"},{id:"webhooks",num:"06",label:"Webhooks"},{id:"mcp",num:"07",label:"MCP"},{id:"generate",num:"08",label:"AI prompt"},{id:"tracing",num:"09",label:"Tracing"},{id:"errors",num:"10",label:"Errors"},{id:"cli",num:"11",label:"CLI"}],E=k("handler");let T=null;J(()=>{if(typeof IntersectionObserver>"u")return;const c=new Set;T=new IntersectionObserver(o=>{for(const i of o)i.isIntersecting?c.add(i.target.id):c.delete(i.target.id);for(const i of I)if(c.has(i.id)){E.value=i.id;break}},{rootMargin:"-20% 0px -70% 0px",threshold:0});for(const o of I){const i=document.getElementById(o.id);i&&T.observe(i)}}),He(()=>{T&&T.disconnect()});const se=De(),D=k(!1);let U=null;const te=async()=>{await Ee()&&(D.value=!0,clearTimeout(U),U=setTimeout(()=>{D.value=!1},1500))},F=x({setup(){return()=>s("svg",{viewBox:"0 0 256 255",width:"14",height:"14",xmlns:"http://www.w3.org/2000/svg"},[s("defs",null,[s("linearGradient",{id:"pyg1",x1:"0",y1:"0",x2:"1",y2:"1"},[s("stop",{offset:"0","stop-color":"#387EB8"}),s("stop",{offset:"1","stop-color":"#366994"})]),s("linearGradient",{id:"pyg2",x1:"0",y1:"0",x2:"1",y2:"1"},[s("stop",{offset:"0","stop-color":"#FFE052"}),s("stop",{offset:"1","stop-color":"#FFC331"})])]),s("path",{fill:"url(#pyg1)",d:"M126.9 12c-58.3 0-54.7 25.3-54.7 25.3l.1 26.2H128v8H50.5S12 67.2 12 126.1c0 58.9 33.6 56.8 33.6 56.8h19.4v-27.4s-1-33.6 33.1-33.6h55.9s32 .5 32-30.9V43.5S191.7 12 126.9 12zM95.7 29.9a10 10 0 0 1 0 20 10 10 0 0 1 0-20z"}),s("path",{fill:"url(#pyg2)",d:"M129.1 243c58.3 0 54.7-25.3 54.7-25.3l-.1-26.2H128v-8h77.5s38.5 4.4 38.5-54.5c0-58.9-33.6-56.8-33.6-56.8h-19.4v27.4s1 33.6-33.1 33.6H102s-32-.5-32 30.9v52S64.3 243 129.1 243zm30.4-17.9a10 10 0 0 1 0-20 10 10 0 0 1 0 20z"})])}}),X=x({setup(){return()=>s("svg",{viewBox:"0 0 256 280",width:"14",height:"14",xmlns:"http://www.w3.org/2000/svg"},[s("path",{fill:"#3F873F",d:"M128 0 12 67v146l116 67 116-67V67L128 0zm0 24.6 95 54.8v121.2l-95 54.8-95-54.8V79.4l95-54.8z"}),s("path",{fill:"#3F873F",d:"M128 64c-3 0-5.7.7-8 2.3L73 92c-5 2.7-8 8-8 13.6V169c0 5.6 3 10.7 8 13.5l13 7.4c6.3 3.1 8.5 3.1 11.4 3.1 9.4 0 14.8-5.7 14.8-15.6V117c0-1-.7-1.7-1.7-1.7H103c-1 0-1.7.7-1.7 1.7v60.2c0 4.4-4.5 8.7-11.8 5.1l-13.7-7.9a1.6 1.6 0 0 1-.8-1.4v-63.4c0-.6.3-1 .8-1.4l46.8-26.9c.4-.3 1-.3 1.4 0L171 110c.5.4.8.8.8 1.4V174a1.7 1.7 0 0 1-.8 1.4l-46.8 27c-.4.2-1 .2-1.4 0l-12-7.2c-.4-.2-.8-.2-1.2 0-3.4 1.9-4 2.2-7.2 3.3-.8.3-2 .7.4 2.1l15.7 9.3c2.5 1.4 5.3 2.2 8.2 2.2 2.9 0 5.7-.8 8.2-2.2L181 184c5-2.8 8-7.9 8-13.5V107c0-5.6-3-10.7-8-13.5l-46.7-26.7a17 17 0 0 0-6.3-2.8z"})])}}),ae=x({name:"DeployPipelineDiagram",setup(){const c=[{glyph:"▣",label:"Tarball",sub:"POST /deploy"},{glyph:"⟜",label:"Extract",sub:"untar → scratch dir"},{glyph:"◍",label:"Install",sub:"npm / pip"},{glyph:"⟐",label:"Compile",sub:"tsc (TypeScript)"},{glyph:"◉",label:"Activate",sub:"rename → current"},{glyph:"✦",label:"Warm pool",sub:"pre-spawn N workers"}];return()=>s("figure",{class:"doc-diagram"},[s("figcaption",{class:"doc-diagram-cap"},"Deploy pipeline"),s("div",{class:"doc-pipeline"},c.flatMap((o,i)=>{const t=s("div",{key:`s${i}`,class:"doc-pipeline-stage"},[s("div",{class:"doc-pipeline-glyph"},o.glyph),s("div",{class:"doc-pipeline-label"},[s("span",{class:"doc-pipeline-name"},o.label),s("span",{class:"doc-pipeline-sub"},o.sub)])]),p=i<c.length-1?s("div",{key:`a${i}`,class:"doc-pipeline-arrow","aria-hidden":"true"}):null;return p?[t,p]:[t]}))])}}),ne=x({name:"TraceTreeDiagram",setup(){const o=[{fn:"api-gateway",trigger:"http",start:0,dur:220,parent:null,klass:"root"},{fn:"resize-image",trigger:"f2f",start:30,dur:90,parent:"api-gateway",klass:"child"},{fn:"send-email",trigger:"job",start:60,dur:40,parent:"api-gateway",klass:"grand"}],i=t=>t/220*100;return()=>s("figure",{class:"doc-diagram"},[s("figcaption",{class:"doc-diagram-cap"},"Causal trace, one HTTP request and three spans"),s("div",{class:"doc-trace"},[s("div",{class:"doc-trace-axis"},[s("span",null,"0 ms"),s("span",null,"220 ms")]),...o.map(t=>s("div",{key:t.fn,class:["doc-trace-row",`is-${t.klass}`]},[s("div",{class:"doc-trace-label"},[s("span",{class:"doc-trace-fn"},t.fn),s("span",{class:"doc-trace-trigger"},t.trigger)]),s("div",{class:"doc-trace-track"},[s("div",{class:"doc-trace-bar",style:{left:`${i(t.start)}%`,width:`${i(t.dur)}%`},title:`+${t.start}ms · ${t.dur}ms`})]),s("div",{class:"doc-trace-dur"},`${t.dur}ms`)])),s("div",{class:"doc-trace-legend"},[s("span",null,"Same "),s("code",{class:"doc-chip"},"trace_id"),s("span",null," across all spans · "),s("code",{class:"doc-chip"},"parent_span_id"),s("span",null," chains them into a tree.")])])])}}),ce=x({name:"WebhookDeliveryDiagram",setup(){return()=>s("figure",{class:"doc-diagram"},[s("figcaption",{class:"doc-diagram-cap"},"Signed webhook delivery"),s("div",{class:"doc-webhook"},[s("div",{class:"doc-webhook-actor"},[s("div",{class:"doc-webhook-actor-head"},"orvad"),s("div",{class:"doc-webhook-actor-body"},[s("span",null,"event fires"),s("code",{class:"doc-chip"},"deployment.succeeded")])]),s("div",{class:"doc-webhook-wire"},[s("div",{class:"doc-webhook-wire-line","aria-hidden":"true"}),s("div",{class:"doc-webhook-wire-payload"},[s("div",{class:"doc-webhook-wire-method"},"POST"),s("div",{class:"doc-webhook-wire-headers"},[s("code",null,"X-Orva-Event"),s("code",null,"X-Orva-Timestamp"),s("code",null,"X-Orva-Signature")]),s("div",{class:"doc-webhook-wire-sig"},"sha256=hex(hmac(secret, ts.body))")])]),s("div",{class:"doc-webhook-actor"},[s("div",{class:"doc-webhook-actor-head"},"your receiver"),s("div",{class:"doc-webhook-actor-body"},[s("span",null,"verify HMAC"),s("span",null,"→ 2xx within 15s or get retried")])])])])}}),de=v(()=>[{label:"Python",lang:"python",code:`def handler(event):
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
};`}]),re=v(()=>[{label:"curl",lang:"bash",code:`curl -X POST ${r.value}/fn/<function_id> \\
  -H 'Content-Type: application/json' \\
  -d '{"name": "Orva"}'`},{label:"fetch",lang:"js",code:`const res = await fetch('${r.value}/fn/<function_id>', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ name: 'Orva' }),
});
console.log(await res.json());`},{label:"Python",lang:"python",code:`import httpx

r = httpx.post(
    "${r.value}/fn/<function_id>",
    json={"name": "Orva"},
)
print(r.json())`}]),ie=[{id:"python314",name:"Python 3.14",entry:"handler.py",deps:"requirements.txt",icon:F},{id:"python313",name:"Python 3.13",entry:"handler.py",deps:"requirements.txt",icon:F},{id:"node24",name:"Node.js 24",entry:"handler.js",deps:"package.json",icon:X},{id:"node22",name:"Node.js 22",entry:"handler.js",deps:"package.json",icon:X}],le=[{field:"env_vars",purpose:"Plain config",body:"Plaintext config stored on the function record. Use for feature flags and non-secret settings.",icon:Ue,iconClass:"text-violet-300"},{field:"/secrets",purpose:"Encrypted",body:"AES-256-GCM at rest. Values decrypt only into the worker environment at spawn time.",icon:M,iconClass:"text-emerald-300"},{field:"network_mode",purpose:"Egress control",body:"none = isolated loopback. egress = outbound HTTPS allowed; firewall blocklist applies.",icon:K,iconClass:"text-sky-300"},{field:"auth_mode",purpose:"Invoke gate",body:"none = public. platform_key = require Orva API key. signed = require HMAC.",icon:Fe,iconClass:"text-violet-300"},{field:"rate_limit_per_min",purpose:"Per-IP throttle",body:"Optional cap for public or webhook-facing functions. Exceeding it returns 429.",icon:Ne,iconClass:"text-amber-300"}],pe=v(()=>`curl -X POST ${r.value}/api/v1/functions \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"name":"hello","runtime":"python314","memory_mb":128,"cpus":0.5}'`),ue=v(()=>`tar czf code.tar.gz handler.py requirements.txt
curl -X POST ${r.value}/api/v1/functions/<function_id>/deploy \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -F code=@code.tar.gz`),he=v(()=>`curl -X POST ${r.value}/api/v1/functions/<function_id>/secrets \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"key":"DATABASE_URL","value":"postgres://..."}'`),ve=v(()=>`# generate signature
SECRET='your-shared-secret-stored-in-function-secrets'
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')

curl -X POST ${r.value}/fn/<function_id> \\
  -H "X-Orva-Timestamp: $TS" \\
  -H "X-Orva-Signature: sha256=$SIG" \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`),me=v(()=>[{label:"curl",lang:"bash",note:"Create a daily-9am schedule for an existing function. payload is delivered as the invoke body.",code:`curl -X POST ${r.value}/api/v1/functions/<function_id>/cron \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{
    "cron_expr": "0 9 * * *",
    "enabled":   true,
    "payload":   {"task": "daily-summary"}
  }'`},{label:"Toggle / edit",lang:"bash",note:"PUT accepts any subset of {cron_expr, enabled, payload}; omitted fields keep their previous value. next_run_at is recomputed on expr changes.",code:`# pause
curl -X PUT ${r.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"enabled": false}'

# change schedule
curl -X PUT ${r.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"cron_expr": "*/15 * * * *"}'`},{label:"List & delete",lang:"bash",note:"GET /api/v1/cron lists every schedule across functions (with function_name JOIN); per-function uses the nested route.",code:`# all schedules
curl ${r.value}/api/v1/cron \\
  -H 'X-Orva-API-Key: <YOUR_KEY>'

# delete one
curl -X DELETE ${r.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>'`}]),be=[{label:"Python",lang:"python",code:`from orva import kv

def handler(event):
    # Store with optional TTL (seconds). 0 = no expiry.
    kv.put("user:42", {"name": "Ada", "tier": "pro"}, ttl_seconds=3600)

    # Read; default returned if missing or expired.
    user = kv.get("user:42", default=None)

    # List by prefix.
    pages = kv.list(prefix="page:", limit=50)

    # Delete is idempotent.
    kv.delete("user:42")

    return {"statusCode": 200, "body": str(user)}`},{label:"Node.js",lang:"js",code:`const { kv } = require('orva')

exports.handler = async (event) => {
  await kv.put('user:42', { name: 'Ada', tier: 'pro' }, { ttlSeconds: 3600 })

  const user = await kv.get('user:42', null)

  const pages = await kv.list({ prefix: 'page:', limit: 50 })

  await kv.delete('user:42')

  return { statusCode: 200, body: JSON.stringify(user) }
}`}],ge=[{label:"Python",lang:"python",code:`from orva import invoke, OrvaError

def handler(event):
    try:
        # invoke() returns the downstream {statusCode, headers, body}.
        # body is JSON-decoded when possible.
        result = invoke("resize-image", {"url": event["body"]["url"]})
        return {"statusCode": 200, "body": result["body"]}
    except OrvaError as e:
        # 404 = function not found, 507 = call depth exceeded.
        return {"statusCode": e.status or 502, "body": str(e)}`},{label:"Node.js",lang:"js",code:`const { invoke, OrvaError } = require('orva')

exports.handler = async (event) => {
  try {
    const result = await invoke('resize-image', { url: event.body.url })
    return { statusCode: 200, body: result.body }
  } catch (e) {
    if (e instanceof OrvaError) {
      return { statusCode: e.status || 502, body: e.message }
    }
    throw e
  }
}`}],ye=[{label:"Python",lang:"python",code:`from orva import jobs

def handler(event):
    # Fire-and-forget. Returns the job id immediately; the function
    # body runs later via the scheduler. max_attempts retries with
    # exponential backoff on 5xx / exception.
    job_id = jobs.enqueue(
        "send-welcome-email",
        {"to": event["body"]["email"]},
        max_attempts=3,
    )
    return {"statusCode": 202, "body": job_id}`},{label:"Node.js",lang:"js",code:`const { jobs } = require('orva')

exports.handler = async (event) => {
  const jobId = await jobs.enqueue(
    'send-welcome-email',
    { to: event.body.email },
    { maxAttempts: 3 }
  )
  return { statusCode: 202, body: jobId }
}`}],fe=[{name:"deployment.succeeded",when:"A function build finished and the new version is active."},{name:"deployment.failed",when:"A build failed or was rejected."},{name:"function.created",when:"A new function row was created via POST /api/v1/functions."},{name:"function.updated",when:"A function config was edited via PUT /api/v1/functions/{id} (status flips during a deploy do NOT fire this; see deployment.*)."},{name:"function.deleted",when:"A function was removed."},{name:"execution.error",when:"An invocation finished with status=error or 5xx."},{name:"cron.failed",when:"A scheduled run failed (bad expr, missing fn, dispatch error, or 5xx)."},{name:"job.succeeded",when:"A queued background job finished successfully."},{name:"job.failed",when:"A queued job exhausted its retries (terminal failure)."}],ke=[{label:"Python",lang:"python",note:"Run on the receiver. Reject anything that fails verification. The signature ensures the request really came from this Orva instance.",code:`import hmac, hashlib, time

def verify(secret: str, ts: str, body: bytes, sig_header: str) -> bool:
    if abs(time.time() - int(ts)) > 300:   # 5-min skew window
        return False
    mac = hmac.new(secret.encode(), f"{ts}.".encode() + body, hashlib.sha256)
    expected = "sha256=" + mac.hexdigest()
    return hmac.compare_digest(expected, sig_header)

# In your Flask/FastAPI/etc. handler:
ts  = request.headers["X-Orva-Timestamp"]
sig = request.headers["X-Orva-Signature"]
if not verify(WEBHOOK_SECRET, ts, request.get_data(), sig):
    return "bad signature", 401`},{label:"Node.js",lang:"js",note:"Same shape as Stripe. Use timingSafeEqual to avoid sig-leak via timing.",code:`const crypto = require('crypto')

function verify(secret, ts, body, sigHeader) {
  if (Math.abs(Date.now() / 1000 - parseInt(ts, 10)) > 300) return false
  const mac = crypto.createHmac('sha256', secret)
  mac.update(ts + '.')
  mac.update(body)
  const expected = 'sha256=' + mac.digest('hex')
  if (expected.length !== sigHeader.length) return false
  return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(sigHeader))
}

// In an express handler with raw body middleware:
app.post('/webhooks/orva', (req, res) => {
  const ok = verify(
    process.env.WEBHOOK_SECRET,
    req.headers['x-orva-timestamp'],
    req.body,                  // raw bytes — NOT parsed JSON
    req.headers['x-orva-signature']
  )
  if (!ok) return res.status(401).send('bad signature')
  res.sendStatus(200)
})`}],we=[{name:"http",desc:"Public HTTP request hit /fn/<id>/. Almost always a root span."},{name:"f2f",desc:"Another function called this one via orva.invoke(). Has a parent_span_id."},{name:"job",desc:"Background job runner picked up an enqueued job. Parent_span_id is whoever enqueued it."},{name:"cron",desc:"Scheduler fired a cron entry. Always a root span."},{name:"inbound",desc:"External webhook hit /webhook/{id}. Always a root span."},{name:"replay",desc:"Operator clicked Replay on a captured execution. Fresh trace, no link to original."},{name:"mcp",desc:"AI agent invoked the function via MCP invoke_function. Fresh trace."}],Ce=[{code:"VALIDATION",when:"Bad request body or path parameter."},{code:"UNAUTHORIZED",when:"Missing or invalid API key / session cookie."},{code:"NOT_FOUND",when:"Function, deployment, or secret doesn't exist."},{code:"RATE_LIMITED",when:"Too many requests; check the Retry-After header."},{code:"VERSION_GCD",when:"Rollback target was garbage-collected."},{code:"INSUFFICIENT_DISK",when:"Host is below min_free_disk_mb."}],xe=[{cmd:"login",subs:W,purpose:"Save endpoint + API key to ~/.orva/config.yaml"},{cmd:"init",subs:W,purpose:"Scaffold an orva.yaml in the current directory"},{cmd:"deploy",subs:"[path]",purpose:"Package a directory and deploy as a function"},{cmd:"invoke",subs:"[name|id]",purpose:"POST to /fn/<id>/ and print the response"},{cmd:"logs",subs:"[name|id] [--tail]",purpose:"List recent executions; --tail follows live via SSE"},{cmd:"functions",subs:"list / get / create / delete",purpose:"CRUD for the function registry"},{cmd:"cron",subs:"list / create / update / delete",purpose:"Manage cron schedules attached to functions"},{cmd:"jobs",subs:"list / enqueue / retry / delete",purpose:"Background queue management"},{cmd:"kv",subs:"list / get / put / delete",purpose:"Browse a function’s key/value store"},{cmd:"secrets",subs:"list / set / delete",purpose:"AES-256-GCM secrets per function"},{cmd:"webhooks",subs:"list / create / test / delete / inbound",purpose:"System-event subscribers + inbound triggers"},{cmd:"routes",subs:"list / set / delete",purpose:"Custom URL → function path mappings"},{cmd:"keys",subs:"list / create / revoke",purpose:"Manage API keys"},{cmd:"activity",subs:"[--tail] [--source web|api|...]",purpose:"Paginated activity rows; live SSE with --tail"},{cmd:"system",subs:"health / metrics / db-stats / vacuum",purpose:"Server diagnostics"},{cmd:"setup",subs:"[--skip-nsjail] [--skip-rootfs]",purpose:"Install nsjail + rootfs on a bare host"},{cmd:"serve",subs:"[--port N]",purpose:"Run as the server daemon (not the CLI client)"},{cmd:"completion",subs:"bash / zsh / fish / powershell",purpose:"Emit shell completion script"}],V=k("");J(async()=>{try{const c=await fetch("/web/docs.md",{cache:"no-cache"});c.ok&&(V.value=await c.text())}catch{}});const Te=v(()=>V.value.replaceAll("{{ORIGIN}}",window.location.origin)),O=k(!1);let G=null;const Se=async()=>{await Y(Te.value)&&(O.value=!0,clearTimeout(G),G=setTimeout(()=>{O.value=!1},1500))},S=k(!1),R=k(""),H=k(!1),_e=v(()=>R.value.slice(0,12)),m=v(()=>R.value||oe),Ae=async()=>{if(!H.value){H.value=!0;try{const c=new Date().toISOString().slice(0,16).replace("T"," "),o=await qe.post("/keys",{name:"MCP: "+c,permissions:["invoke","read","write","admin"]});R.value=o.data.key}catch(c){console.error("mint mcp key failed",c),q.notify({title:"Could not mint key",message:c?.response?.data?.error?.message||c.message||"Unknown error",danger:!0})}finally{H.value=!1}}},Oe=v(()=>[{label:"Claude Code",lang:"bash",note:"Anthropic's `claude` CLI. Restart Claude Code afterwards; `/mcp` lists Orva's 70 tools.",code:`claude mcp add --transport http --scope user orva ${r.value}/mcp --header "Authorization: Bearer ${m.value}"`},{label:"curl",lang:"bash",note:"Talk to MCP directly. Step 1 returns a session id (Mcp-Session-Id) that Step 2 references.",code:`curl -sD - -X POST ${r.value}/mcp \\
  -H 'Authorization: Bearer ${m.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

curl -sX POST ${r.value}/mcp \\
  -H 'Authorization: Bearer ${m.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H 'Mcp-Session-Id: <SID>' \\
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'`}]),Pe=v(()=>[{label:"Claude Desktop",lang:"json",note:"Paste into ~/Library/Application Support/Claude/claude_desktop_config.json (macOS), %APPDATA%\\Claude\\claude_desktop_config.json (Windows), or ~/.config/Claude/claude_desktop_config.json (Linux). Restart Claude Desktop.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${r.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${m.value}"
      }
    }
  }
}`},{label:"Cursor",lang:"bash",note:"Open the link in your browser. Cursor pops an approval dialog and writes ~/.cursor/mcp.json.",code:`cursor://anysphere.cursor-deeplink/mcp/install?name=orva&config=${je.value}`},{label:"VS Code",lang:"bash",note:'User-scoped install via the Copilot-MCP `code --add-mcp` flag. Pick "Workspace" at the prompt to write .vscode/mcp.json instead.',code:`code --add-mcp '{"name":"orva","type":"http","url":"${r.value}/mcp","headers":{"Authorization":"Bearer ${m.value}"}}'`},{label:"Codex CLI",lang:"bash",note:"OpenAI's `codex` CLI. Writes to ~/.codex/config.toml.",code:`codex mcp add --transport streamable-http orva ${r.value}/mcp --header "Authorization: Bearer ${m.value}"`},{label:"OpenCode",lang:"bash",note:`Interactive add. Pick "Remote", paste ${r.value}/mcp, then add the header Authorization: Bearer ${m.value}.`,code:"opencode mcp add"},{label:"Zed",lang:"json",note:"Zed runs MCP as stdio subprocesses, so use the `mcp-remote` bridge. Paste under context_servers in ~/.config/zed/settings.json. Restart Zed.",code:`{
  "context_servers": {
    "orva": {
      "source": "custom",
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "${r.value}/mcp",
        "--header", "Authorization:Bearer ${m.value}"
      ]
    }
  }
}`},{label:"Windsurf",lang:"json",note:"Paste into ~/.codeium/windsurf/mcp_config.json and reload Windsurf.",code:`{
  "mcpServers": {
    "orva": {
      "serverUrl": "${r.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${m.value}"
      }
    }
  }
}`},{label:"claude.ai web",lang:"text",note:"UI-only flow. Settings → Connectors → Add custom connector. claude.ai opens an Orva login + consent popup and issues an OAuth 2.1 token automatically; no token paste required.",code:`URL:  ${r.value}/mcp
Auth: OAuth (auto-discovered)`},{label:"ChatGPT",lang:"text",note:"UI-only flow. Settings → Apps & Connectors → Developer mode → Add new connector. ChatGPT discovers OIDC metadata, performs Dynamic Client Registration, and pops the Orva consent screen. No token paste required.",code:`URL:  ${r.value}/mcp
Auth: OAuth (auto-discovered)`}]),je=v(()=>{const c=JSON.stringify({url:r.value+"/mcp",headers:{Authorization:"Bearer "+m.value}});return typeof window.btoa=="function"?window.btoa(c):c}),Ie=v(()=>[{label:"Cursor (global)",lang:"json",note:"Paste into ~/.cursor/mcp.json, or .cursor/mcp.json in your project root for a per-workspace install.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${r.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${m.value}"
      }
    }
  }
}`},{label:"Cline",lang:"json",note:"In VS Code: open Cline → MCP icon → Configure MCP Servers. Cline writes cline_mcp_settings.json.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${r.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${m.value}"
      },
      "disabled": false
    }
  }
}`}]),h=x({name:"CodeBlock",props:{code:{type:String,required:!0},lang:{type:String,default:""}},setup(c){const o=k(!1),i=async()=>{await Y(c.code)&&(o.value=!0,setTimeout(()=>{o.value=!1},1200))},t=v(()=>{const p=(c.lang||"").toLowerCase();if(p&&f.getLanguage(p))try{return f.highlight(c.code,{language:p,ignoreIllegals:!0}).value}catch{}return c.code.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;")});return()=>s("div",{class:"codeblock"},[s("div",{class:"codeblock-bar"},[s("span",{class:"codeblock-lang"},c.lang||""),s("button",{class:"codeblock-copy",onClick:i,title:"Copy code"},[o.value?s(B,{class:"w-3 h-3"}):s(z,{class:"w-3 h-3"}),o.value?"Copied":"Copy"])]),s("pre",{class:"codeblock-pre"},[s("code",{class:`hljs language-${(c.lang||"text").toLowerCase()}`,innerHTML:t.value})])])}}),y=x({name:"TabbedCode",props:{tabs:{type:Array,required:!0},storageKey:{type:String,default:""}},setup(c){const o=(()=>{try{if(c.storageKey){const p=localStorage.getItem(c.storageKey);if(p&&c.tabs.some(w=>w.label===p))return p}}catch{}return c.tabs[0]?.label})(),i=k(o),t=p=>{i.value=p;try{c.storageKey&&localStorage.setItem(c.storageKey,p)}catch{}};return()=>{const p=c.tabs.find(w=>w.label===i.value)||c.tabs[0];return s("div",{class:"tabbed"},[s("div",{class:"tabbed-tabs"},c.tabs.map(w=>s("button",{key:w.label,class:["tabbed-tab",{active:w.label===i.value}],onClick:()=>t(w.label)},w.label))),p.note?s("div",{class:"tabbed-note"},p.note):null,s(h,{code:p.code,lang:p.lang})])}}}),$=x({name:"Callout",props:{title:{type:String,default:""},icon:{type:[Object,Function],default:null}},setup(c,{slots:o}){return()=>s("div",{class:"callout"},[s("div",{class:"callout-head"},[c.icon?s(c.icon,{class:"callout-icon"}):null,c.title?s("span",null,c.title):null]),s("div",{class:"callout-body"},o.default?.())])}});return(c,o)=>{const i=Le("router-link");return u(),g("div",Ve,[e("header",Ge,[o[3]||(o[3]=e("div",{class:"docs-hero-bg","aria-hidden":"true"},null,-1)),e("div",We,[e("div",Ye,[o[1]||(o[1]=e("div",{class:"docs-hero-text"},[e("h1",{class:"docs-hero-title"}," Documentation "),e("p",{class:"docs-hero-sub"}," Everything you need to write, deploy, and operate functions on Orva. Handler contract, deploy + invoke, SDK, MCP, tracing, error taxonomy. ")],-1)),e("div",Je,[e("button",{class:P(["docs-hero-copy-icon",{copied:O.value}]),title:O.value?"Copied":"Copy entire docs page as Markdown","aria-label":O.value?"Markdown copied to clipboard":"Copy entire docs page as Markdown",onClick:Se},[O.value?(u(),j(n(B),{key:0,class:"w-4 h-4"})):(u(),j(n(z),{key:1,class:"w-4 h-4"}))],10,Ze)])]),e("nav",Qe,[o[2]||(o[2]=e("span",{class:"docs-hero-toc-label"},"Jump to",-1)),(u(),g(_,null,A(I,t=>e("a",{key:t.id,href:`#${t.id}`,class:P(["docs-hero-toc-link",{active:E.value===t.id}])},[e("span",oo,l(t.num),1),e("span",null,l(t.label),1)],10,eo)),64))])])]),e("section",so,[o[5]||(o[5]=e("div",{class:"doc-section-head"},[e("span",{class:"doc-section-num"},"01"),e("div",null,[e("h2",{class:"doc-section-title"}," Handler contract "),e("p",{class:"doc-lede"}," One exported function receives the inbound HTTP event and returns an HTTP-shaped response. The adapter handles serialization and headers. ")])],-1)),d(n(y),{tabs:de.value,"storage-key":"docs.handler"},null,8,["tabs"]),o[6]||(o[6]=b('<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Event shape </div><div class="doc-card-body"><code class="doc-chip">method</code><code class="doc-chip">path</code><code class="doc-chip">headers</code><code class="doc-chip">query</code><code class="doc-chip">body</code></div></div><div class="doc-card"><div class="doc-microlabel"> Response </div><div class="doc-card-body"><code class="doc-chip">{ statusCode, headers, body }</code><p class="mt-1.5 text-foreground-muted"> Non-string bodies are JSON-encoded by the adapter. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Runtime env </div><div class="doc-card-body"> Env vars and secrets land in <code class="doc-chip">process.env</code> / <code class="doc-chip">os.environ</code>. </div></div></div>',1)),e("div",to,[e("table",ao,[o[4]||(o[4]=e("thead",null,[e("tr",null,[e("th",null,"Runtime"),e("th",null,"ID"),e("th",{class:"hidden sm:table-cell"}," Entrypoint "),e("th",{class:"hidden md:table-cell"}," Dependencies ")])],-1)),e("tbody",null,[(u(),g(_,null,A(ie,t=>e("tr",{key:t.id},[e("td",no,[(u(),j(Z(t.icon),{class:"shrink-0"})),a(" "+l(t.name),1)]),e("td",co,l(t.id),1),e("td",ro,l(t.entry),1),e("td",io,l(t.deps),1)])),64))])])])]),e("section",lo,[o[11]||(o[11]=b('<div class="doc-section-head"><span class="doc-section-num">02</span><div><h2 class="doc-section-title"> Deploy &amp; invoke </h2><p class="doc-lede"> The dashboard handles day-to-day work; these calls are for CI and automation. Builds run async; poll <code class="doc-chip">/api/v1/deployments/&lt;id&gt;</code> or stream <code class="doc-chip">/api/v1/deployments/&lt;id&gt;/stream</code> until <code class="doc-chip">phase: done</code>. </p></div></div>',1)),d(n(ae)),e("div",po,[e("div",uo,[o[7]||(o[7]=e("div",{class:"doc-step-label"},[e("span",{class:"doc-step-num"},"1"),a(" Create the function row ")],-1)),d(n(h),{code:pe.value,lang:"bash"},null,8,["code"])]),e("div",ho,[o[8]||(o[8]=e("div",{class:"doc-step-label"},[e("span",{class:"doc-step-num"},"2"),a(" Upload code ")],-1)),d(n(h),{code:ue.value,lang:"bash"},null,8,["code"])])]),e("div",vo,[o[9]||(o[9]=e("div",{class:"doc-microlabel"}," Invoke ",-1)),d(n(y),{tabs:re.value,"storage-key":"docs.invoke"},null,8,["tabs"])]),d(n($),{icon:n(K),title:"Custom routes"},{default:C(()=>[...o[10]||(o[10]=[a(" Attach a friendly path with ",-1),e("code",{class:"doc-chip"},"POST /api/v1/routes",-1),a(". Reserved prefixes: ",-1),e("code",{class:"doc-chip"},"/api/",-1),e("code",{class:"doc-chip"},"/fn/",-1),e("code",{class:"doc-chip"},"/mcp/",-1),e("code",{class:"doc-chip"},"/web/",-1),e("code",{class:"doc-chip"},"/_orva/",-1),a(". ",-1)])]),_:1},8,["icon"])]),e("section",mo,[o[15]||(o[15]=e("div",{class:"doc-section-head"},[e("span",{class:"doc-section-num"},"03"),e("div",null,[e("h2",{class:"doc-section-title"}," Configuration reference "),e("p",{class:"doc-lede"}," Everything below lives on the function record. Secrets are stored encrypted and only decrypt into the worker environment at spawn time. ")])],-1)),e("div",bo,[e("table",go,[o[12]||(o[12]=e("thead",null,[e("tr",null,[e("th",null,"Field"),e("th",{class:"hidden sm:table-cell"}," Purpose "),e("th",null,"Behaviour")])],-1)),e("tbody",null,[(u(),g(_,null,A(le,t=>e("tr",{key:t.field,class:"align-top"},[e("td",yo,[(u(),j(Z(t.icon),{class:P(["w-3.5 h-3.5 shrink-0",t.iconClass])},null,8,["class"])),e("code",null,l(t.field),1)]),e("td",fo,l(t.purpose),1),e("td",ko,l(t.body),1)])),64))])])]),e("div",wo,[o[13]||(o[13]=e("div",{class:"doc-microlabel"}," Set a secret ",-1)),d(n(h),{code:he.value,lang:"bash"},null,8,["code"])]),e("details",Co,[e("summary",xo,[d(n(ee),{class:"w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted"}),o[14]||(o[14]=a(" Signed-invoke recipe (HMAC, opt-in) ",-1))]),e("div",To,[d(n(h),{code:ve.value,lang:"bash"},null,8,["code"])])])]),e("section",So,[o[21]||(o[21]=b('<div class="doc-section-head"><span class="doc-section-num">04</span><div><h2 class="doc-section-title"> SDK from inside a function </h2><p class="doc-lede"> The bundled <code class="doc-chip">orva</code> module exposes three primitives every function can use without extra dependencies: a per-function key/value store, in-process calls to other Orva functions, and a fire-and-forget background job queue. Routes through the per-process internal token injected at worker spawn time. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.kv</code></div><div class="doc-card-body"><code class="doc-chip">put / get / delete / list</code><p class="mt-1.5 text-foreground-muted"> Per-function namespace on SQLite, optional TTL. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.invoke</code></div><div class="doc-card-body"><code class="doc-chip">invoke(name, payload)</code><p class="mt-1.5 text-foreground-muted"> In-process call to another function. 8-deep call cap. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.jobs</code></div><div class="doc-card-body"><code class="doc-chip">jobs.enqueue(name, payload)</code><p class="mt-1.5 text-foreground-muted"> Fire-and-forget; persisted; retried with exp backoff. </p></div></div></div>',2)),e("div",_o,[o[16]||(o[16]=e("div",{class:"doc-microlabel"}," KV: get/put with TTL ",-1)),d(n(y),{tabs:be,"storage-key":"docs.sdk.kv"}),o[17]||(o[17]=b('<p class="text-xs text-foreground-muted"> Browse / inspect / edit / delete / set keys without leaving the dashboard at <code class="doc-chip">/web/functions/&lt;name&gt;/kv</code> (or click the <code class="doc-chip">KV</code> button in the editor&#39;s action bar). REST mirror at <code class="doc-chip">GET/PUT/DELETE /api/v1/functions/&lt;id&gt;/kv[/&lt;key&gt;]</code>; MCP tools <code class="doc-chip">kv_list</code> / <code class="doc-chip">kv_get</code> / <code class="doc-chip">kv_put</code> / <code class="doc-chip">kv_delete</code> for agents. </p>',1))]),e("div",Ao,[o[18]||(o[18]=e("div",{class:"doc-microlabel"}," Function-to-function: invoke() ",-1)),d(n(y),{tabs:ge,"storage-key":"docs.sdk.invoke"})]),e("div",Oo,[o[19]||(o[19]=e("div",{class:"doc-microlabel"}," Background jobs: jobs.enqueue() ",-1)),d(n(y),{tabs:ye,"storage-key":"docs.sdk.jobs"})]),d(n($),{icon:n(K),title:"Network mode"},{default:C(()=>[...o[20]||(o[20]=[a(" The SDK reaches orvad over loopback through the host gateway, so the function needs ",-1),e("code",{class:"doc-chip"},'network_mode: "egress"',-1),a(". On the default ",-1),e("code",{class:"doc-chip"},'"none"',-1),a(" the SDK throws ",-1),e("code",{class:"doc-chip"},"OrvaUnavailableError",-1),a(" with a clear hint. ",-1)])]),_:1},8,["icon"])]),e("section",Po,[e("div",jo,[o[32]||(o[32]=e("span",{class:"doc-section-num"},"05",-1)),e("div",null,[o[31]||(o[31]=e("h2",{class:"doc-section-title"}," Schedules ",-1)),e("p",Io,[o[23]||(o[23]=a(" Fire any function on a cron expression. The scheduler runs as part of the orvad process; no external service. Manage from the ",-1)),d(i,{to:"/cron",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:C(()=>[...o[22]||(o[22]=[a("Schedules page",-1)])]),_:1}),o[24]||(o[24]=a(" or via the API. Standard 5-field cron with the usual shorthands (",-1)),o[25]||(o[25]=e("code",{class:"doc-chip"},"@daily",-1)),o[26]||(o[26]=a(", ",-1)),o[27]||(o[27]=e("code",{class:"doc-chip"},"@hourly",-1)),o[28]||(o[28]=a(", ",-1)),o[29]||(o[29]=e("code",{class:"doc-chip"},"*/5 * * * *",-1)),o[30]||(o[30]=a("). ",-1))])])]),d(n(y),{tabs:me.value,"storage-key":"docs.cron"},null,8,["tabs"]),d(n($),{icon:n($e),title:"Cron-fired headers"},{default:C(()=>[...o[33]||(o[33]=[a(" Every cron-triggered invocation arrives at the function with ",-1),e("code",{class:"doc-chip"},"x-orva-trigger: cron",-1),a(" and ",-1),e("code",{class:"doc-chip"},"x-orva-cron-id: cron_…",-1),a(" on the event headers, so user code can branch on origin. ",-1)])]),_:1},8,["icon"])]),e("section",Eo,[e("div",Do,[o[38]||(o[38]=e("span",{class:"doc-section-num"},"06",-1)),e("div",null,[o[37]||(o[37]=e("h2",{class:"doc-section-title"}," Webhooks ",-1)),e("p",Ro,[o[35]||(o[35]=a(" Operator-managed subscriptions for system events. Configure URLs from the ",-1)),d(i,{to:"/webhooks",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:C(()=>[...o[34]||(o[34]=[a("Webhooks page",-1)])]),_:1}),o[36]||(o[36]=a("; Orva delivers signed POSTs to them when matching events fire (deployments, function lifecycle, cron failures, job outcomes). Subscriptions are global, not per-function. ",-1))])])]),d(n(ce)),o[41]||(o[41]=b('<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Headers </div><div class="doc-card-body"><code class="doc-chip">X-Orva-Event</code><code class="doc-chip">X-Orva-Delivery-Id</code><code class="doc-chip">X-Orva-Timestamp</code><code class="doc-chip">X-Orva-Signature</code></div></div><div class="doc-card"><div class="doc-microlabel"> Signature </div><div class="doc-card-body"><code class="doc-chip">sha256=hex(hmac(secret, ts.body))</code><p class="mt-1.5 text-foreground-muted"> Same shape as Stripe / signed-invoke. Receivers verify with the secret returned at create time. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Retries </div><div class="doc-card-body"><code class="doc-chip">5 attempts</code><code class="doc-chip">exp backoff (≤ 1h)</code><p class="mt-1.5 text-foreground-muted"> Receiver must 2xx within 15s. </p></div></div></div>',1)),e("div",Ho,[e("table",$o,[o[39]||(o[39]=e("thead",null,[e("tr",null,[e("th",null,"Event"),e("th",null,"When it fires")])],-1)),e("tbody",null,[(u(),g(_,null,A(fe,t=>e("tr",{key:t.name},[e("td",Mo,[e("code",null,l(t.name),1)]),e("td",Lo,l(t.when),1)])),64))])])]),e("div",qo,[o[40]||(o[40]=e("div",{class:"doc-microlabel"}," Verify a delivery ",-1)),d(n(y),{tabs:ke,"storage-key":"docs.webhooks.verify"})])]),e("section",No,[o[51]||(o[51]=e("div",{class:"doc-section-head"},[e("span",{class:"doc-section-num"},"07"),e("div",null,[e("h2",{class:"doc-section-title"}," MCP: Model Context Protocol "),e("p",{class:"doc-lede"}," Same API surface the dashboard uses, exposed as 70 tools an agent can call directly. API key permissions scope the available tool set. ")])],-1)),e("div",Bo,[e("div",zo,[o[42]||(o[42]=e("div",{class:"doc-microlabel"}," Endpoint ",-1)),e("div",Ko,[e("code",Uo,l(r.value)+"/mcp",1)])]),o[43]||(o[43]=b('<div class="doc-card"><div class="doc-microlabel"> Auth header </div><div class="doc-card-body"><code class="doc-chip break-all">Authorization: Bearer &lt;token&gt;</code><p class="mt-1.5 text-foreground-muted"> Or as a fallback: <code class="doc-chip">X-Orva-API-Key: &lt;token&gt;</code></p></div></div><div class="doc-card"><div class="doc-microlabel"> Transport </div><div class="doc-card-body"><code class="doc-chip">Streamable HTTP</code><code class="doc-chip">MCP 2025-11-25</code></div></div>',2))]),d(n($),{icon:n(M),title:"Two header formats; same auth"},{default:C(()=>[...o[44]||(o[44]=[a(" Either header works against the same API key store with identical permission gating. ",-1),e("code",{class:"doc-chip"},"Authorization: Bearer",-1),a(" is the MCP / OAuth 2 spec form; every MCP SDK (Claude Code, Claude Desktop, Cursor, mcp-remote, Python ",-1),e("code",{class:"doc-chip"},"mcp",-1),a(") configures it natively, so prefer it for new setups. ",-1),e("code",{class:"doc-chip"},"X-Orva-API-Key",-1),a(" is the same header the REST API accepts; useful when a tool reuses an existing Orva REST integration. Internally both paths SHA-256-hash the token and look it up against the same ",-1),e("code",{class:"doc-chip"},"api_keys",-1),a(" table. ",-1)])]),_:1},8,["icon"]),e("div",Fo,[e("div",Xo,[d(n(M),{class:"w-4 h-4 shrink-0 text-foreground-muted"}),R.value?(u(),g("span",Go,[o[47]||(o[47]=a(" Token minted: ",-1)),e("code",Wo,l(_e.value)+"…",1),o[48]||(o[48]=a(" Shown once, copy now. ",-1))])):(u(),g("span",Vo,[o[45]||(o[45]=a(" Snippets show ",-1)),e("code",{class:"doc-chip"},l(oe)),o[46]||(o[46]=a(". Mint a token to substitute it everywhere. ",-1))]))]),e("button",{class:"doc-token-btn",disabled:H.value,onClick:Ae},[d(n(M),{class:"w-3.5 h-3.5"}),a(" "+l(R.value?"Mint another":H.value?"Minting…":"Generate token"),1)],8,Yo)]),d(n(y),{tabs:Oe.value,"storage-key":"docs.mcp.install"},null,8,["tabs"]),e("details",Jo,[e("summary",Zo,[d(n(ee),{class:"w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted"}),o[49]||(o[49]=a(" More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT, manual config) ",-1))]),e("div",Qo,[d(n(y),{tabs:Pe.value,"storage-key":"docs.mcp.install.more"},null,8,["tabs"]),o[50]||(o[50]=e("div",{class:"doc-microlabel pt-1"}," Hand-edited config files ",-1)),d(n(y),{tabs:Ie.value,"storage-key":"docs.mcp.manual"},null,8,["tabs"])])])]),e("section",es,[o[52]||(o[52]=b('<div class="doc-section-head"><span class="doc-section-num">08</span><div><h2 class="doc-section-title"> System prompt for AI assistants </h2><p class="doc-lede"> Paste the prompt below into ChatGPT, Claude, Gemini, Cursor, Copilot, or any other AI tool to teach it Orva&#39;s full surface Handler contract, runtimes, sandbox limits, the in-sandbox <code class="doc-chip">orva</code> SDK (kv / invoke / jobs), cron triggers, system-event webhooks, auth modes, and production patterns. The model then turns &quot;describe what I want&quot; into a pasteable handler on the first try. </p></div></div>',1)),e("div",os,[e("button",{class:P(["ai-copy-btn",{copied:D.value}]),onClick:te},[D.value?(u(),j(n(B),{key:0,class:"w-3.5 h-3.5"})):(u(),j(n(z),{key:1,class:"w-3.5 h-3.5"})),a(" "+l(D.value?"Copied":"Copy system prompt"),1)],2)]),e("div",{class:P(["prompt-collapse",{expanded:S.value}])},[d(n(h),{code:n(se),lang:"text"},null,8,["code"]),S.value?Me("",!0):(u(),g("div",ss))],2),e("button",{class:"prompt-expand-btn","aria-expanded":S.value,onClick:o[0]||(o[0]=t=>S.value=!S.value)},[d(n(Ke),{class:P(["w-3.5 h-3.5 transition-transform",{"rotate-180":S.value}])},null,8,["class"]),a(" "+l(S.value?"Collapse system prompt":"Expand full system prompt (~400 lines)"),1)],8,ts)]),e("section",as,[o[54]||(o[54]=b('<div class="doc-section-head"><span class="doc-section-num">09</span><div><h2 class="doc-section-title"> Tracing </h2><p class="doc-lede"> Every invocation chain is recorded as a causal trace. automatically, with <strong>zero changes to your function code</strong>. HTTP requests, F2F invokes, jobs, cron, inbound webhooks, and replays all stitch into the same tree. The dashboard renders it as a waterfall at <code class="doc-chip">/traces</code>. </p></div></div><p class="doc-prose"> Each execution row IS a span. Spans share a <code class="doc-chip">trace_id</code>; child spans point at their parent via <code class="doc-chip">parent_span_id</code>. You don&#39;t instantiate spans, you don&#39;t import a tracer; you just write your handler and the platform plumbs IDs through every internal hop. </p>',2)),d(n(ne)),o[55]||(o[55]=e("h3",{class:"doc-h3"},"What user code sees",-1)),o[56]||(o[56]=e("p",{class:"doc-prose"}," Two env vars are stamped per invocation. Read them only if you want to log the trace_id alongside your own messages; they're optional. ",-1)),d(n(h),{code:_s,lang:"text"}),o[57]||(o[57]=e("h3",{class:"doc-h3"},"Automatic propagation",-1)),o[58]||(o[58]=e("p",{class:"doc-prose"},[a(" When a function calls another via the SDK, the trace context flows through automatically. The called function becomes a child span of the caller; both share the same "),e("code",{class:"doc-chip"},"trace_id"),a(". ")],-1)),d(n(h),{code:As,lang:"js"}),o[59]||(o[59]=b('<p class="doc-prose"> Job enqueues work the same way: <code class="doc-chip">orva.jobs.enqueue()</code> records the trace context on the job row. When the scheduler picks the job up later, the resulting execution lands in the same trace as the function that enqueued it, even if the gap is hours or days. </p><h3 class="doc-h3">Triggers</h3><p class="doc-prose"> Each span carries a <code class="doc-chip">trigger</code> label so the UI can show how the chain started. </p>',3)),e("div",ns,[e("table",cs,[o[53]||(o[53]=e("thead",null,[e("tr",null,[e("th",null,"Trigger"),e("th",null,"Meaning")])],-1)),e("tbody",null,[(u(),g(_,null,A(we,t=>e("tr",{key:t.name},[e("td",ds,[e("code",null,l(t.name),1)]),e("td",rs,l(t.desc),1)])),64))])])]),o[60]||(o[60]=e("h3",{class:"doc-h3"},"External correlation (W3C traceparent)",-1)),o[61]||(o[61]=e("p",{class:"doc-prose"},[a(" Send a standard "),e("code",{class:"doc-chip"},"traceparent"),a(" header on the inbound HTTP request and Orva makes its trace a child of yours. The same trace_id is echoed back as "),e("code",{class:"doc-chip"},"X-Trace-Id"),a(" on every response, so external systems can correlate without parsing bodies. ")],-1)),d(n(h),{code:Os,lang:"bash"}),o[62]||(o[62]=b('<h3 class="doc-h3">Outlier detection</h3><p class="doc-prose"> Each function maintains an in-memory rolling P95 baseline over its last 100 successful warm executions. An invocation is flagged as an outlier when it has at least 20 baseline samples AND its duration exceeds <strong>P95 × 2</strong>. Cold starts and errors are excluded from the baseline so a flapping function can&#39;t drag it down. The flag and baseline P95 are stored on the execution row and rendered as an amber flag icon next to the span. </p><h3 class="doc-h3">Where to look</h3><ul class="doc-list"><li><code class="doc-chip">/traces</code>: list of recent traces, filterable by function / status / outlier-only.</li><li><code class="doc-chip">/traces/:id</code>: waterfall + per-span detail. Click a span to jump to its execution in the Invocations log.</li><li><code class="doc-chip">GET /api/v1/traces/{id}</code>: full span tree as JSON. Pair with <code class="doc-chip">list_traces</code> / <code class="doc-chip">get_trace</code> MCP tools for AI agents.</li><li><code class="doc-chip">GET /api/v1/functions/{id}/baseline</code>: current P95/P99/mean for a function.</li></ul>',4))]),e("section",is,[o[64]||(o[64]=b('<div class="doc-section-head"><span class="doc-section-num">10</span><div><h2 class="doc-section-title"> Errors &amp; recovery </h2><p class="doc-lede"> Every error response uses the same envelope so log scrapers and retries can match on <code class="doc-chip">code</code>. Deploys are content-addressed; rollback retargets the active version pointer and refreshes warm workers. </p></div></div>',1)),d(n(h),{code:Ps,lang:"json"}),e("div",ls,[e("table",ps,[o[63]||(o[63]=e("thead",null,[e("tr",null,[e("th",null,"Code"),e("th",null,"When you see it")])],-1)),e("tbody",null,[(u(),g(_,null,A(Ce,t=>e("tr",{key:t.code},[e("td",us,[e("code",null,l(t.code),1)]),e("td",hs,l(t.when),1)])),64))])])])]),e("section",vs,[o[81]||(o[81]=b('<div class="doc-section-head"><span class="doc-section-num">11</span><div><h2 class="doc-section-title"> CLI </h2><p class="doc-lede"><code class="doc-chip">orva</code> is a single static binary that talks to a remote (or local) Orva server over HTTPS. Same binary as the daemon, <code class="doc-chip">orva serve</code> starts a server, every other subcommand is a CLI client. Drop it on operator laptops, CI runners, or anywhere bash runs. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel">Install (server included)</div><div class="doc-card-body"><code class="doc-chip">curl … install.sh | sh</code><p class="mt-1.5 text-foreground-muted"> Full install: daemon + nsjail + rootfs + CLI. </p></div></div><div class="doc-card"><div class="doc-microlabel">Install (CLI only)</div><div class="doc-card-body"><code class="doc-chip">install.sh --cli-only</code><p class="mt-1.5 text-foreground-muted"> ~10 MB binary at <code>/usr/local/bin/orva</code>. No service. </p></div></div><div class="doc-card"><div class="doc-microlabel">Inside Docker</div><div class="doc-card-body"><code class="doc-chip">docker exec orva orva …</code><p class="mt-1.5 text-foreground-muted"> CLI auto-authed via <code>~/.orva/config.yaml</code>. </p></div></div></div><h3 class="doc-h3">Authenticate</h3>',3)),e("p",ms,[o[66]||(o[66]=a(" The CLI reads ",-1)),o[67]||(o[67]=e("code",{class:"doc-chip"},"~/.orva/config.yaml",-1)),o[68]||(o[68]=a(" for ",-1)),o[69]||(o[69]=e("code",{class:"doc-chip"},"endpoint",-1)),o[70]||(o[70]=a(" + ",-1)),o[71]||(o[71]=e("code",{class:"doc-chip"},"api_key",-1)),o[72]||(o[72]=a(". Generate a key from ",-1)),d(i,{to:"/api-keys",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:C(()=>[...o[65]||(o[65]=[a("Keys",-1)])]),_:1}),o[73]||(o[73]=a(" in the dashboard, then: ",-1))]),d(n(h),{code:js,lang:"bash"}),o[82]||(o[82]=e("h3",{class:"doc-h3"},"Command index",-1)),e("div",bs,[e("table",gs,[o[74]||(o[74]=e("thead",null,[e("tr",null,[e("th",null,"Command"),e("th",null,"Subcommands"),e("th",{class:"hidden md:table-cell"},"Purpose")])],-1)),e("tbody",null,[(u(),g(_,null,A(xe,t=>e("tr",{key:t.cmd},[e("td",ys,[e("code",null,"orva "+l(t.cmd),1)]),e("td",fs,l(t.subs),1),e("td",ks,l(t.purpose),1)])),64))])])]),o[83]||(o[83]=e("h3",{class:"doc-h3"},"Common recipes",-1)),e("div",ws,[o[75]||(o[75]=e("div",{class:"doc-microlabel"},"Deploy a function from a directory",-1)),d(n(h),{code:Is,lang:"bash"})]),e("div",Cs,[o[76]||(o[76]=e("div",{class:"doc-microlabel"},"Invoke + tail logs",-1)),d(n(h),{code:Es,lang:"bash"})]),e("div",xs,[o[77]||(o[77]=e("div",{class:"doc-microlabel"},"Manage KV state",-1)),d(n(h),{code:Ds,lang:"bash"})]),e("div",Ts,[o[78]||(o[78]=e("div",{class:"doc-microlabel"},"Secrets, cron, jobs, webhooks",-1)),d(n(h),{code:Rs,lang:"bash"})]),e("div",Ss,[o[79]||(o[79]=e("div",{class:"doc-microlabel"},"System health, metrics, vacuum",-1)),d(n(h),{code:Hs,lang:"bash"})]),d(n($),{icon:n(M),title:"Shell completion"},{default:C(()=>[...o[80]||(o[80]=[a(" Generate completion for your shell: ",-1),e("code",{class:"doc-chip"},"orva completion bash | sudo tee /etc/bash_completion.d/orva",-1),a(", or ",-1),e("code",{class:"doc-chip"},"zsh",-1),a(" / ",-1),e("code",{class:"doc-chip"},"fish",-1),a(" / ",-1),e("code",{class:"doc-chip"},"powershell",-1),a(". Tab-completes commands, subcommands, and flags. ",-1)])]),_:1},8,["icon"])])])}}};export{Ys as default};
