import{C as e,D as t,M as n,P as r,T as i,W as a,X as o,_ as s,_t as c,c as l,d as u,h as d,ht as f,j as ee,k as p,l as m,m as h,p as g,r as _,s as v,u as te,v as y}from"./runtime-core.esm-bundler-r8ENnH55.js";import{t as b}from"./check-DZzcyy_0.js";import{t as ne}from"./chevron-down-mm2PDjdZ.js";import{t as x}from"./chevron-right-mu2HsF7m.js";import{t as S}from"./copy-xDmuE7R0.js";import{t as C}from"./globe-Ch-pov5r.js";import{t as w}from"./key-round-wwm-pljA.js";import{t as re}from"./lock-z8F-Dt7a.js";import{t as ie}from"./variable-Dn44NIla.js";import{t as ae}from"./client-FR1qBAS0.js";import{Dt as oe,Et as se,gt as ce}from"./index-Dy-ijpLI.js";import{a as T,i as E,n as le,r as ue,t as D}from"./github-dark-DLS_GlXW.js";import{t as O}from"./clipboard-D_9N0yai.js";import{r as de,t as fe}from"./aiPrompts-DdGypNbE.js";function pe(e){let t=e.regex,n=`HTTP/([32]|1\\.[01])`,r={className:`attribute`,begin:t.concat(`^`,/[A-Za-z][A-Za-z0-9-]*/,`(?=\\:\\s)`),starts:{contains:[{className:`punctuation`,begin:/: /,relevance:0,starts:{end:`$`,relevance:0}}]}},i=[r,{begin:`\\n\\n`,starts:{subLanguage:[],endsWithParent:!0}}];return{name:`HTTP`,aliases:[`https`],illegal:/\S/,contains:[{begin:`^(?=HTTP/([32]|1\\.[01]) \\d{3})`,end:/$/,contains:[{className:`meta`,begin:n},{className:`number`,begin:`\\b\\d{3}\\b`}],starts:{end:/\b\B/,illegal:/\S/,contains:i}},{begin:`(?=^[A-Z]+ (.*?) HTTP/([32]|1\\.[01])$)`,end:/$/,contains:[{className:`string`,begin:` `,end:` `,excludeBegin:!0,excludeEnd:!0},{className:`meta`,begin:n},{className:`keyword`,begin:`[A-Z]+`}],starts:{end:/\b\B/,illegal:/\S/,contains:i}},e.inherit(r,{relevance:0})]}}var me={class:`space-y-12 pb-16`},he={class:`docs-hero`},ge={class:`docs-hero-content`},_e={class:`docs-hero-row`},ve={class:`docs-hero-actions`},k=[`title`,`aria-label`],A={class:`docs-hero-toc`,"aria-label":`Jump to docs section`},j=[`href`],M={class:`docs-hero-toc-num`},N={id:`handler`,class:`space-y-5 scroll-mt-6`},P={class:`doc-table-wrap`},F={class:`doc-table`},ye={class:`doc-cell-key`},be={class:`doc-cell-mono`},xe={class:`doc-cell-mono hidden sm:table-cell`},Se={class:`doc-cell-mono hidden md:table-cell`},Ce={id:`deploy`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},we={class:`grid grid-cols-1 lg:grid-cols-2 gap-3`},Te={class:`space-y-2`},Ee={class:`space-y-2`},De={class:`space-y-2`},Oe={id:`config`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},ke={class:`doc-table-wrap`},Ae={class:`doc-table`},je={class:`doc-cell-key whitespace-nowrap`},Me={class:`doc-cell-mono hidden sm:table-cell whitespace-nowrap`},Ne={class:`doc-cell-body`},Pe={class:`space-y-2`},Fe={class:`doc-details group`},Ie={class:`doc-details-summary`},Le={class:`doc-details-body`},Re={id:`sdk`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},ze={class:`space-y-2`},Be={class:`space-y-2`},Ve={class:`space-y-2`},He={id:`schedules`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},Ue={class:`doc-section-head`},We={class:`doc-lede`},Ge={id:`webhooks`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},Ke={class:`doc-section-head`},qe={class:`doc-lede`},Je={class:`doc-table-wrap`},Ye={class:`doc-table`},Xe={class:`doc-cell-key whitespace-nowrap`},Ze={class:`doc-cell-body`},Qe={class:`space-y-2`},$e={id:`mcp`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},et={class:`grid grid-cols-1 md:grid-cols-3 gap-3`},tt={class:`doc-card`},nt={class:`doc-card-body`},rt={class:`doc-chip break-all`},it={class:`doc-token-bar`},at={class:`flex items-center gap-2 min-w-0 flex-1`},ot={key:0,class:`text-sm text-foreground-muted truncate`},st={key:1,class:`text-sm text-success truncate`},ct={class:`doc-chip`},lt=[`disabled`],ut={class:`doc-details group`},dt={class:`doc-details-summary`},ft={class:`doc-details-body space-y-4`},pt={id:`generate`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},mt={class:`ai-prompt-actions`},ht={key:0,class:`prompt-collapse-fade`,"aria-hidden":`true`},gt=[`aria-expanded`],_t={id:`tracing`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},vt={class:`doc-table-wrap`},yt={class:`doc-table`},bt={class:`doc-cell-key whitespace-nowrap`},xt={class:`doc-cell-body`},St={id:`errors`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},Ct={class:`doc-table-wrap`},wt={class:`doc-table`},Tt={class:`doc-cell-key whitespace-nowrap`},Et={class:`doc-cell-body`},Dt={id:`cli`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},Ot={class:`doc-prose`},kt={class:`doc-table-wrap`},At={class:`doc-table`},jt={class:`doc-cell-key whitespace-nowrap`},Mt={class:`doc-cell-mono`},Nt={class:`doc-cell-body hidden md:table-cell`},Pt={class:`space-y-2`},Ft={class:`space-y-2`},It={class:`space-y-2`},Lt={class:`space-y-2`},Rt={class:`space-y-2`},zt=`# Available inside every running function — refresh per-invocation:
ORVA_TRACE_ID=tr_3e39f6991c66f140577c6021da7dd13b   # one per causal chain
ORVA_SPAN_ID=sp_4ceba57f6b1c982e                    # this execution

# Python:        os.environ["ORVA_TRACE_ID"]
# Node.js:       process.env.ORVA_TRACE_ID
# Reading them is optional — the platform records the trace for you.`,Bt=`// Function A — calls B via the SDK. Trace context flows automatically.
const { invoke, jobs } = require('orva')

module.exports.handler = async (event) => {
  // F2F call — B becomes a child span under A.
  const result = await invoke('send_email', { to: event.email })

  // Job enqueue — when this job runs (now or in 6 hours), the resulting
  // execution lands in the SAME trace as A.
  await jobs.enqueue('audit_log', { action: 'sent', to: event.email })

  return { statusCode: 200, body: 'ok' }
}`,Vt=`# Send the W3C traceparent header — Orva will adopt it as the trace root.
curl -H "traceparent: 00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01" \\
     https://orva.example.com/fn/myfn/

# Response always echoes:
# X-Trace-Id: tr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`,Ht=`{
  "error": {
    "code": "VALIDATION",
    "message": "name must be lowercase and dash-separated",
    "request_id": "req_abc123"
  }
}`,Ut=`# 1. Generate an API key in the dashboard (Keys page) or via the API
# 2. Tell the CLI where to find your Orva and which key to use
orva login \\
  --endpoint https://orva.example.com \\
  --api-key  orva_xxx_your_key_here

# Writes ~/.orva/config.yaml. Subsequent commands need no flags.
orva system health      # smoke test`,Wt=`# Init a project in cwd (creates orva.yaml + handler stub)
orva init

# Deploy from a directory. Auto-detects handler.ts when tsconfig.json
# is present; else uses the runtime default (handler.js / handler.py).
orva deploy ./my-fn \\
  --name    resize-image \\
  --runtime node

# Override the entrypoint explicitly:
orva deploy ./my-fn --name api --runtime python --entrypoint app.py`,Gt=`# Invoke a function by name or fn_<id>:
orva invoke resize-image --data '{"url":"https://example.com/cat.jpg"}'

# Recent executions:
orva logs resize-image

# Single execution, with stdout/stderr:
orva logs resize-image --exec-id exec_abc123

# Live tail — SSE stream, Ctrl-C to stop:
orva logs resize-image --tail`,Kt=`# List keys (optionally by prefix)
orva kv list resize-image
orva kv list resize-image --prefix user:

# Read / write / delete
orva kv get  resize-image cache:home
orva kv put  resize-image cache:home '{"hits":42}' --ttl 3600
orva kv delete resize-image cache:home`,qt=`# Secrets — encrypted at rest, injected as env vars at spawn:
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
orva webhooks inbound create --fn order-handler --signature stripe`,Jt=`orva system health        # daemon up + DB ok
orva system metrics       # JSON metrics snapshot
orva system db-stats      # on-disk breakdown (orva.db, WAL, functions/)
orva system vacuum        # rewrite SQLite to reclaim freelist pages

orva activity                          # last 50 activity rows
orva activity --tail                   # live feed (Ctrl-C)
orva activity --source mcp --limit 200 # MCP-only, last 200`,I=`<YOUR_ORVA_TOKEN>`,Yt={__name:`Docs`,setup(Yt){let Xt=ce();T.registerLanguage(`python`,ue),T.registerLanguage(`javascript`,E),T.registerLanguage(`js`,E),T.registerLanguage(`json`,le),T.registerLanguage(`bash`,D),T.registerLanguage(`shell`,D),T.registerLanguage(`sh`,D),T.registerLanguage(`http`,pe);let L=v(()=>window.location.origin),R=[{id:`handler`,num:`01`,label:`Handler`},{id:`deploy`,num:`02`,label:`Deploy`},{id:`config`,num:`03`,label:`Config`},{id:`sdk`,num:`04`,label:`SDK`},{id:`schedules`,num:`05`,label:`Schedules`},{id:`webhooks`,num:`06`,label:`Webhooks`},{id:`mcp`,num:`07`,label:`MCP`},{id:`generate`,num:`08`,label:`AI prompt`},{id:`tracing`,num:`09`,label:`Tracing`},{id:`errors`,num:`10`,label:`Errors`},{id:`cli`,num:`11`,label:`CLI`}],z=a(`handler`),B=null;i(()=>{if(typeof IntersectionObserver>`u`)return;let e=new Set;B=new IntersectionObserver(t=>{for(let n of t)n.isIntersecting?e.add(n.target.id):e.delete(n.target.id);for(let t of R)if(e.has(t.id)){z.value=t.id;break}},{rootMargin:`-20% 0px -70% 0px`,threshold:0});for(let e of R){let t=document.getElementById(e.id);t&&B.observe(t)}}),e(()=>{B&&B.disconnect()});let Zt=fe(),V=a(!1),H=null,Qt=async()=>{await de()&&(V.value=!0,clearTimeout(H),H=setTimeout(()=>{V.value=!1},1500))},$t=s({setup(){return()=>y(`svg`,{viewBox:`0 0 256 255`,width:`14`,height:`14`,xmlns:`http://www.w3.org/2000/svg`},[y(`defs`,null,[y(`linearGradient`,{id:`pyg1`,x1:`0`,y1:`0`,x2:`1`,y2:`1`},[y(`stop`,{offset:`0`,"stop-color":`#387EB8`}),y(`stop`,{offset:`1`,"stop-color":`#366994`})]),y(`linearGradient`,{id:`pyg2`,x1:`0`,y1:`0`,x2:`1`,y2:`1`},[y(`stop`,{offset:`0`,"stop-color":`#FFE052`}),y(`stop`,{offset:`1`,"stop-color":`#FFC331`})])]),y(`path`,{fill:`url(#pyg1)`,d:`M126.9 12c-58.3 0-54.7 25.3-54.7 25.3l.1 26.2H128v8H50.5S12 67.2 12 126.1c0 58.9 33.6 56.8 33.6 56.8h19.4v-27.4s-1-33.6 33.1-33.6h55.9s32 .5 32-30.9V43.5S191.7 12 126.9 12zM95.7 29.9a10 10 0 0 1 0 20 10 10 0 0 1 0-20z`}),y(`path`,{fill:`url(#pyg2)`,d:`M129.1 243c58.3 0 54.7-25.3 54.7-25.3l-.1-26.2H128v-8h77.5s38.5 4.4 38.5-54.5c0-58.9-33.6-56.8-33.6-56.8h-19.4v27.4s1 33.6-33.1 33.6H102s-32-.5-32 30.9v52S64.3 243 129.1 243zm30.4-17.9a10 10 0 0 1 0-20 10 10 0 0 1 0 20z`})])}}),en=s({setup(){return()=>y(`svg`,{viewBox:`0 0 256 280`,width:`14`,height:`14`,xmlns:`http://www.w3.org/2000/svg`},[y(`path`,{fill:`#3F873F`,d:`M128 0 12 67v146l116 67 116-67V67L128 0zm0 24.6 95 54.8v121.2l-95 54.8-95-54.8V79.4l95-54.8z`}),y(`path`,{fill:`#3F873F`,d:`M128 64c-3 0-5.7.7-8 2.3L73 92c-5 2.7-8 8-8 13.6V169c0 5.6 3 10.7 8 13.5l13 7.4c6.3 3.1 8.5 3.1 11.4 3.1 9.4 0 14.8-5.7 14.8-15.6V117c0-1-.7-1.7-1.7-1.7H103c-1 0-1.7.7-1.7 1.7v60.2c0 4.4-4.5 8.7-11.8 5.1l-13.7-7.9a1.6 1.6 0 0 1-.8-1.4v-63.4c0-.6.3-1 .8-1.4l46.8-26.9c.4-.3 1-.3 1.4 0L171 110c.5.4.8.8.8 1.4V174a1.7 1.7 0 0 1-.8 1.4l-46.8 27c-.4.2-1 .2-1.4 0l-12-7.2c-.4-.2-.8-.2-1.2 0-3.4 1.9-4 2.2-7.2 3.3-.8.3-2 .7.4 2.1l15.7 9.3c2.5 1.4 5.3 2.2 8.2 2.2 2.9 0 5.7-.8 8.2-2.2L181 184c5-2.8 8-7.9 8-13.5V107c0-5.6-3-10.7-8-13.5l-46.7-26.7a17 17 0 0 0-6.3-2.8z`})])}}),tn=s({name:`DeployPipelineDiagram`,setup(){let e=[{glyph:`▣`,label:`Tarball`,sub:`POST /deploy`},{glyph:`⟜`,label:`Extract`,sub:`untar → scratch dir`},{glyph:`◍`,label:`Install`,sub:`npm / pip`},{glyph:`⟐`,label:`Compile`,sub:`tsc (TypeScript)`},{glyph:`◉`,label:`Activate`,sub:`rename → current`},{glyph:`✦`,label:`Warm pool`,sub:`pre-spawn N workers`}];return()=>y(`figure`,{class:`doc-diagram`},[y(`figcaption`,{class:`doc-diagram-cap`},`Deploy pipeline`),y(`div`,{class:`doc-pipeline`},e.flatMap((t,n)=>{let r=y(`div`,{key:`s${n}`,class:`doc-pipeline-stage`},[y(`div`,{class:`doc-pipeline-glyph`},t.glyph),y(`div`,{class:`doc-pipeline-label`},[y(`span`,{class:`doc-pipeline-name`},t.label),y(`span`,{class:`doc-pipeline-sub`},t.sub)])]),i=n<e.length-1?y(`div`,{key:`a${n}`,class:`doc-pipeline-arrow`,"aria-hidden":`true`}):null;return i?[r,i]:[r]}))])}}),nn=s({name:`TraceTreeDiagram`,setup(){let e=[{fn:`api-gateway`,trigger:`http`,start:0,dur:220,parent:null,klass:`root`},{fn:`resize-image`,trigger:`f2f`,start:30,dur:90,parent:`api-gateway`,klass:`child`},{fn:`send-email`,trigger:`job`,start:60,dur:40,parent:`api-gateway`,klass:`grand`}],t=e=>e/220*100;return()=>y(`figure`,{class:`doc-diagram`},[y(`figcaption`,{class:`doc-diagram-cap`},`Causal trace, one HTTP request and three spans`),y(`div`,{class:`doc-trace`},[y(`div`,{class:`doc-trace-axis`},[y(`span`,null,`0 ms`),y(`span`,null,`220 ms`)]),...e.map(e=>y(`div`,{key:e.fn,class:[`doc-trace-row`,`is-${e.klass}`]},[y(`div`,{class:`doc-trace-label`},[y(`span`,{class:`doc-trace-fn`},e.fn),y(`span`,{class:`doc-trace-trigger`},e.trigger)]),y(`div`,{class:`doc-trace-track`},[y(`div`,{class:`doc-trace-bar`,style:{left:`${t(e.start)}%`,width:`${t(e.dur)}%`},title:`+${e.start}ms · ${e.dur}ms`})]),y(`div`,{class:`doc-trace-dur`},`${e.dur}ms`)])),y(`div`,{class:`doc-trace-legend`},[y(`span`,null,`Same `),y(`code`,{class:`doc-chip`},`trace_id`),y(`span`,null,` across all spans · `),y(`code`,{class:`doc-chip`},`parent_span_id`),y(`span`,null,` chains them into a tree.`)])])])}}),rn=s({name:`WebhookDeliveryDiagram`,setup(){return()=>y(`figure`,{class:`doc-diagram`},[y(`figcaption`,{class:`doc-diagram-cap`},`Signed webhook delivery`),y(`div`,{class:`doc-webhook`},[y(`div`,{class:`doc-webhook-actor`},[y(`div`,{class:`doc-webhook-actor-head`},`orvad`),y(`div`,{class:`doc-webhook-actor-body`},[y(`span`,null,`event fires`),y(`code`,{class:`doc-chip`},`deployment.succeeded`)])]),y(`div`,{class:`doc-webhook-wire`},[y(`div`,{class:`doc-webhook-wire-line`,"aria-hidden":`true`}),y(`div`,{class:`doc-webhook-wire-payload`},[y(`div`,{class:`doc-webhook-wire-method`},`POST`),y(`div`,{class:`doc-webhook-wire-headers`},[y(`code`,null,`X-Orva-Event`),y(`code`,null,`X-Orva-Timestamp`),y(`code`,null,`X-Orva-Signature`)]),y(`div`,{class:`doc-webhook-wire-sig`},`sha256=hex(hmac(secret, ts.body))`)])]),y(`div`,{class:`doc-webhook-actor`},[y(`div`,{class:`doc-webhook-actor-head`},`your receiver`),y(`div`,{class:`doc-webhook-actor-body`},[y(`span`,null,`verify HMAC`),y(`span`,null,`→ 2xx within 15s or get retried`)])])])])}}),an=v(()=>[{label:`Python`,lang:`python`,code:`def handler(event):
    body = event.get("body") or {}
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": {"hello": body.get("name", "world")},
    }`},{label:`Node.js`,lang:`js`,code:`exports.handler = async (event) => {
  const body = event.body || {};
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: { hello: body.name || 'world' },
  };
};`}]),on=v(()=>[{label:`curl`,lang:`bash`,code:`curl -X POST ${L.value}/fn/<function_id> \\
  -H 'Content-Type: application/json' \\
  -d '{"name": "Orva"}'`},{label:`fetch`,lang:`js`,code:`const res = await fetch('${L.value}/fn/<function_id>', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ name: 'Orva' }),
});
console.log(await res.json());`},{label:`Python`,lang:`python`,code:`import httpx

r = httpx.post(
    "${L.value}/fn/<function_id>",
    json={"name": "Orva"},
)
print(r.json())`}]),sn=[{id:`python`,name:`Python 3.14`,entry:`handler.py`,deps:`requirements.txt`,icon:$t},{id:`node`,name:`Node.js 24`,entry:`handler.js`,deps:`package.json`,icon:en}],cn=[{field:`env_vars`,purpose:`Plain config`,body:`Plaintext config stored on the function record. Use for feature flags and non-secret settings.`,icon:ie,iconClass:`text-violet-300`},{field:`/secrets`,purpose:`Encrypted`,body:`AES-256-GCM at rest. Values decrypt only into the worker environment at spawn time.`,icon:w,iconClass:`text-emerald-300`},{field:`network_mode`,purpose:`Egress control`,body:`none = isolated loopback. egress = outbound HTTPS allowed; firewall blocklist applies.`,icon:C,iconClass:`text-sky-300`},{field:`auth_mode`,purpose:`Invoke gate`,body:`none = public. platform_key = require Orva API key. signed = require HMAC.`,icon:re,iconClass:`text-violet-300`},{field:`rate_limit_per_min`,purpose:`Per-IP throttle`,body:`Optional cap for public or webhook-facing functions. Exceeding it returns 429.`,icon:se,iconClass:`text-amber-300`}],ln=v(()=>`curl -X POST ${L.value}/api/v1/functions \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"name":"hello","runtime":"python","memory_mb":128,"cpus":0.5}'`),un=v(()=>`tar czf code.tar.gz handler.py requirements.txt
curl -X POST ${L.value}/api/v1/functions/<function_id>/deploy \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -F code=@code.tar.gz`),dn=v(()=>`curl -X POST ${L.value}/api/v1/functions/<function_id>/secrets \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"key":"DATABASE_URL","value":"postgres://..."}'`),fn=v(()=>`# generate signature
SECRET='your-shared-secret-stored-in-function-secrets'
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')

curl -X POST ${L.value}/fn/<function_id> \\
  -H "X-Orva-Timestamp: $TS" \\
  -H "X-Orva-Signature: sha256=$SIG" \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`),pn=v(()=>[{label:`curl`,lang:`bash`,note:`Create a daily-9am schedule for an existing function. payload is delivered as the invoke body.`,code:`curl -X POST ${L.value}/api/v1/functions/<function_id>/cron \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{
    "cron_expr": "0 9 * * *",
    "enabled":   true,
    "payload":   {"task": "daily-summary"}
  }'`},{label:`Toggle / edit`,lang:`bash`,note:`PUT accepts any subset of {cron_expr, enabled, payload}; omitted fields keep their previous value. next_run_at is recomputed on expr changes.`,code:`# pause
curl -X PUT ${L.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"enabled": false}'

# change schedule
curl -X PUT ${L.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"cron_expr": "*/15 * * * *"}'`},{label:`List & delete`,lang:`bash`,note:`GET /api/v1/cron lists every schedule across functions (with function_name JOIN); per-function uses the nested route.`,code:`# all schedules
curl ${L.value}/api/v1/cron \\
  -H 'X-Orva-API-Key: <YOUR_KEY>'

# delete one
curl -X DELETE ${L.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>'`}]),mn=[{label:`Python`,lang:`python`,code:`from orva import kv

def handler(event):
    # Store with optional TTL (seconds). 0 = no expiry.
    kv.put("user:42", {"name": "Ada", "tier": "pro"}, ttl_seconds=3600)

    # Read; default returned if missing or expired.
    user = kv.get("user:42", default=None)

    # List by prefix.
    pages = kv.list(prefix="page:", limit=50)

    # Delete is idempotent.
    kv.delete("user:42")

    return {"statusCode": 200, "body": str(user)}`},{label:`Node.js`,lang:`js`,code:`const { kv } = require('orva')

exports.handler = async (event) => {
  await kv.put('user:42', { name: 'Ada', tier: 'pro' }, { ttlSeconds: 3600 })

  const user = await kv.get('user:42', null)

  const pages = await kv.list({ prefix: 'page:', limit: 50 })

  await kv.delete('user:42')

  return { statusCode: 200, body: JSON.stringify(user) }
}`}],hn=[{label:`Python`,lang:`python`,code:`from orva import invoke, OrvaError

def handler(event):
    try:
        # invoke() returns the downstream {statusCode, headers, body}.
        # body is JSON-decoded when possible.
        result = invoke("resize-image", {"url": event["body"]["url"]})
        return {"statusCode": 200, "body": result["body"]}
    except OrvaError as e:
        # 404 = function not found, 507 = call depth exceeded.
        return {"statusCode": e.status or 502, "body": str(e)}`},{label:`Node.js`,lang:`js`,code:`const { invoke, OrvaError } = require('orva')

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
}`}],gn=[{label:`Python`,lang:`python`,code:`from orva import jobs

def handler(event):
    # Fire-and-forget. Returns the job id immediately; the function
    # body runs later via the scheduler. max_attempts retries with
    # exponential backoff on 5xx / exception.
    job_id = jobs.enqueue(
        "send-welcome-email",
        {"to": event["body"]["email"]},
        max_attempts=3,
    )
    return {"statusCode": 202, "body": job_id}`},{label:`Node.js`,lang:`js`,code:`const { jobs } = require('orva')

exports.handler = async (event) => {
  const jobId = await jobs.enqueue(
    'send-welcome-email',
    { to: event.body.email },
    { maxAttempts: 3 }
  )
  return { statusCode: 202, body: jobId }
}`}],_n=[{name:`deployment.succeeded`,when:`A function build finished and the new version is active.`},{name:`deployment.failed`,when:`A build failed or was rejected.`},{name:`function.created`,when:`A new function row was created via POST /api/v1/functions.`},{name:`function.updated`,when:`A function config was edited via PUT /api/v1/functions/{id} (status flips during a deploy do NOT fire this; see deployment.*).`},{name:`function.deleted`,when:`A function was removed.`},{name:`execution.error`,when:`An invocation finished with status=error or 5xx.`},{name:`cron.failed`,when:`A scheduled run failed (bad expr, missing fn, dispatch error, or 5xx).`},{name:`job.succeeded`,when:`A queued background job finished successfully.`},{name:`job.failed`,when:`A queued job exhausted its retries (terminal failure).`}],vn=[{label:`Python`,lang:`python`,note:`Run on the receiver. Reject anything that fails verification. The signature ensures the request really came from this Orva instance.`,code:`import hmac, hashlib, time

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
    return "bad signature", 401`},{label:`Node.js`,lang:`js`,note:`Same shape as Stripe. Use timingSafeEqual to avoid sig-leak via timing.`,code:`const crypto = require('crypto')

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
})`}],U=[{name:`http`,desc:`Public HTTP request hit /fn/<id>/. Almost always a root span.`},{name:`f2f`,desc:`Another function called this one via orva.invoke(). Has a parent_span_id.`},{name:`job`,desc:`Background job runner picked up an enqueued job. Parent_span_id is whoever enqueued it.`},{name:`cron`,desc:`Scheduler fired a cron entry. Always a root span.`},{name:`inbound`,desc:`External webhook hit /webhook/{id}. Always a root span.`},{name:`replay`,desc:`Operator clicked Replay on a captured execution. Fresh trace, no link to original.`},{name:`mcp`,desc:`AI agent invoked the function via MCP invoke_function. Fresh trace.`}],yn=[{code:`VALIDATION`,when:`Bad request body or path parameter.`},{code:`UNAUTHORIZED`,when:`Missing or invalid API key / session cookie.`},{code:`NOT_FOUND`,when:`Function, deployment, or secret doesn't exist.`},{code:`RATE_LIMITED`,when:`Too many requests; check the Retry-After header.`},{code:`VERSION_GCD`,when:`Rollback target was garbage-collected.`},{code:`INSUFFICIENT_DISK`,when:`Host is below min_free_disk_mb.`}],bn=[{cmd:`login`,subs:`—`,purpose:`Save endpoint + API key to ~/.orva/config.yaml`},{cmd:`init`,subs:`—`,purpose:`Scaffold an orva.yaml in the current directory`},{cmd:`deploy`,subs:`[path]`,purpose:`Package a directory and deploy as a function`},{cmd:`invoke`,subs:`[name|id]`,purpose:`POST to /fn/<id>/ and print the response`},{cmd:`logs`,subs:`[name|id] [--tail]`,purpose:`List recent executions; --tail follows live via SSE`},{cmd:`functions`,subs:`list / get / create / delete`,purpose:`CRUD for the function registry`},{cmd:`cron`,subs:`list / create / update / delete`,purpose:`Manage cron schedules attached to functions`},{cmd:`jobs`,subs:`list / enqueue / retry / delete`,purpose:`Background queue management`},{cmd:`kv`,subs:`list / get / put / delete`,purpose:`Browse a function’s key/value store`},{cmd:`secrets`,subs:`list / set / delete`,purpose:`AES-256-GCM secrets per function`},{cmd:`webhooks`,subs:`list / create / test / delete / inbound`,purpose:`System-event subscribers + inbound triggers`},{cmd:`routes`,subs:`list / set / delete`,purpose:`Custom URL → function path mappings`},{cmd:`keys`,subs:`list / create / revoke`,purpose:`Manage API keys`},{cmd:`activity`,subs:`[--tail] [--source web|api|...]`,purpose:`Paginated activity rows; live SSE with --tail`},{cmd:`system`,subs:`health / metrics / db-stats / vacuum`,purpose:`Server diagnostics`},{cmd:`setup`,subs:`[--skip-nsjail] [--skip-rootfs]`,purpose:`Install nsjail + rootfs on a bare host`},{cmd:`serve`,subs:`[--port N]`,purpose:`Run as the server daemon (not the CLI client)`},{cmd:`completion`,subs:`bash / zsh / fish / powershell`,purpose:`Emit shell completion script`}],W=a(``);v(()=>W.value.length>0),i(async()=>{try{let e=await fetch(`/web/docs.md`,{cache:`no-cache`});e.ok&&(W.value=await e.text())}catch{}});let xn=v(()=>W.value.replaceAll(`{{ORIGIN}}`,window.location.origin)),G=a(!1),K=null,Sn=async()=>{await O(xn.value)&&(G.value=!0,clearTimeout(K),K=setTimeout(()=>{G.value=!1},1500))},q=a(!1),J=a(``),Y=a(!1),Cn=v(()=>J.value.slice(0,12)),X=v(()=>J.value||I),wn=async()=>{if(!Y.value){Y.value=!0;try{let e=new Date().toISOString().slice(0,16).replace(`T`,` `);J.value=(await ae.post(`/keys`,{name:`MCP: `+e,permissions:[`invoke`,`read`,`write`,`admin`]})).data.key}catch(e){console.error(`mint mcp key failed`,e),Xt.notify({title:`Could not mint key`,message:e?.response?.data?.error?.message||e.message||`Unknown error`,danger:!0})}finally{Y.value=!1}}},Tn=v(()=>[{label:`Claude Code`,lang:`bash`,note:"Anthropic's `claude` CLI. Restart Claude Code afterwards; `/mcp` lists Orva's 70 tools.",code:`claude mcp add --transport http --scope user orva ${L.value}/mcp --header "Authorization: Bearer ${X.value}"`},{label:`curl`,lang:`bash`,note:`Talk to MCP directly. Step 1 returns a session id (Mcp-Session-Id) that Step 2 references.`,code:`curl -sD - -X POST ${L.value}/mcp \\
  -H 'Authorization: Bearer ${X.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

curl -sX POST ${L.value}/mcp \\
  -H 'Authorization: Bearer ${X.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H 'Mcp-Session-Id: <SID>' \\
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'`}]),En=v(()=>[{label:`Claude Desktop`,lang:`json`,note:`Paste into ~/Library/Application Support/Claude/claude_desktop_config.json (macOS), %APPDATA%\\Claude\\claude_desktop_config.json (Windows), or ~/.config/Claude/claude_desktop_config.json (Linux). Restart Claude Desktop.`,code:`{
  "mcpServers": {
    "orva": {
      "url": "${L.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${X.value}"
      }
    }
  }
}`},{label:`Cursor`,lang:`bash`,note:`Open the link in your browser. Cursor pops an approval dialog and writes ~/.cursor/mcp.json.`,code:`cursor://anysphere.cursor-deeplink/mcp/install?name=orva&config=${Dn.value}`},{label:`VS Code`,lang:`bash`,note:'User-scoped install via the Copilot-MCP `code --add-mcp` flag. Pick "Workspace" at the prompt to write .vscode/mcp.json instead.',code:`code --add-mcp '{"name":"orva","type":"http","url":"${L.value}/mcp","headers":{"Authorization":"Bearer ${X.value}"}}'`},{label:`Codex CLI`,lang:`bash`,note:"OpenAI's `codex` CLI. Writes to ~/.codex/config.toml.",code:`codex mcp add --transport streamable-http orva ${L.value}/mcp --header "Authorization: Bearer ${X.value}"`},{label:`OpenCode`,lang:`bash`,note:`Interactive add. Pick "Remote", paste ${L.value}/mcp, then add the header Authorization: Bearer ${X.value}.`,code:`opencode mcp add`},{label:`Zed`,lang:`json`,note:"Zed runs MCP as stdio subprocesses, so use the `mcp-remote` bridge. Paste under context_servers in ~/.config/zed/settings.json. Restart Zed.",code:`{
  "context_servers": {
    "orva": {
      "source": "custom",
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "${L.value}/mcp",
        "--header", "Authorization:Bearer ${X.value}"
      ]
    }
  }
}`},{label:`Windsurf`,lang:`json`,note:`Paste into ~/.codeium/windsurf/mcp_config.json and reload Windsurf.`,code:`{
  "mcpServers": {
    "orva": {
      "serverUrl": "${L.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${X.value}"
      }
    }
  }
}`},{label:`claude.ai web`,lang:`text`,note:`UI-only flow. Settings → Connectors → Add custom connector. claude.ai opens an Orva login + consent popup and issues an OAuth 2.1 token automatically; no token paste required.`,code:`URL:  ${L.value}/mcp
Auth: OAuth (auto-discovered)`},{label:`ChatGPT`,lang:`text`,note:`UI-only flow. Settings → Apps & Connectors → Developer mode → Add new connector. ChatGPT discovers OIDC metadata, performs Dynamic Client Registration, and pops the Orva consent screen. No token paste required.`,code:`URL:  ${L.value}/mcp
Auth: OAuth (auto-discovered)`}]),Dn=v(()=>{let e=JSON.stringify({url:L.value+`/mcp`,headers:{Authorization:`Bearer `+X.value}});return typeof window.btoa==`function`?window.btoa(e):e}),On=v(()=>[{label:`Cursor (global)`,lang:`json`,note:`Paste into ~/.cursor/mcp.json, or .cursor/mcp.json in your project root for a per-workspace install.`,code:`{
  "mcpServers": {
    "orva": {
      "url": "${L.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${X.value}"
      }
    }
  }
}`},{label:`Cline`,lang:`json`,note:`In VS Code: open Cline → MCP icon → Configure MCP Servers. Cline writes cline_mcp_settings.json.`,code:`{
  "mcpServers": {
    "orva": {
      "url": "${L.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${X.value}"
      },
      "disabled": false
    }
  }
}`}]),Z=s({name:`CodeBlock`,props:{code:{type:String,required:!0},lang:{type:String,default:``}},setup(e){let t=a(!1),n=async()=>{await O(e.code)&&(t.value=!0,setTimeout(()=>{t.value=!1},1200))},r=v(()=>{let t=(e.lang||``).toLowerCase();if(t&&T.getLanguage(t))try{return T.highlight(e.code,{language:t,ignoreIllegals:!0}).value}catch{}return e.code.replace(/&/g,`&amp;`).replace(/</g,`&lt;`).replace(/>/g,`&gt;`)});return()=>y(`div`,{class:`codeblock`},[y(`div`,{class:`codeblock-bar`},[y(`span`,{class:`codeblock-lang`},e.lang||``),y(`button`,{class:`codeblock-copy`,onClick:n,title:`Copy code`},[t.value?y(b,{class:`w-3 h-3`}):y(S,{class:`w-3 h-3`}),t.value?`Copied`:`Copy`])]),y(`pre`,{class:`codeblock-pre`},[y(`code`,{class:`hljs language-${(e.lang||`text`).toLowerCase()}`,innerHTML:r.value})])])}}),Q=s({name:`TabbedCode`,props:{tabs:{type:Array,required:!0},storageKey:{type:String,default:``}},setup(e){let t=a((()=>{try{if(e.storageKey){let t=localStorage.getItem(e.storageKey);if(t&&e.tabs.some(e=>e.label===t))return t}}catch{}return e.tabs[0]?.label})()),n=n=>{t.value=n;try{e.storageKey&&localStorage.setItem(e.storageKey,n)}catch{}};return()=>{let r=e.tabs.find(e=>e.label===t.value)||e.tabs[0];return y(`div`,{class:`tabbed`},[y(`div`,{class:`tabbed-tabs`},e.tabs.map(e=>y(`button`,{key:e.label,class:[`tabbed-tab`,{active:e.label===t.value}],onClick:()=>n(e.label)},e.label))),r.note?y(`div`,{class:`tabbed-note`},r.note):null,y(Z,{code:r.code,lang:r.lang})])}}}),$=s({name:`Callout`,props:{title:{type:String,default:``},icon:{type:[Object,Function],default:null}},setup(e,{slots:t}){return()=>y(`div`,{class:`callout`},[y(`div`,{class:`callout-head`},[e.icon?y(e.icon,{class:`callout-icon`}):null,e.title?y(`span`,null,e.title):null]),y(`div`,{class:`callout-body`},t.default?.())])}});return(e,i)=>{let a=ee(`router-link`);return t(),u(`div`,me,[l(`header`,he,[i[3]||=l(`div`,{class:`docs-hero-bg`,"aria-hidden":`true`},null,-1),l(`div`,ge,[l(`div`,_e,[i[1]||=l(`div`,{class:`docs-hero-text`},[l(`h1`,{class:`docs-hero-title`},` Documentation `),l(`p`,{class:`docs-hero-sub`},` Everything you need to write, deploy, and operate functions on Orva. Handler contract, deploy + invoke, SDK, MCP, tracing, error taxonomy. `)],-1),l(`div`,ve,[l(`button`,{class:f([`docs-hero-copy-icon`,{copied:G.value}]),title:G.value?`Copied`:`Copy entire docs page as Markdown`,"aria-label":G.value?`Markdown copied to clipboard`:`Copy entire docs page as Markdown`,onClick:Sn},[G.value?(t(),m(o(b),{key:0,class:`w-4 h-4`})):(t(),m(o(S),{key:1,class:`w-4 h-4`}))],10,k)])]),l(`nav`,A,[i[2]||=l(`span`,{class:`docs-hero-toc-label`},`Jump to`,-1),(t(),u(_,null,p(R,e=>l(`a`,{key:e.id,href:`#${e.id}`,class:f([`docs-hero-toc-link`,{active:z.value===e.id}])},[l(`span`,M,c(e.num),1),l(`span`,null,c(e.label),1)],10,j)),64))])])]),l(`section`,N,[i[5]||=l(`div`,{class:`doc-section-head`},[l(`span`,{class:`doc-section-num`},`01`),l(`div`,null,[l(`h2`,{class:`doc-section-title`},` Handler contract `),l(`p`,{class:`doc-lede`},` One exported function receives the inbound HTTP event and returns an HTTP-shaped response. The adapter handles serialization and headers. `)])],-1),d(o(Q),{tabs:an.value,"storage-key":`docs.handler`},null,8,[`tabs`]),i[6]||=g(`<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Event shape </div><div class="doc-card-body"><code class="doc-chip">method</code><code class="doc-chip">path</code><code class="doc-chip">headers</code><code class="doc-chip">query</code><code class="doc-chip">body</code></div></div><div class="doc-card"><div class="doc-microlabel"> Response </div><div class="doc-card-body"><code class="doc-chip">{ statusCode, headers, body }</code><p class="mt-1.5 text-foreground-muted"> Non-string bodies are JSON-encoded by the adapter. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Runtime env </div><div class="doc-card-body"> Env vars and secrets land in <code class="doc-chip">process.env</code> / <code class="doc-chip">os.environ</code>. </div></div></div>`,1),l(`div`,P,[l(`table`,F,[i[4]||=l(`thead`,null,[l(`tr`,null,[l(`th`,null,`Runtime`),l(`th`,null,`ID`),l(`th`,{class:`hidden sm:table-cell`},` Entrypoint `),l(`th`,{class:`hidden md:table-cell`},` Dependencies `)])],-1),l(`tbody`,null,[(t(),u(_,null,p(sn,e=>l(`tr`,{key:e.id},[l(`td`,ye,[(t(),m(n(e.icon),{class:`shrink-0`})),h(` `+c(e.name),1)]),l(`td`,be,c(e.id),1),l(`td`,xe,c(e.entry),1),l(`td`,Se,c(e.deps),1)])),64))])])])]),l(`section`,Ce,[i[11]||=g(`<div class="doc-section-head"><span class="doc-section-num">02</span><div><h2 class="doc-section-title"> Deploy &amp; invoke </h2><p class="doc-lede"> The dashboard handles day-to-day work; these calls are for CI and automation. Builds run async; poll <code class="doc-chip">/api/v1/deployments/&lt;id&gt;</code> or stream <code class="doc-chip">/api/v1/deployments/&lt;id&gt;/stream</code> until <code class="doc-chip">phase: done</code>. </p></div></div>`,1),d(o(tn)),l(`div`,we,[l(`div`,Te,[i[7]||=l(`div`,{class:`doc-step-label`},[l(`span`,{class:`doc-step-num`},`1`),h(` Create the function row `)],-1),d(o(Z),{code:ln.value,lang:`bash`},null,8,[`code`])]),l(`div`,Ee,[i[8]||=l(`div`,{class:`doc-step-label`},[l(`span`,{class:`doc-step-num`},`2`),h(` Upload code `)],-1),d(o(Z),{code:un.value,lang:`bash`},null,8,[`code`])])]),l(`div`,De,[i[9]||=l(`div`,{class:`doc-microlabel`},` Invoke `,-1),d(o(Q),{tabs:on.value,"storage-key":`docs.invoke`},null,8,[`tabs`])]),d(o($),{icon:o(C),title:`Custom routes`},{default:r(()=>[...i[10]||=[h(` Attach a friendly path with `,-1),l(`code`,{class:`doc-chip`},`POST /api/v1/routes`,-1),h(`. Reserved prefixes: `,-1),l(`code`,{class:`doc-chip`},`/api/`,-1),l(`code`,{class:`doc-chip`},`/fn/`,-1),l(`code`,{class:`doc-chip`},`/mcp/`,-1),l(`code`,{class:`doc-chip`},`/web/`,-1),l(`code`,{class:`doc-chip`},`/_orva/`,-1),h(`. `,-1)]]),_:1},8,[`icon`])]),l(`section`,Oe,[i[15]||=l(`div`,{class:`doc-section-head`},[l(`span`,{class:`doc-section-num`},`03`),l(`div`,null,[l(`h2`,{class:`doc-section-title`},` Configuration reference `),l(`p`,{class:`doc-lede`},` Everything below lives on the function record. Secrets are stored encrypted and only decrypt into the worker environment at spawn time. `)])],-1),l(`div`,ke,[l(`table`,Ae,[i[12]||=l(`thead`,null,[l(`tr`,null,[l(`th`,null,`Field`),l(`th`,{class:`hidden sm:table-cell`},` Purpose `),l(`th`,null,`Behaviour`)])],-1),l(`tbody`,null,[(t(),u(_,null,p(cn,e=>l(`tr`,{key:e.field,class:`align-top`},[l(`td`,je,[(t(),m(n(e.icon),{class:f([`w-3.5 h-3.5 shrink-0`,e.iconClass])},null,8,[`class`])),l(`code`,null,c(e.field),1)]),l(`td`,Me,c(e.purpose),1),l(`td`,Ne,c(e.body),1)])),64))])])]),l(`div`,Pe,[i[13]||=l(`div`,{class:`doc-microlabel`},` Set a secret `,-1),d(o(Z),{code:dn.value,lang:`bash`},null,8,[`code`])]),l(`details`,Fe,[l(`summary`,Ie,[d(o(x),{class:`w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted`}),i[14]||=h(` Signed-invoke recipe (HMAC, opt-in) `,-1)]),l(`div`,Le,[d(o(Z),{code:fn.value,lang:`bash`},null,8,[`code`])])])]),l(`section`,Re,[i[21]||=g(`<div class="doc-section-head"><span class="doc-section-num">04</span><div><h2 class="doc-section-title"> SDK from inside a function </h2><p class="doc-lede"> The bundled <code class="doc-chip">orva</code> module exposes three primitives every function can use without extra dependencies: a per-function key/value store, in-process calls to other Orva functions, and a fire-and-forget background job queue. Routes through the per-process internal token injected at worker spawn time. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.kv</code></div><div class="doc-card-body"><code class="doc-chip">put / get / delete / list</code><p class="mt-1.5 text-foreground-muted"> Per-function namespace on SQLite, optional TTL. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.invoke</code></div><div class="doc-card-body"><code class="doc-chip">invoke(name, payload)</code><p class="mt-1.5 text-foreground-muted"> In-process call to another function. 8-deep call cap. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.jobs</code></div><div class="doc-card-body"><code class="doc-chip">jobs.enqueue(name, payload)</code><p class="mt-1.5 text-foreground-muted"> Fire-and-forget; persisted; retried with exp backoff. </p></div></div></div>`,2),l(`div`,ze,[i[16]||=l(`div`,{class:`doc-microlabel`},` KV: get/put with TTL `,-1),d(o(Q),{tabs:mn,"storage-key":`docs.sdk.kv`}),i[17]||=g(`<p class="text-xs text-foreground-muted"> Browse / inspect / edit / delete / set keys without leaving the dashboard at <code class="doc-chip">/web/functions/&lt;name&gt;/kv</code> (or click the <code class="doc-chip">KV</code> button in the editor&#39;s action bar). REST mirror at <code class="doc-chip">GET/PUT/DELETE /api/v1/functions/&lt;id&gt;/kv[/&lt;key&gt;]</code>; MCP tools <code class="doc-chip">kv_list</code> / <code class="doc-chip">kv_get</code> / <code class="doc-chip">kv_put</code> / <code class="doc-chip">kv_delete</code> for agents. </p>`,1)]),l(`div`,Be,[i[18]||=l(`div`,{class:`doc-microlabel`},` Function-to-function: invoke() `,-1),d(o(Q),{tabs:hn,"storage-key":`docs.sdk.invoke`})]),l(`div`,Ve,[i[19]||=l(`div`,{class:`doc-microlabel`},` Background jobs: jobs.enqueue() `,-1),d(o(Q),{tabs:gn,"storage-key":`docs.sdk.jobs`})]),d(o($),{icon:o(C),title:`Network mode`},{default:r(()=>[...i[20]||=[h(` The SDK reaches orvad over loopback through the host gateway, so the function needs `,-1),l(`code`,{class:`doc-chip`},`network_mode: "egress"`,-1),h(`. On the default `,-1),l(`code`,{class:`doc-chip`},`"none"`,-1),h(` the SDK throws `,-1),l(`code`,{class:`doc-chip`},`OrvaUnavailableError`,-1),h(` with a clear hint. `,-1)]]),_:1},8,[`icon`])]),l(`section`,He,[l(`div`,Ue,[i[32]||=l(`span`,{class:`doc-section-num`},`05`,-1),l(`div`,null,[i[31]||=l(`h2`,{class:`doc-section-title`},` Schedules `,-1),l(`p`,We,[i[23]||=h(` Fire any function on a cron expression. The scheduler runs as part of the orvad process; no external service. Manage from the `,-1),d(a,{to:`/cron`,class:`text-foreground hover:text-white underline decoration-dotted underline-offset-4`},{default:r(()=>[...i[22]||=[h(`Schedules page`,-1)]]),_:1}),i[24]||=h(` or via the API. Standard 5-field cron with the usual shorthands (`,-1),i[25]||=l(`code`,{class:`doc-chip`},`@daily`,-1),i[26]||=h(`, `,-1),i[27]||=l(`code`,{class:`doc-chip`},`@hourly`,-1),i[28]||=h(`, `,-1),i[29]||=l(`code`,{class:`doc-chip`},`*/5 * * * *`,-1),i[30]||=h(`). `,-1)])])]),d(o(Q),{tabs:pn.value,"storage-key":`docs.cron`},null,8,[`tabs`]),d(o($),{icon:o(oe),title:`Cron-fired headers`},{default:r(()=>[...i[33]||=[h(` Every cron-triggered invocation arrives at the function with `,-1),l(`code`,{class:`doc-chip`},`x-orva-trigger: cron`,-1),h(` and `,-1),l(`code`,{class:`doc-chip`},`x-orva-cron-id: cron_…`,-1),h(` on the event headers, so user code can branch on origin. `,-1)]]),_:1},8,[`icon`])]),l(`section`,Ge,[l(`div`,Ke,[i[38]||=l(`span`,{class:`doc-section-num`},`06`,-1),l(`div`,null,[i[37]||=l(`h2`,{class:`doc-section-title`},` Webhooks `,-1),l(`p`,qe,[i[35]||=h(` Operator-managed subscriptions for system events. Configure URLs from the `,-1),d(a,{to:`/webhooks`,class:`text-foreground hover:text-white underline decoration-dotted underline-offset-4`},{default:r(()=>[...i[34]||=[h(`Webhooks page`,-1)]]),_:1}),i[36]||=h(`; Orva delivers signed POSTs to them when matching events fire (deployments, function lifecycle, cron failures, job outcomes). Subscriptions are global, not per-function. `,-1)])])]),d(o(rn)),i[41]||=g(`<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Headers </div><div class="doc-card-body"><code class="doc-chip">X-Orva-Event</code><code class="doc-chip">X-Orva-Delivery-Id</code><code class="doc-chip">X-Orva-Timestamp</code><code class="doc-chip">X-Orva-Signature</code></div></div><div class="doc-card"><div class="doc-microlabel"> Signature </div><div class="doc-card-body"><code class="doc-chip">sha256=hex(hmac(secret, ts.body))</code><p class="mt-1.5 text-foreground-muted"> Same shape as Stripe / signed-invoke. Receivers verify with the secret returned at create time. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Retries </div><div class="doc-card-body"><code class="doc-chip">5 attempts</code><code class="doc-chip">exp backoff (≤ 1h)</code><p class="mt-1.5 text-foreground-muted"> Receiver must 2xx within 15s. </p></div></div></div>`,1),l(`div`,Je,[l(`table`,Ye,[i[39]||=l(`thead`,null,[l(`tr`,null,[l(`th`,null,`Event`),l(`th`,null,`When it fires`)])],-1),l(`tbody`,null,[(t(),u(_,null,p(_n,e=>l(`tr`,{key:e.name},[l(`td`,Xe,[l(`code`,null,c(e.name),1)]),l(`td`,Ze,c(e.when),1)])),64))])])]),l(`div`,Qe,[i[40]||=l(`div`,{class:`doc-microlabel`},` Verify a delivery `,-1),d(o(Q),{tabs:vn,"storage-key":`docs.webhooks.verify`})])]),l(`section`,$e,[i[51]||=l(`div`,{class:`doc-section-head`},[l(`span`,{class:`doc-section-num`},`07`),l(`div`,null,[l(`h2`,{class:`doc-section-title`},` MCP: Model Context Protocol `),l(`p`,{class:`doc-lede`},` Same API surface the dashboard uses, exposed as 70 tools an agent can call directly. API key permissions scope the available tool set. `)])],-1),l(`div`,et,[l(`div`,tt,[i[42]||=l(`div`,{class:`doc-microlabel`},` Endpoint `,-1),l(`div`,nt,[l(`code`,rt,c(L.value)+`/mcp`,1)])]),i[43]||=g(`<div class="doc-card"><div class="doc-microlabel"> Auth header </div><div class="doc-card-body"><code class="doc-chip break-all">Authorization: Bearer &lt;token&gt;</code><p class="mt-1.5 text-foreground-muted"> Or as a fallback: <code class="doc-chip">X-Orva-API-Key: &lt;token&gt;</code></p></div></div><div class="doc-card"><div class="doc-microlabel"> Transport </div><div class="doc-card-body"><code class="doc-chip">Streamable HTTP</code><code class="doc-chip">MCP 2025-11-25</code></div></div>`,2)]),d(o($),{icon:o(w),title:`Two header formats; same auth`},{default:r(()=>[...i[44]||=[h(` Either header works against the same API key store with identical permission gating. `,-1),l(`code`,{class:`doc-chip`},`Authorization: Bearer`,-1),h(` is the MCP / OAuth 2 spec form; every MCP SDK (Claude Code, Claude Desktop, Cursor, mcp-remote, Python `,-1),l(`code`,{class:`doc-chip`},`mcp`,-1),h(`) configures it natively, so prefer it for new setups. `,-1),l(`code`,{class:`doc-chip`},`X-Orva-API-Key`,-1),h(` is the same header the REST API accepts; useful when a tool reuses an existing Orva REST integration. Internally both paths SHA-256-hash the token and look it up against the same `,-1),l(`code`,{class:`doc-chip`},`api_keys`,-1),h(` table. `,-1)]]),_:1},8,[`icon`]),l(`div`,it,[l(`div`,at,[d(o(w),{class:`w-4 h-4 shrink-0 text-foreground-muted`}),J.value?(t(),u(`span`,st,[i[47]||=h(` Token minted: `,-1),l(`code`,ct,c(Cn.value)+`…`,1),i[48]||=h(` Shown once, copy now. `,-1)])):(t(),u(`span`,ot,[i[45]||=h(` Snippets show `,-1),l(`code`,{class:`doc-chip`},c(I)),i[46]||=h(`. Mint a token to substitute it everywhere. `,-1)]))]),l(`button`,{class:`doc-token-btn`,disabled:Y.value,onClick:wn},[d(o(w),{class:`w-3.5 h-3.5`}),h(` `+c(J.value?`Mint another`:Y.value?`Minting…`:`Generate token`),1)],8,lt)]),d(o(Q),{tabs:Tn.value,"storage-key":`docs.mcp.install`},null,8,[`tabs`]),l(`details`,ut,[l(`summary`,dt,[d(o(x),{class:`w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted`}),i[49]||=h(` More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT, manual config) `,-1)]),l(`div`,ft,[d(o(Q),{tabs:En.value,"storage-key":`docs.mcp.install.more`},null,8,[`tabs`]),i[50]||=l(`div`,{class:`doc-microlabel pt-1`},` Hand-edited config files `,-1),d(o(Q),{tabs:On.value,"storage-key":`docs.mcp.manual`},null,8,[`tabs`])])])]),l(`section`,pt,[i[52]||=g(`<div class="doc-section-head"><span class="doc-section-num">08</span><div><h2 class="doc-section-title"> System prompt for AI assistants </h2><p class="doc-lede"> Paste the prompt below into ChatGPT, Claude, Gemini, Cursor, Copilot, or any other AI tool to teach it Orva&#39;s full surface Handler contract, runtimes, sandbox limits, the in-sandbox <code class="doc-chip">orva</code> SDK (kv / invoke / jobs), cron triggers, system-event webhooks, auth modes, and production patterns. The model then turns &quot;describe what I want&quot; into a pasteable handler on the first try. </p></div></div>`,1),l(`div`,mt,[l(`button`,{class:f([`ai-copy-btn`,{copied:V.value}]),onClick:Qt},[V.value?(t(),m(o(b),{key:0,class:`w-3.5 h-3.5`})):(t(),m(o(S),{key:1,class:`w-3.5 h-3.5`})),h(` `+c(V.value?`Copied`:`Copy system prompt`),1)],2)]),l(`div`,{class:f([`prompt-collapse`,{expanded:q.value}])},[d(o(Z),{code:o(Zt),lang:`text`},null,8,[`code`]),q.value?te(``,!0):(t(),u(`div`,ht))],2),l(`button`,{class:`prompt-expand-btn`,"aria-expanded":q.value,onClick:i[0]||=e=>q.value=!q.value},[d(o(ne),{class:f([`w-3.5 h-3.5 transition-transform`,{"rotate-180":q.value}])},null,8,[`class`]),h(` `+c(q.value?`Collapse system prompt`:`Expand full system prompt (~400 lines)`),1)],8,gt)]),l(`section`,_t,[i[54]||=g(`<div class="doc-section-head"><span class="doc-section-num">09</span><div><h2 class="doc-section-title"> Tracing </h2><p class="doc-lede"> Every invocation chain is recorded as a causal trace. automatically, with <strong>zero changes to your function code</strong>. HTTP requests, F2F invokes, jobs, cron, inbound webhooks, and replays all stitch into the same tree. The dashboard renders it as a waterfall at <code class="doc-chip">/traces</code>. </p></div></div><p class="doc-prose"> Each execution row IS a span. Spans share a <code class="doc-chip">trace_id</code>; child spans point at their parent via <code class="doc-chip">parent_span_id</code>. You don&#39;t instantiate spans, you don&#39;t import a tracer; you just write your handler and the platform plumbs IDs through every internal hop. </p>`,2),d(o(nn)),i[55]||=l(`h3`,{class:`doc-h3`},`What user code sees`,-1),i[56]||=l(`p`,{class:`doc-prose`},` Two env vars are stamped per invocation. Read them only if you want to log the trace_id alongside your own messages; they're optional. `,-1),d(o(Z),{code:zt,lang:`text`}),i[57]||=l(`h3`,{class:`doc-h3`},`Automatic propagation`,-1),i[58]||=l(`p`,{class:`doc-prose`},[h(` When a function calls another via the SDK, the trace context flows through automatically. The called function becomes a child span of the caller; both share the same `),l(`code`,{class:`doc-chip`},`trace_id`),h(`. `)],-1),d(o(Z),{code:Bt,lang:`js`}),i[59]||=g(`<p class="doc-prose"> Job enqueues work the same way: <code class="doc-chip">orva.jobs.enqueue()</code> records the trace context on the job row. When the scheduler picks the job up later, the resulting execution lands in the same trace as the function that enqueued it, even if the gap is hours or days. </p><h3 class="doc-h3">Triggers</h3><p class="doc-prose"> Each span carries a <code class="doc-chip">trigger</code> label so the UI can show how the chain started. </p>`,3),l(`div`,vt,[l(`table`,yt,[i[53]||=l(`thead`,null,[l(`tr`,null,[l(`th`,null,`Trigger`),l(`th`,null,`Meaning`)])],-1),l(`tbody`,null,[(t(),u(_,null,p(U,e=>l(`tr`,{key:e.name},[l(`td`,bt,[l(`code`,null,c(e.name),1)]),l(`td`,xt,c(e.desc),1)])),64))])])]),i[60]||=l(`h3`,{class:`doc-h3`},`External correlation (W3C traceparent)`,-1),i[61]||=l(`p`,{class:`doc-prose`},[h(` Send a standard `),l(`code`,{class:`doc-chip`},`traceparent`),h(` header on the inbound HTTP request and Orva makes its trace a child of yours. The same trace_id is echoed back as `),l(`code`,{class:`doc-chip`},`X-Trace-Id`),h(` on every response, so external systems can correlate without parsing bodies. `)],-1),d(o(Z),{code:Vt,lang:`bash`}),i[62]||=g(`<h3 class="doc-h3">Outlier detection</h3><p class="doc-prose"> Each function maintains an in-memory rolling P95 baseline over its last 100 successful warm executions. An invocation is flagged as an outlier when it has at least 20 baseline samples AND its duration exceeds <strong>P95 × 2</strong>. Cold starts and errors are excluded from the baseline so a flapping function can&#39;t drag it down. The flag and baseline P95 are stored on the execution row and rendered as an amber flag icon next to the span. </p><h3 class="doc-h3">Where to look</h3><ul class="doc-list"><li><code class="doc-chip">/traces</code>: list of recent traces, filterable by function / status / outlier-only.</li><li><code class="doc-chip">/traces/:id</code>: waterfall + per-span detail. Click a span to jump to its execution in the Invocations log.</li><li><code class="doc-chip">GET /api/v1/traces/{id}</code>: full span tree as JSON. Pair with <code class="doc-chip">list_traces</code> / <code class="doc-chip">get_trace</code> MCP tools for AI agents.</li><li><code class="doc-chip">GET /api/v1/functions/{id}/baseline</code>: current P95/P99/mean for a function.</li></ul>`,4)]),l(`section`,St,[i[64]||=g(`<div class="doc-section-head"><span class="doc-section-num">10</span><div><h2 class="doc-section-title"> Errors &amp; recovery </h2><p class="doc-lede"> Every error response uses the same envelope so log scrapers and retries can match on <code class="doc-chip">code</code>. Deploys are content-addressed; rollback retargets the active version pointer and refreshes warm workers. </p></div></div>`,1),d(o(Z),{code:Ht,lang:`json`}),l(`div`,Ct,[l(`table`,wt,[i[63]||=l(`thead`,null,[l(`tr`,null,[l(`th`,null,`Code`),l(`th`,null,`When you see it`)])],-1),l(`tbody`,null,[(t(),u(_,null,p(yn,e=>l(`tr`,{key:e.code},[l(`td`,Tt,[l(`code`,null,c(e.code),1)]),l(`td`,Et,c(e.when),1)])),64))])])])]),l(`section`,Dt,[i[81]||=g(`<div class="doc-section-head"><span class="doc-section-num">11</span><div><h2 class="doc-section-title"> CLI </h2><p class="doc-lede"><code class="doc-chip">orva</code> is a single static binary that talks to a remote (or local) Orva server over HTTPS. Same binary as the daemon, <code class="doc-chip">orva serve</code> starts a server, every other subcommand is a CLI client. Drop it on operator laptops, CI runners, or anywhere bash runs. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel">Install (server included)</div><div class="doc-card-body"><code class="doc-chip">curl … install.sh | sh</code><p class="mt-1.5 text-foreground-muted"> Full install: daemon + nsjail + rootfs + CLI. </p></div></div><div class="doc-card"><div class="doc-microlabel">Install (CLI only)</div><div class="doc-card-body"><code class="doc-chip">install.sh --cli-only</code><p class="mt-1.5 text-foreground-muted"> ~10 MB binary at <code>/usr/local/bin/orva</code>. No service. </p></div></div><div class="doc-card"><div class="doc-microlabel">Inside Docker</div><div class="doc-card-body"><code class="doc-chip">docker exec orva orva …</code><p class="mt-1.5 text-foreground-muted"> CLI auto-authed via <code>~/.orva/config.yaml</code>. </p></div></div></div><h3 class="doc-h3">Authenticate</h3>`,3),l(`p`,Ot,[i[66]||=h(` The CLI reads `,-1),i[67]||=l(`code`,{class:`doc-chip`},`~/.orva/config.yaml`,-1),i[68]||=h(` for `,-1),i[69]||=l(`code`,{class:`doc-chip`},`endpoint`,-1),i[70]||=h(` + `,-1),i[71]||=l(`code`,{class:`doc-chip`},`api_key`,-1),i[72]||=h(`. Generate a key from `,-1),d(a,{to:`/api-keys`,class:`text-foreground hover:text-white underline decoration-dotted underline-offset-4`},{default:r(()=>[...i[65]||=[h(`Keys`,-1)]]),_:1}),i[73]||=h(` in the dashboard, then: `,-1)]),d(o(Z),{code:Ut,lang:`bash`}),i[82]||=l(`h3`,{class:`doc-h3`},`Command index`,-1),l(`div`,kt,[l(`table`,At,[i[74]||=l(`thead`,null,[l(`tr`,null,[l(`th`,null,`Command`),l(`th`,null,`Subcommands`),l(`th`,{class:`hidden md:table-cell`},`Purpose`)])],-1),l(`tbody`,null,[(t(),u(_,null,p(bn,e=>l(`tr`,{key:e.cmd},[l(`td`,jt,[l(`code`,null,`orva `+c(e.cmd),1)]),l(`td`,Mt,c(e.subs),1),l(`td`,Nt,c(e.purpose),1)])),64))])])]),i[83]||=l(`h3`,{class:`doc-h3`},`Common recipes`,-1),l(`div`,Pt,[i[75]||=l(`div`,{class:`doc-microlabel`},`Deploy a function from a directory`,-1),d(o(Z),{code:Wt,lang:`bash`})]),l(`div`,Ft,[i[76]||=l(`div`,{class:`doc-microlabel`},`Invoke + tail logs`,-1),d(o(Z),{code:Gt,lang:`bash`})]),l(`div`,It,[i[77]||=l(`div`,{class:`doc-microlabel`},`Manage KV state`,-1),d(o(Z),{code:Kt,lang:`bash`})]),l(`div`,Lt,[i[78]||=l(`div`,{class:`doc-microlabel`},`Secrets, cron, jobs, webhooks`,-1),d(o(Z),{code:qt,lang:`bash`})]),l(`div`,Rt,[i[79]||=l(`div`,{class:`doc-microlabel`},`System health, metrics, vacuum`,-1),d(o(Z),{code:Jt,lang:`bash`})]),d(o($),{icon:o(w),title:`Shell completion`},{default:r(()=>[...i[80]||=[h(` Generate completion for your shell: `,-1),l(`code`,{class:`doc-chip`},`orva completion bash | sudo tee /etc/bash_completion.d/orva`,-1),h(`, or `,-1),l(`code`,{class:`doc-chip`},`zsh`,-1),h(` / `,-1),l(`code`,{class:`doc-chip`},`fish`,-1),h(` / `,-1),l(`code`,{class:`doc-chip`},`powershell`,-1),h(`. Tab-completes commands, subcommands, and flags. `,-1)]]),_:1},8,[`icon`])])])}}};export{Yt as default};