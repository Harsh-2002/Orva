import{C as e,D as t,F as n,G as r,M as i,T as a,Z as o,_ as s,c,d as l,gt as u,h as d,j as ee,k as f,l as p,m,p as h,r as g,s as _,u as te,v,vt as y}from"./runtime-core.esm-bundler-BMQPyJ_W.js";import{t as b}from"./check-BNre7JFR.js";import{t as x}from"./chevron-right-CRxFsA9Y.js";import{t as S}from"./copy-BqdwwcxC.js";import{t as C}from"./globe-BnBar2lO.js";import{t as w}from"./key-round-BKXtbC85.js";import{t as T}from"./lock-CttMBTH5.js";import{t as ne}from"./variable-DrK2KZuk.js";import{t as re}from"./client-BF51V3uE.js";import{Dt as ie,Et as ae,Ot as oe,gt as se}from"./index-DTqMKlE1.js";import{a as E,i as D,n as ce,r as le,t as O}from"./github-dark-D7LxIVih.js";import{t as k}from"./clipboard-D_9N0yai.js";import{r as ue,t as de}from"./aiPrompts-XrsFCpj_.js";function fe(e){let t=e.regex,n=`HTTP/([32]|1\\.[01])`,r={className:`attribute`,begin:t.concat(`^`,/[A-Za-z][A-Za-z0-9-]*/,`(?=\\:\\s)`),starts:{contains:[{className:`punctuation`,begin:/: /,relevance:0,starts:{end:`$`,relevance:0}}]}},i=[r,{begin:`\\n\\n`,starts:{subLanguage:[],endsWithParent:!0}}];return{name:`HTTP`,aliases:[`https`],illegal:/\S/,contains:[{begin:`^(?=HTTP/([32]|1\\.[01]) \\d{3})`,end:/$/,contains:[{className:`meta`,begin:n},{className:`number`,begin:`\\b\\d{3}\\b`}],starts:{end:/\b\B/,illegal:/\S/,contains:i}},{begin:`(?=^[A-Z]+ (.*?) HTTP/([32]|1\\.[01])$)`,end:/$/,contains:[{className:`string`,begin:` `,end:` `,excludeBegin:!0,excludeEnd:!0},{className:`meta`,begin:n},{className:`keyword`,begin:`[A-Z]+`}],starts:{end:/\b\B/,illegal:/\S/,contains:i}},e.inherit(r,{relevance:0})]}}var pe={class:`space-y-12 pb-16`},me={class:`docs-hero`},he={class:`docs-hero-content`},ge={class:`docs-hero-row`},_e={class:`docs-hero-actions`},ve=[`title`,`aria-label`],A={class:`docs-hero-toc`,"aria-label":`Jump to docs section`},j=[`href`],M={class:`docs-hero-toc-num`},N={id:`handler`,class:`space-y-5 scroll-mt-6`},P={class:`doc-table-wrap`},F={class:`doc-table`},ye={class:`doc-cell-key`},be={class:`doc-cell-mono`},xe={class:`doc-cell-mono hidden sm:table-cell`},Se={class:`doc-cell-mono hidden md:table-cell`},Ce={id:`deploy`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},we={class:`grid grid-cols-1 lg:grid-cols-2 gap-3`},Te={class:`space-y-2`},Ee={class:`space-y-2`},De={class:`space-y-2`},Oe={id:`config`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},ke={class:`doc-table-wrap`},Ae={class:`doc-table`},je={class:`doc-cell-key whitespace-nowrap`},Me={class:`doc-cell-mono hidden sm:table-cell whitespace-nowrap`},Ne={class:`doc-cell-body`},Pe={class:`space-y-2`},Fe={class:`doc-details group`},Ie={class:`doc-details-summary`},Le={class:`doc-details-body`},Re={id:`sdk`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},ze={class:`space-y-2`},Be={class:`space-y-2`},Ve={class:`space-y-2`},He={id:`schedules`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},Ue={class:`doc-section-head`},We={class:`doc-lede`},Ge={id:`webhooks`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},Ke={class:`doc-section-head`},qe={class:`doc-lede`},Je={class:`doc-table-wrap`},Ye={class:`doc-table`},Xe={class:`doc-cell-key whitespace-nowrap`},Ze={class:`doc-cell-body`},Qe={class:`space-y-2`},$e={id:`mcp`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},et={class:`grid grid-cols-1 md:grid-cols-3 gap-3`},tt={class:`doc-card`},nt={class:`doc-card-body`},rt={class:`doc-chip break-all`},it={class:`doc-token-bar`},at={class:`flex items-center gap-2 min-w-0 flex-1`},ot={key:0,class:`text-sm text-foreground-muted truncate`},st={key:1,class:`text-sm text-success truncate`},ct={class:`doc-chip`},lt=[`disabled`],ut={class:`doc-details group`},dt={class:`doc-details-summary`},ft={class:`doc-details-body space-y-4`},pt={id:`generate`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},mt={class:`ai-prompt-actions`},ht={key:0,class:`prompt-collapse-fade`,"aria-hidden":`true`},gt=[`aria-expanded`],_t={id:`tracing`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},vt={class:`doc-table-wrap`},yt={class:`doc-table`},bt={class:`doc-cell-key whitespace-nowrap`},xt={class:`doc-cell-body`},St={id:`errors`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},Ct={class:`doc-table-wrap`},wt={class:`doc-table`},Tt={class:`doc-cell-key whitespace-nowrap`},Et={class:`doc-cell-body`},Dt={id:`cli`,class:`space-y-5 scroll-mt-6 border-t border-border pt-12`},Ot={class:`doc-prose`},kt={class:`doc-table-wrap`},At={class:`doc-table`},jt={class:`doc-cell-key whitespace-nowrap`},Mt={class:`doc-cell-mono`},Nt={class:`doc-cell-body hidden md:table-cell`},Pt={class:`space-y-2`},Ft={class:`space-y-2`},It={class:`space-y-2`},Lt={class:`space-y-2`},Rt={class:`space-y-2`},zt=`# Available inside every running function — refresh per-invocation:
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
orva system health      # smoke test`,Wt=`# Deploy from a directory. Auto-detects handler.ts when tsconfig.json
# is present; else uses the runtime default (handler.js / handler.py).
orva deploy ./my-fn \\
  --name    resize-image \\
  --runtime node

# Override the entrypoint explicitly:
orva deploy ./my-fn --name api --runtime python --entrypoint app.py`,Gt=`# Invoke a function by name or UUID:
orva invoke resize-image --body '{"url":"https://example.com/cat.jpg"}'

# Recent executions:
orva logs resize-image

# Single execution, with stdout/stderr:
orva logs resize-image --exec-id exec_abc123

# Live tail — SSE stream, Ctrl-C to stop:
orva logs resize-image --follow`,Kt=`# List keys (optionally by prefix)
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
orva activity --follow                 # live feed (Ctrl-C)
orva activity --source mcp --limit 200 # MCP-only, last 200`,I=`<YOUR_ORVA_TOKEN>`,Yt={__name:`Docs`,setup(Yt){let Xt=se();E.registerLanguage(`python`,le),E.registerLanguage(`javascript`,D),E.registerLanguage(`js`,D),E.registerLanguage(`json`,ce),E.registerLanguage(`bash`,O),E.registerLanguage(`shell`,O),E.registerLanguage(`sh`,O),E.registerLanguage(`http`,fe);let L=_(()=>window.location.origin),R=[{id:`handler`,num:`01`,label:`Handler`},{id:`deploy`,num:`02`,label:`Deploy`},{id:`config`,num:`03`,label:`Config`},{id:`sdk`,num:`04`,label:`SDK`},{id:`schedules`,num:`05`,label:`Schedules`},{id:`webhooks`,num:`06`,label:`Webhooks`},{id:`mcp`,num:`07`,label:`MCP`},{id:`generate`,num:`08`,label:`AI prompt`},{id:`tracing`,num:`09`,label:`Tracing`},{id:`errors`,num:`10`,label:`Errors`},{id:`cli`,num:`11`,label:`CLI`}],z=r(`handler`),B=null;a(()=>{if(typeof IntersectionObserver>`u`)return;let e=new Set;B=new IntersectionObserver(t=>{for(let n of t)n.isIntersecting?e.add(n.target.id):e.delete(n.target.id);for(let t of R)if(e.has(t.id)){z.value=t.id;break}},{rootMargin:`-20% 0px -70% 0px`,threshold:0});for(let e of R){let t=document.getElementById(e.id);t&&B.observe(t)}}),e(()=>{B&&B.disconnect()});let Zt=de(),V=r(!1),H=null,Qt=async()=>{await ue()&&(V.value=!0,clearTimeout(H),H=setTimeout(()=>{V.value=!1},1500))},$t=s({setup(){return()=>v(`svg`,{viewBox:`0 0 256 255`,width:`14`,height:`14`,xmlns:`http://www.w3.org/2000/svg`},[v(`defs`,null,[v(`linearGradient`,{id:`pyg1`,x1:`0`,y1:`0`,x2:`1`,y2:`1`},[v(`stop`,{offset:`0`,"stop-color":`#387EB8`}),v(`stop`,{offset:`1`,"stop-color":`#366994`})]),v(`linearGradient`,{id:`pyg2`,x1:`0`,y1:`0`,x2:`1`,y2:`1`},[v(`stop`,{offset:`0`,"stop-color":`#FFE052`}),v(`stop`,{offset:`1`,"stop-color":`#FFC331`})])]),v(`path`,{fill:`url(#pyg1)`,d:`M126.9 12c-58.3 0-54.7 25.3-54.7 25.3l.1 26.2H128v8H50.5S12 67.2 12 126.1c0 58.9 33.6 56.8 33.6 56.8h19.4v-27.4s-1-33.6 33.1-33.6h55.9s32 .5 32-30.9V43.5S191.7 12 126.9 12zM95.7 29.9a10 10 0 0 1 0 20 10 10 0 0 1 0-20z`}),v(`path`,{fill:`url(#pyg2)`,d:`M129.1 243c58.3 0 54.7-25.3 54.7-25.3l-.1-26.2H128v-8h77.5s38.5 4.4 38.5-54.5c0-58.9-33.6-56.8-33.6-56.8h-19.4v27.4s1 33.6-33.1 33.6H102s-32-.5-32 30.9v52S64.3 243 129.1 243zm30.4-17.9a10 10 0 0 1 0-20 10 10 0 0 1 0 20z`})])}}),en=s({setup(){return()=>v(`svg`,{viewBox:`0 0 256 280`,width:`14`,height:`14`,xmlns:`http://www.w3.org/2000/svg`},[v(`path`,{fill:`#3F873F`,d:`M128 0 12 67v146l116 67 116-67V67L128 0zm0 24.6 95 54.8v121.2l-95 54.8-95-54.8V79.4l95-54.8z`}),v(`path`,{fill:`#3F873F`,d:`M128 64c-3 0-5.7.7-8 2.3L73 92c-5 2.7-8 8-8 13.6V169c0 5.6 3 10.7 8 13.5l13 7.4c6.3 3.1 8.5 3.1 11.4 3.1 9.4 0 14.8-5.7 14.8-15.6V117c0-1-.7-1.7-1.7-1.7H103c-1 0-1.7.7-1.7 1.7v60.2c0 4.4-4.5 8.7-11.8 5.1l-13.7-7.9a1.6 1.6 0 0 1-.8-1.4v-63.4c0-.6.3-1 .8-1.4l46.8-26.9c.4-.3 1-.3 1.4 0L171 110c.5.4.8.8.8 1.4V174a1.7 1.7 0 0 1-.8 1.4l-46.8 27c-.4.2-1 .2-1.4 0l-12-7.2c-.4-.2-.8-.2-1.2 0-3.4 1.9-4 2.2-7.2 3.3-.8.3-2 .7.4 2.1l15.7 9.3c2.5 1.4 5.3 2.2 8.2 2.2 2.9 0 5.7-.8 8.2-2.2L181 184c5-2.8 8-7.9 8-13.5V107c0-5.6-3-10.7-8-13.5l-46.7-26.7a17 17 0 0 0-6.3-2.8z`})])}}),tn=s({name:`DeployPipelineDiagram`,setup(){let e=[{glyph:`▣`,label:`Tarball`,sub:`POST /deploy`},{glyph:`⟜`,label:`Extract`,sub:`untar → scratch dir`},{glyph:`◍`,label:`Install`,sub:`npm / pip`},{glyph:`⟐`,label:`Compile`,sub:`tsc (TypeScript)`},{glyph:`◉`,label:`Activate`,sub:`rename → current`},{glyph:`✦`,label:`Warm pool`,sub:`pre-spawn N workers`}];return()=>v(`figure`,{class:`doc-diagram`},[v(`figcaption`,{class:`doc-diagram-cap`},`Deploy pipeline`),v(`div`,{class:`doc-pipeline`},e.flatMap((t,n)=>{let r=v(`div`,{key:`s${n}`,class:`doc-pipeline-stage`},[v(`div`,{class:`doc-pipeline-glyph`},t.glyph),v(`div`,{class:`doc-pipeline-label`},[v(`span`,{class:`doc-pipeline-name`},t.label),v(`span`,{class:`doc-pipeline-sub`},t.sub)])]),i=n<e.length-1?v(`div`,{key:`a${n}`,class:`doc-pipeline-arrow`,"aria-hidden":`true`}):null;return i?[r,i]:[r]}))])}}),nn=s({name:`TraceTreeDiagram`,setup(){let e=[{fn:`api-gateway`,trigger:`http`,start:0,dur:220,parent:null,klass:`root`},{fn:`resize-image`,trigger:`f2f`,start:30,dur:90,parent:`api-gateway`,klass:`child`},{fn:`send-email`,trigger:`job`,start:60,dur:40,parent:`api-gateway`,klass:`grand`}],t=e=>e/220*100;return()=>v(`figure`,{class:`doc-diagram`},[v(`figcaption`,{class:`doc-diagram-cap`},`Causal trace, one HTTP request and three spans`),v(`div`,{class:`doc-trace`},[v(`div`,{class:`doc-trace-axis`},[v(`span`,null,`0 ms`),v(`span`,null,`220 ms`)]),...e.map(e=>v(`div`,{key:e.fn,class:[`doc-trace-row`,`is-${e.klass}`]},[v(`div`,{class:`doc-trace-label`},[v(`span`,{class:`doc-trace-fn`},e.fn),v(`span`,{class:`doc-trace-trigger`},e.trigger)]),v(`div`,{class:`doc-trace-track`},[v(`div`,{class:`doc-trace-bar`,style:{left:`${t(e.start)}%`,width:`${t(e.dur)}%`},title:`+${e.start}ms · ${e.dur}ms`})]),v(`div`,{class:`doc-trace-dur`},`${e.dur}ms`)])),v(`div`,{class:`doc-trace-legend`},[v(`span`,null,`Same `),v(`code`,{class:`doc-chip`},`trace_id`),v(`span`,null,` across all spans · `),v(`code`,{class:`doc-chip`},`parent_span_id`),v(`span`,null,` chains them into a tree.`)])])])}}),rn=s({name:`WebhookDeliveryDiagram`,setup(){return()=>v(`figure`,{class:`doc-diagram`},[v(`figcaption`,{class:`doc-diagram-cap`},`Signed webhook delivery`),v(`div`,{class:`doc-webhook`},[v(`div`,{class:`doc-webhook-actor`},[v(`div`,{class:`doc-webhook-actor-head`},`orvad`),v(`div`,{class:`doc-webhook-actor-body`},[v(`span`,null,`event fires`),v(`code`,{class:`doc-chip`},`deployment.succeeded`)])]),v(`div`,{class:`doc-webhook-wire`},[v(`div`,{class:`doc-webhook-wire-line`,"aria-hidden":`true`}),v(`div`,{class:`doc-webhook-wire-payload`},[v(`div`,{class:`doc-webhook-wire-method`},`POST`),v(`div`,{class:`doc-webhook-wire-headers`},[v(`code`,null,`X-Orva-Event`),v(`code`,null,`X-Orva-Timestamp`),v(`code`,null,`X-Orva-Signature`)]),v(`div`,{class:`doc-webhook-wire-sig`},`sha256=hex(hmac(secret, ts.body))`)])]),v(`div`,{class:`doc-webhook-actor`},[v(`div`,{class:`doc-webhook-actor-head`},`your receiver`),v(`div`,{class:`doc-webhook-actor-body`},[v(`span`,null,`verify HMAC`),v(`span`,null,`→ 2xx within 15s or get retried`)])])])])}}),an=_(()=>[{label:`Python`,lang:`python`,code:`def handler(event):
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
};`}]),on=_(()=>[{label:`curl`,lang:`bash`,code:`curl -X POST ${L.value}/fn/<function_id> \\
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
print(r.json())`}]),sn=[{id:`python`,name:`Python 3.14`,entry:`handler.py`,deps:`requirements.txt`,icon:$t},{id:`node`,name:`Node.js 24`,entry:`handler.js`,deps:`package.json`,icon:en}],cn=[{field:`env_vars`,purpose:`Plain config`,body:`Plaintext config stored on the function record. Use for feature flags and non-secret settings.`,icon:ne,iconClass:`text-primary`},{field:`/secrets`,purpose:`Encrypted`,body:`AES-256-GCM at rest. Values decrypt only into the worker environment at spawn time.`,icon:w,iconClass:`text-primary`},{field:`network_mode`,purpose:`Egress control`,body:`none = isolated loopback. egress = outbound HTTPS allowed, filtered by the sandbox egress policy.`,icon:C,iconClass:`text-primary`},{field:`auth_mode`,purpose:`Invoke gate`,body:`none = public. platform_key = require Orva API key. signed = require HMAC.`,icon:T,iconClass:`text-primary`},{field:`rate_limit_per_min`,purpose:`Per-IP throttle`,body:`Optional cap for public or webhook-facing functions. Exceeding it returns 429.`,icon:ae,iconClass:`text-primary`}],ln=_(()=>`curl -X POST ${L.value}/api/v1/functions \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"name":"hello","runtime":"python","memory_mb":128,"cpus":0.5}'`),un=_(()=>`tar czf code.tar.gz handler.py requirements.txt
curl -X POST ${L.value}/api/v1/functions/<function_id>/deploy \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -F code=@code.tar.gz`),dn=_(()=>`curl -X POST ${L.value}/api/v1/functions/<function_id>/secrets \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"key":"DATABASE_URL","value":"postgres://..."}'`),fn=_(()=>`# generate signature
SECRET='your-shared-secret-stored-in-function-secrets'
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')

curl -X POST ${L.value}/fn/<function_id> \\
  -H "X-Orva-Timestamp: $TS" \\
  -H "X-Orva-Signature: sha256=$SIG" \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`),pn=_(()=>[{label:`curl`,lang:`bash`,note:`Create a daily-9am schedule for an existing function. payload is delivered as the invoke body.`,code:`curl -X POST ${L.value}/api/v1/functions/<function_id>/cron \\
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
})`}],U=[{name:`http`,desc:`Public HTTP request hit /fn/<id>/. Almost always a root span.`},{name:`f2f`,desc:`Another function called this one via orva.invoke(). Has a parent_span_id.`},{name:`job`,desc:`Background job runner picked up an enqueued job. Parent_span_id is whoever enqueued it.`},{name:`cron`,desc:`Scheduler fired a cron entry. Always a root span.`},{name:`inbound`,desc:`External webhook hit /webhook/{id}. Always a root span.`},{name:`replay`,desc:`Operator clicked Replay on a captured execution. Fresh trace, no link to original.`},{name:`mcp`,desc:`AI agent invoked the function via MCP invoke_function. Fresh trace.`}],yn=[{code:`VALIDATION`,when:`Bad request body or path parameter.`},{code:`UNAUTHORIZED`,when:`Missing or invalid API key / session cookie.`},{code:`NOT_FOUND`,when:`Function, deployment, or secret doesn't exist.`},{code:`RATE_LIMITED`,when:`Too many requests; check the Retry-After header.`},{code:`VERSION_GCD`,when:`Rollback target was garbage-collected.`},{code:`INSUFFICIENT_DISK`,when:`Host is below min_free_disk_mb.`}],bn=[{cmd:`login`,subs:`—`,purpose:`Save endpoint + API key to ~/.orva/config.yaml`},{cmd:`init`,subs:`—`,purpose:`Full server only: write the legacy orva.yaml template`},{cmd:`deploy`,subs:`[path]`,purpose:`Package a directory and deploy as a function`},{cmd:`invoke`,subs:`[name|id]`,purpose:`POST to /fn/<id>/ and print the response`},{cmd:`logs`,subs:`[name|id] [--follow]`,purpose:`List recent executions; --follow streams live via SSE`},{cmd:`functions`,subs:`list / get / create / delete`,purpose:`CRUD for the function registry`},{cmd:`cron`,subs:`list / create / update / delete`,purpose:`Manage cron schedules attached to functions`},{cmd:`jobs`,subs:`list / enqueue / retry / delete`,purpose:`Background queue management`},{cmd:`kv`,subs:`list / get / put / delete`,purpose:`Browse a function’s key/value store`},{cmd:`secrets`,subs:`list / set / delete`,purpose:`AES-256-GCM secrets per function`},{cmd:`webhooks`,subs:`list / create / test / delete / inbound`,purpose:`System-event subscribers + inbound triggers`},{cmd:`routes`,subs:`list / set / delete`,purpose:`Custom URL → function path mappings`},{cmd:`keys`,subs:`list / create / revoke`,purpose:`Manage API keys`},{cmd:`activity`,subs:`[--follow] [--source web|api|...]`,purpose:`Paginated activity rows; live SSE with --follow`},{cmd:`system`,subs:`health / metrics / db-stats / vacuum`,purpose:`Server diagnostics`},{cmd:`setup`,subs:`[--skip-nsjail] [--skip-rootfs]`,purpose:`Install nsjail + rootfs on a bare host`},{cmd:`serve`,subs:`[--port N]`,purpose:`Run as the server daemon (not the CLI client)`},{cmd:`completion`,subs:`bash / zsh / fish / powershell`,purpose:`Emit shell completion script`}],W=r(``);a(async()=>{try{let e=await fetch(`/web/docs.md`,{cache:`no-cache`});e.ok&&(W.value=await e.text())}catch{}});let xn=_(()=>W.value.replaceAll(`{{ORIGIN}}`,window.location.origin)),G=r(!1),K=null,Sn=async()=>{await k(xn.value)&&(G.value=!0,clearTimeout(K),K=setTimeout(()=>{G.value=!1},1500))},q=r(!1),J=r(``),Y=r(!1),Cn=_(()=>J.value.slice(0,12)),X=_(()=>J.value||I),wn=async()=>{if(!Y.value){Y.value=!0;try{let e=new Date().toISOString().slice(0,16).replace(`T`,` `),t=await re.post(`/keys`,{name:`MCP: `+e,permissions:[`invoke`,`read`,`write`,`admin`]});J.value=t.data.key}catch(e){console.error(`mint mcp key failed`,e),Xt.notify({title:`Could not mint key`,message:e?.response?.data?.error?.message||e.message||`Unknown error`,danger:!0})}finally{Y.value=!1}}},Tn=_(()=>[{label:`Claude Code`,lang:`bash`,note:"Anthropic's `claude` CLI. Restart Claude Code afterwards; `/mcp` lists Orva's 73 tools.",code:`claude mcp add --transport http --scope user orva ${L.value}/mcp --header "Authorization: Bearer ${X.value}"`},{label:`curl`,lang:`bash`,note:"Talk to MCP directly — no handshake, no session id. Step 1 asks the server what it supports; Step 2 lists the tools. A successful reply is one SSE `message` event; a rejected one is plain JSON with a 4xx.",code:`curl -sN -X POST ${L.value}/mcp \\
  -H 'Authorization: Bearer ${X.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H 'Mcp-Protocol-Version: 2026-07-28' \\
  -H 'Mcp-Method: server/discover' \\
  -d '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'

curl -sN -X POST ${L.value}/mcp \\
  -H 'Authorization: Bearer ${X.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H 'Mcp-Protocol-Version: 2026-07-28' \\
  -H 'Mcp-Method: tools/list' \\
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"curl","version":"0"}}}}'

# Calling a tool needs a third header. Mcp-Name must repeat params.name, or the
# request is refused with -32020 before the tool runs:
curl -sN -X POST ${L.value}/mcp \\
  -H 'Authorization: Bearer ${X.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H 'Mcp-Protocol-Version: 2026-07-28' \\
  -H 'Mcp-Method: tools/call' \\
  -H 'Mcp-Name: system_health' \\
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"system_health","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'

# Older clients need none of that — a bare list returns the same catalog:
curl -sN -X POST ${L.value}/mcp \\
  -H 'Authorization: Bearer ${X.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}'`}]),En=_(()=>[{label:`Claude Desktop`,lang:`json`,note:`Paste into ~/Library/Application Support/Claude/claude_desktop_config.json (macOS), %APPDATA%\\Claude\\claude_desktop_config.json (Windows), or ~/.config/Claude/claude_desktop_config.json (Linux). Restart Claude Desktop.`,code:`{
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
Auth: OAuth (auto-discovered)`}]),Dn=_(()=>{let e=JSON.stringify({url:L.value+`/mcp`,headers:{Authorization:`Bearer `+X.value}});return typeof window.btoa==`function`?window.btoa(e):e}),On=_(()=>[{label:`Cursor (global)`,lang:`json`,note:`Paste into ~/.cursor/mcp.json, or .cursor/mcp.json in your project root for a per-workspace install.`,code:`{
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
}`}]),Z=s({name:`CodeBlock`,props:{code:{type:String,required:!0},lang:{type:String,default:``}},setup(e){let t=r(!1),n=async()=>{await k(e.code)&&(t.value=!0,setTimeout(()=>{t.value=!1},1200))},i=_(()=>{let t=(e.lang||``).toLowerCase();if(t&&E.getLanguage(t))try{return E.highlight(e.code,{language:t,ignoreIllegals:!0}).value}catch{}return e.code.replace(/&/g,`&amp;`).replace(/</g,`&lt;`).replace(/>/g,`&gt;`)});return()=>v(`div`,{class:`codeblock`},[v(`div`,{class:`codeblock-bar`},[v(`span`,{class:`codeblock-lang`},e.lang||``),v(`button`,{class:`codeblock-copy`,onClick:n,title:`Copy code`},[t.value?v(b,{class:`w-3 h-3`}):v(S,{class:`w-3 h-3`}),t.value?`Copied`:`Copy`])]),v(`pre`,{class:`codeblock-pre`},[v(`code`,{class:`hljs language-${(e.lang||`text`).toLowerCase()}`,innerHTML:i.value})])])}}),Q=s({name:`TabbedCode`,props:{tabs:{type:Array,required:!0},storageKey:{type:String,default:``}},setup(e){let t=(()=>{try{if(e.storageKey){let t=localStorage.getItem(e.storageKey);if(t&&e.tabs.some(e=>e.label===t))return t}}catch{}return e.tabs[0]?.label})(),n=r(t),i=t=>{n.value=t;try{e.storageKey&&localStorage.setItem(e.storageKey,t)}catch{}};return()=>{let t=e.tabs.find(e=>e.label===n.value)||e.tabs[0];return v(`div`,{class:`tabbed`},[v(`div`,{class:`tabbed-tabs`},e.tabs.map(e=>v(`button`,{key:e.label,class:[`tabbed-tab`,{active:e.label===n.value}],onClick:()=>i(e.label)},e.label))),t.note?v(`div`,{class:`tabbed-note`},t.note):null,v(Z,{code:t.code,lang:t.lang})])}}}),$=s({name:`DocsCallout`,props:{title:{type:String,default:``},icon:{type:[Object,Function],default:null}},setup(e,{slots:t}){return()=>v(`div`,{class:`callout`},[v(`div`,{class:`callout-head`},[e.icon?v(e.icon,{class:`callout-icon`}):null,e.title?v(`span`,null,e.title):null]),v(`div`,{class:`callout-body`},t.default?.())])}});return(e,r)=>{let a=ee(`router-link`);return t(),l(`div`,pe,[c(`header`,me,[c(`div`,he,[c(`div`,ge,[r[1]||=c(`div`,{class:`docs-hero-text`},[c(`h1`,{class:`docs-hero-title`},` Documentation `),c(`p`,{class:`docs-hero-sub`},` Build, deploy, and operate functions on Orva. `)],-1),c(`div`,_e,[c(`button`,{class:u([`docs-hero-copy-icon`,{copied:G.value}]),title:G.value?`Copied`:`Copy entire docs page as Markdown`,"aria-label":G.value?`Markdown copied to clipboard`:`Copy entire docs page as Markdown`,onClick:Sn},[G.value?(t(),p(o(b),{key:0,class:`w-4 h-4`})):(t(),p(o(S),{key:1,class:`w-4 h-4`}))],10,ve)])]),c(`nav`,A,[r[2]||=c(`span`,{class:`docs-hero-toc-label`},`Jump to`,-1),(t(),l(g,null,f(R,e=>c(`a`,{key:e.id,href:`#${e.id}`,class:u([`docs-hero-toc-link`,{active:z.value===e.id}])},[c(`span`,M,y(e.num),1),c(`span`,null,y(e.label),1)],10,j)),64))])])]),c(`section`,N,[r[4]||=c(`div`,{class:`doc-section-head`},[c(`span`,{class:`doc-section-num`},`01`),c(`div`,null,[c(`h2`,{class:`doc-section-title`},` Handler contract `),c(`p`,{class:`doc-lede`},` One exported function receives the inbound HTTP event and returns an HTTP-shaped response. The adapter handles serialization and headers. `)])],-1),d(o(Q),{tabs:an.value,"storage-key":`docs.handler`},null,8,[`tabs`]),r[5]||=h(`<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Event shape </div><div class="doc-card-body"><code class="doc-chip">method</code><code class="doc-chip">path</code><code class="doc-chip">headers</code><code class="doc-chip">query</code><code class="doc-chip">body</code></div></div><div class="doc-card"><div class="doc-microlabel"> Response </div><div class="doc-card-body"><code class="doc-chip">{ statusCode, headers, body }</code><p class="mt-1.5 text-foreground-muted"> Non-string bodies are JSON-encoded by the adapter. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Runtime env </div><div class="doc-card-body"> Env vars and secrets land in <code class="doc-chip">process.env</code> / <code class="doc-chip">os.environ</code>. </div></div></div>`,1),c(`div`,P,[c(`table`,F,[r[3]||=c(`thead`,null,[c(`tr`,null,[c(`th`,null,`Runtime`),c(`th`,null,`ID`),c(`th`,{class:`hidden sm:table-cell`},` Entrypoint `),c(`th`,{class:`hidden md:table-cell`},` Dependencies `)])],-1),c(`tbody`,null,[(t(),l(g,null,f(sn,e=>c(`tr`,{key:e.id},[c(`td`,ye,[(t(),p(i(e.icon),{class:`shrink-0`})),m(` `+y(e.name),1)]),c(`td`,be,y(e.id),1),c(`td`,xe,y(e.entry),1),c(`td`,Se,y(e.deps),1)])),64))])])])]),c(`section`,Ce,[r[10]||=h(`<div class="doc-section-head"><span class="doc-section-num">02</span><div><h2 class="doc-section-title"> Deploy &amp; invoke </h2><p class="doc-lede"> The dashboard handles day-to-day work; these calls are for CI and automation. Builds run async; poll <code class="doc-chip">/api/v1/deployments/&lt;id&gt;</code> or stream <code class="doc-chip">/api/v1/deployments/&lt;id&gt;/stream</code> until <code class="doc-chip">phase: done</code>. </p></div></div>`,1),d(o(tn)),c(`div`,we,[c(`div`,Te,[r[6]||=c(`div`,{class:`doc-step-label`},[c(`span`,{class:`doc-step-num`},`1`),m(` Create the function row `)],-1),d(o(Z),{code:ln.value,lang:`bash`},null,8,[`code`])]),c(`div`,Ee,[r[7]||=c(`div`,{class:`doc-step-label`},[c(`span`,{class:`doc-step-num`},`2`),m(` Upload code `)],-1),d(o(Z),{code:un.value,lang:`bash`},null,8,[`code`])])]),c(`div`,De,[r[8]||=c(`div`,{class:`doc-microlabel`},` Invoke `,-1),d(o(Q),{tabs:on.value,"storage-key":`docs.invoke`},null,8,[`tabs`])]),d(o($),{icon:o(C),title:`Custom routes`},{default:n(()=>[...r[9]||=[m(` Attach a friendly path with `,-1),c(`code`,{class:`doc-chip`},`POST /api/v1/routes`,-1),m(`. Reserved prefixes: `,-1),c(`code`,{class:`doc-chip`},`/api/`,-1),c(`code`,{class:`doc-chip`},`/fn/`,-1),c(`code`,{class:`doc-chip`},`/mcp/`,-1),c(`code`,{class:`doc-chip`},`/web/`,-1),c(`code`,{class:`doc-chip`},`/_orva/`,-1),m(`. `,-1)]]),_:1},8,[`icon`])]),c(`section`,Oe,[r[14]||=c(`div`,{class:`doc-section-head`},[c(`span`,{class:`doc-section-num`},`03`),c(`div`,null,[c(`h2`,{class:`doc-section-title`},` Configuration reference `),c(`p`,{class:`doc-lede`},` Everything below lives on the function record. Secrets are stored encrypted and only decrypt into the worker environment at spawn time. `)])],-1),c(`div`,ke,[c(`table`,Ae,[r[11]||=c(`thead`,null,[c(`tr`,null,[c(`th`,null,`Field`),c(`th`,{class:`hidden sm:table-cell`},` Purpose `),c(`th`,null,`Behaviour`)])],-1),c(`tbody`,null,[(t(),l(g,null,f(cn,e=>c(`tr`,{key:e.field,class:`align-top`},[c(`td`,je,[(t(),p(i(e.icon),{class:u([`w-3.5 h-3.5 shrink-0`,e.iconClass])},null,8,[`class`])),c(`code`,null,y(e.field),1)]),c(`td`,Me,y(e.purpose),1),c(`td`,Ne,y(e.body),1)])),64))])])]),c(`div`,Pe,[r[12]||=c(`div`,{class:`doc-microlabel`},` Set a secret `,-1),d(o(Z),{code:dn.value,lang:`bash`},null,8,[`code`])]),c(`details`,Fe,[c(`summary`,Ie,[d(o(x),{class:`w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted`}),r[13]||=m(` Signed-invoke recipe (HMAC, opt-in) `,-1)]),c(`div`,Le,[d(o(Z),{code:fn.value,lang:`bash`},null,8,[`code`])])])]),c(`section`,Re,[r[20]||=h(`<div class="doc-section-head"><span class="doc-section-num">04</span><div><h2 class="doc-section-title"> SDK from inside a function </h2><p class="doc-lede"> The bundled <code class="doc-chip">orva</code> module exposes three primitives every function can use without extra dependencies: a per-function key/value store, in-process calls to other Orva functions, and a fire-and-forget background job queue. Routes through a process-signed, function-scoped credential injected at worker spawn time. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.kv</code></div><div class="doc-card-body"><code class="doc-chip">put / get / delete / list</code><p class="mt-1.5 text-foreground-muted"> Per-function namespace on SQLite, optional TTL. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.invoke</code></div><div class="doc-card-body"><code class="doc-chip">invoke(name, payload)</code><p class="mt-1.5 text-foreground-muted"> In-process call to another function. 8-deep call cap. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.jobs</code></div><div class="doc-card-body"><code class="doc-chip">jobs.enqueue(name, payload)</code><p class="mt-1.5 text-foreground-muted"> Fire-and-forget; persisted; retried with exp backoff. </p></div></div></div>`,2),c(`div`,ze,[r[15]||=c(`div`,{class:`doc-microlabel`},` KV: get/put with TTL `,-1),d(o(Q),{tabs:mn,"storage-key":`docs.sdk.kv`}),r[16]||=h(`<p class="text-xs text-foreground-muted"> Browse / inspect / edit / delete / set keys without leaving the dashboard at <code class="doc-chip">/web/functions/&lt;name&gt;/kv</code> (or click the <code class="doc-chip">KV</code> button in the editor&#39;s action bar). REST mirror at <code class="doc-chip">GET/PUT/DELETE /api/v1/functions/&lt;id&gt;/kv[/&lt;key&gt;]</code>; MCP tools <code class="doc-chip">kv_list</code> / <code class="doc-chip">kv_get</code> / <code class="doc-chip">kv_put</code> / <code class="doc-chip">kv_delete</code> for agents. </p>`,1)]),c(`div`,Be,[r[17]||=c(`div`,{class:`doc-microlabel`},` Function-to-function: invoke() `,-1),d(o(Q),{tabs:hn,"storage-key":`docs.sdk.invoke`})]),c(`div`,Ve,[r[18]||=c(`div`,{class:`doc-microlabel`},` Background jobs: jobs.enqueue() `,-1),d(o(Q),{tabs:gn,"storage-key":`docs.sdk.jobs`})]),d(o($),{icon:o(C),title:`Network mode`},{default:n(()=>[...r[19]||=[m(` The SDK reaches orvad over loopback through the host gateway, so the function needs `,-1),c(`code`,{class:`doc-chip`},`network_mode: "egress"`,-1),m(`. On the default `,-1),c(`code`,{class:`doc-chip`},`"none"`,-1),m(` the SDK throws `,-1),c(`code`,{class:`doc-chip`},`OrvaUnavailableError`,-1),m(` with a clear hint. `,-1)]]),_:1},8,[`icon`])]),c(`section`,He,[c(`div`,Ue,[r[31]||=c(`span`,{class:`doc-section-num`},`05`,-1),c(`div`,null,[r[30]||=c(`h2`,{class:`doc-section-title`},` Schedules `,-1),c(`p`,We,[r[22]||=m(` Fire any function on a cron expression. The scheduler runs as part of the orvad process; no external service. Manage from the `,-1),d(a,{to:`/cron`,class:`text-foreground hover:text-white underline decoration-dotted underline-offset-4`},{default:n(()=>[...r[21]||=[m(` Schedules page `,-1)]]),_:1}),r[23]||=m(` or via the API. Standard 5-field cron with the usual shorthands (`,-1),r[24]||=c(`code`,{class:`doc-chip`},`@daily`,-1),r[25]||=m(`, `,-1),r[26]||=c(`code`,{class:`doc-chip`},`@hourly`,-1),r[27]||=m(`, `,-1),r[28]||=c(`code`,{class:`doc-chip`},`*/5 * * * *`,-1),r[29]||=m(`). `,-1)])])]),d(o(Q),{tabs:pn.value,"storage-key":`docs.cron`},null,8,[`tabs`]),d(o($),{icon:o(oe),title:`Cron-fired headers`},{default:n(()=>[...r[32]||=[m(` Every cron-triggered invocation arrives at the function with `,-1),c(`code`,{class:`doc-chip`},`x-orva-trigger: cron`,-1),m(` and `,-1),c(`code`,{class:`doc-chip`},`x-orva-cron-id: cron_…`,-1),m(` on the event headers, so user code can branch on origin. `,-1)]]),_:1},8,[`icon`])]),c(`section`,Ge,[c(`div`,Ke,[r[37]||=c(`span`,{class:`doc-section-num`},`06`,-1),c(`div`,null,[r[36]||=c(`h2`,{class:`doc-section-title`},` Webhooks `,-1),c(`p`,qe,[r[34]||=m(` Operator-managed subscriptions for system events. Configure URLs from the `,-1),d(a,{to:`/webhooks`,class:`text-foreground hover:text-white underline decoration-dotted underline-offset-4`},{default:n(()=>[...r[33]||=[m(` Webhooks page `,-1)]]),_:1}),r[35]||=m(`; Orva delivers signed POSTs to them when matching events fire (deployments, function lifecycle, cron failures, job outcomes). Subscriptions are global, not per-function. `,-1)])])]),d(o(rn)),r[40]||=h(`<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Headers </div><div class="doc-card-body"><code class="doc-chip">X-Orva-Event</code><code class="doc-chip">X-Orva-Delivery-Id</code><code class="doc-chip">X-Orva-Timestamp</code><code class="doc-chip">X-Orva-Signature</code></div></div><div class="doc-card"><div class="doc-microlabel"> Signature </div><div class="doc-card-body"><code class="doc-chip">sha256=hex(hmac(secret, ts.body))</code><p class="mt-1.5 text-foreground-muted"> Same shape as Stripe / signed-invoke. Receivers verify with the secret returned at create time. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Retries </div><div class="doc-card-body"><code class="doc-chip">5 attempts</code><code class="doc-chip">exp backoff (≤ 1h)</code><p class="mt-1.5 text-foreground-muted"> Receiver must 2xx within 15s. </p></div></div></div>`,1),c(`div`,Je,[c(`table`,Ye,[r[38]||=c(`thead`,null,[c(`tr`,null,[c(`th`,null,`Event`),c(`th`,null,`When it fires`)])],-1),c(`tbody`,null,[(t(),l(g,null,f(_n,e=>c(`tr`,{key:e.name},[c(`td`,Xe,[c(`code`,null,y(e.name),1)]),c(`td`,Ze,y(e.when),1)])),64))])])]),c(`div`,Qe,[r[39]||=c(`div`,{class:`doc-microlabel`},` Verify a delivery `,-1),d(o(Q),{tabs:vn,"storage-key":`docs.webhooks.verify`})])]),c(`section`,$e,[r[52]||=c(`div`,{class:`doc-section-head`},[c(`span`,{class:`doc-section-num`},`07`),c(`div`,null,[c(`h2`,{class:`doc-section-title`},` MCP: Model Context Protocol `),c(`p`,{class:`doc-lede`},` Same API surface the dashboard uses, exposed as 73 tools an agent can call directly. API key permissions scope the available tool set. `)])],-1),c(`div`,et,[c(`div`,tt,[r[41]||=c(`div`,{class:`doc-microlabel`},` Endpoint `,-1),c(`div`,nt,[c(`code`,rt,y(L.value)+`/mcp`,1)])]),r[42]||=h(`<div class="doc-card"><div class="doc-microlabel"> Auth header </div><div class="doc-card-body"><code class="doc-chip break-all">Authorization: Bearer &lt;token&gt;</code><p class="mt-1.5 text-foreground-muted"> Or as a fallback: <code class="doc-chip">X-Orva-API-Key: &lt;token&gt;</code></p></div></div><div class="doc-card"><div class="doc-microlabel"> Transport </div><div class="doc-card-body"><code class="doc-chip">Streamable HTTP</code><code class="doc-chip">Stateless</code><code class="doc-chip">MCP 2026-07-28</code><p class="mt-1.5 text-foreground-muted"> Older clients negotiate down — <code class="doc-chip">server/discover</code> advertises 2026-07-28, 2025-11-25, 2025-06-18, 2025-03-26 and 2024-11-05. </p></div></div>`,2)]),d(o($),{icon:o(w),title:`Two header formats; same auth`},{default:n(()=>[...r[43]||=[m(` Either header works against the same API key store with identical permission gating. `,-1),c(`code`,{class:`doc-chip`},`Authorization: Bearer`,-1),m(` is the MCP / OAuth 2 spec form; every MCP SDK (Claude Code, Claude Desktop, Cursor, mcp-remote, Python `,-1),c(`code`,{class:`doc-chip`},`mcp`,-1),m(`) configures it natively, so prefer it for new setups. `,-1),c(`code`,{class:`doc-chip`},`X-Orva-API-Key`,-1),m(` is the same header the REST API accepts; useful when a tool reuses an existing Orva REST integration. Internally both paths SHA-256-hash the token and look it up against the same `,-1),c(`code`,{class:`doc-chip`},`api_keys`,-1),m(` table. `,-1)]]),_:1},8,[`icon`]),d(o($),{icon:o(C),title:`No handshake, no session`},{default:n(()=>[...r[44]||=[m(` The transport is stateless. There is no `,-1),c(`code`,{class:`doc-chip`},`initialize`,-1),m(` step to perform first, no `,-1),c(`code`,{class:`doc-chip`},`Mcp-Session-Id`,-1),m(` is ever issued, and `,-1),c(`code`,{class:`doc-chip`},`GET /mcp`,-1),m(` / `,-1),c(`code`,{class:`doc-chip`},`DELETE /mcp`,-1),m(` — the SSE-resume and session-teardown verbs of the older session transport — return `,-1),c(`code`,{class:`doc-chip`},`405`,-1),m(`. Every POST carries its own bearer token and is answered on its own; a legacy client that still sends `,-1),c(`code`,{class:`doc-chip`},`initialize`,-1),m(` gets a normal reply with its own `,-1),c(`code`,{class:`doc-chip`},`protocolVersion`,-1),m(` echoed back, and simply never receives a session header. A request that opts into 2026-07-28 sends the headers `,-1),c(`code`,{class:`doc-chip`},`Mcp-Protocol-Version`,-1),m(` and `,-1),c(`code`,{class:`doc-chip`},`Mcp-Method`,-1),m(` — plus `,-1),c(`code`,{class:`doc-chip`},`Mcp-Name`,-1),m(` for `,-1),c(`code`,{class:`doc-chip`},`tools/call`,-1),m(`, `,-1),c(`code`,{class:`doc-chip`},`resources/read`,-1),m(` and `,-1),c(`code`,{class:`doc-chip`},`prompts/get`,-1),m(`, which must repeat the name from the body — plus `,-1),c(`code`,{class:`doc-chip`},`io.modelcontextprotocol/protocolVersion`,-1),m(` and `,-1),c(`code`,{class:`doc-chip`},`io.modelcontextprotocol/clientCapabilities`,-1),m(` in `,-1),c(`code`,{class:`doc-chip`},`params._meta`,-1),m(` (a `,-1),c(`code`,{class:`doc-chip`},`clientInfo`,-1),m(` key is optional). The headers let a proxy route on the operation without parsing the body, so one that disagrees with the body is rejected, not ignored. Every POST must send `,-1),c(`code`,{class:`doc-chip`},`Accept: application/json, text/event-stream`,-1),m(`. A successful reply is one SSE `,-1),c(`code`,{class:`doc-chip`},`message`,-1),m(` event; a request rejected at the transport layer comes back as plain `,-1),c(`code`,{class:`doc-chip`},`application/json`,-1),m(` with a 4xx status. `,-1)]]),_:1},8,[`icon`]),d(o($),{icon:o(T),title:`List results are private and immediately stale`},{default:n(()=>[...r[45]||=[c(`code`,{class:`doc-chip`},`tools/list`,-1),m(`, `,-1),c(`code`,{class:`doc-chip`},`resources/list`,-1),m(`, `,-1),c(`code`,{class:`doc-chip`},`resources/templates/list`,-1),m(`, `,-1),c(`code`,{class:`doc-chip`},`prompts/list`,-1),m(`, `,-1),c(`code`,{class:`doc-chip`},`resources/read`,-1),m(` and `,-1),c(`code`,{class:`doc-chip`},`server/discover`,-1),m(` results carry `,-1),c(`code`,{class:`doc-chip`},`ttlMs`,-1),m(` and `,-1),c(`code`,{class:`doc-chip`},`cacheScope`,-1),m(`. Orva returns `,-1),c(`code`,{class:`doc-chip`},`cacheScope: "private"`,-1),m(` because the tool catalog is permission-scoped (a full-permission key lists 73 tools; a `,-1),c(`code`,{class:`doc-chip`},`read`,-1),m(`-only key lists 27) and channel-specific (a channel token sees only that channel's functions) — a shared cache entry would hand one caller another caller's tool surface. `,-1),c(`code`,{class:`doc-chip`},`ttlMs`,-1),m(` is `,-1),c(`code`,{class:`doc-chip`},`0`,-1),m(` because the catalog changes on any deploy, channel edit, or permission change, and statelessness removed the session a `,-1),c(`code`,{class:`doc-chip`},`tools/list_changed`,-1),m(` notification would have travelled over. Re-list instead of caching. `,-1)]]),_:1},8,[`icon`]),c(`div`,it,[c(`div`,at,[d(o(w),{class:`w-4 h-4 shrink-0 text-foreground-muted`}),J.value?(t(),l(`span`,st,[r[48]||=m(` Token minted: `,-1),c(`code`,ct,y(Cn.value)+`…`,1),r[49]||=m(` Shown once, copy now. `,-1)])):(t(),l(`span`,ot,[r[46]||=m(` Snippets show `,-1),c(`code`,{class:`doc-chip`},y(I)),r[47]||=m(`. Mint a token to substitute it everywhere. `,-1)]))]),c(`button`,{class:`doc-token-btn`,disabled:Y.value,onClick:wn},[d(o(w),{class:`w-3.5 h-3.5`}),m(` `+y(J.value?`Mint another`:Y.value?`Minting…`:`Generate token`),1)],8,lt)]),d(o(Q),{tabs:Tn.value,"storage-key":`docs.mcp.install`},null,8,[`tabs`]),c(`details`,ut,[c(`summary`,dt,[d(o(x),{class:`w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted`}),r[50]||=m(` More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT, manual config) `,-1)]),c(`div`,ft,[d(o(Q),{tabs:En.value,"storage-key":`docs.mcp.install.more`},null,8,[`tabs`]),r[51]||=c(`div`,{class:`doc-microlabel pt-1`},` Hand-edited config files `,-1),d(o(Q),{tabs:On.value,"storage-key":`docs.mcp.manual`},null,8,[`tabs`])])])]),c(`section`,pt,[r[53]||=c(`div`,{class:`doc-section-head`},[c(`span`,{class:`doc-section-num`},`08`),c(`div`,null,[c(`h2`,{class:`doc-section-title`},` System prompt for AI assistants `),c(`p`,{class:`doc-lede`},` Copy Orva's full reference into another AI assistant. `)])],-1),c(`div`,mt,[c(`button`,{class:u([`ai-copy-btn`,{copied:V.value}]),onClick:Qt},[V.value?(t(),p(o(b),{key:0,class:`w-3.5 h-3.5`})):(t(),p(o(S),{key:1,class:`w-3.5 h-3.5`})),m(` `+y(V.value?`Copied`:`Copy system prompt`),1)],2)]),c(`div`,{class:u([`prompt-collapse`,{expanded:q.value}])},[d(o(Z),{code:o(Zt),lang:`text`},null,8,[`code`]),q.value?te(``,!0):(t(),l(`div`,ht))],2),c(`button`,{class:`prompt-expand-btn`,"aria-expanded":q.value,onClick:r[0]||=e=>q.value=!q.value},[d(o(ie),{class:u([`w-3.5 h-3.5 transition-transform`,{"rotate-180":q.value}])},null,8,[`class`]),m(` `+y(q.value?`Collapse system prompt`:`Show full system prompt`),1)],8,gt)]),c(`section`,_t,[r[55]||=h(`<div class="doc-section-head"><span class="doc-section-num">09</span><div><h2 class="doc-section-title"> Tracing </h2><p class="doc-lede"> Every invocation chain is recorded as a causal trace. automatically, with <strong>zero changes to your function code</strong>. HTTP requests, F2F invokes, jobs, cron, inbound webhooks, and replays all stitch into the same tree. The dashboard renders it as a waterfall at <code class="doc-chip">/traces</code>. </p></div></div><p class="doc-prose"> Each execution row IS a span. Spans share a <code class="doc-chip">trace_id</code>; child spans point at their parent via <code class="doc-chip">parent_span_id</code>. You don&#39;t instantiate spans, you don&#39;t import a tracer; you just write your handler and the platform plumbs IDs through every internal hop. </p>`,2),d(o(nn)),r[56]||=c(`h3`,{class:`doc-h3`},` What user code sees `,-1),r[57]||=c(`p`,{class:`doc-prose`},` Two env vars are stamped per invocation. Read them only if you want to log the trace_id alongside your own messages; they're optional. `,-1),d(o(Z),{code:zt,lang:`text`}),r[58]||=c(`h3`,{class:`doc-h3`},` Automatic propagation `,-1),r[59]||=c(`p`,{class:`doc-prose`},[m(` When a function calls another via the SDK, the trace context flows through automatically. The called function becomes a child span of the caller; both share the same `),c(`code`,{class:`doc-chip`},`trace_id`),m(`. `)],-1),d(o(Z),{code:Bt,lang:`js`}),r[60]||=h(`<p class="doc-prose"> Job enqueues work the same way: <code class="doc-chip">orva.jobs.enqueue()</code> records the trace context on the job row. When the scheduler picks the job up later, the resulting execution lands in the same trace as the function that enqueued it, even if the gap is hours or days. </p><h3 class="doc-h3"> Triggers </h3><p class="doc-prose"> Each span carries a <code class="doc-chip">trigger</code> label so the UI can show how the chain started. </p>`,3),c(`div`,vt,[c(`table`,yt,[r[54]||=c(`thead`,null,[c(`tr`,null,[c(`th`,null,`Trigger`),c(`th`,null,`Meaning`)])],-1),c(`tbody`,null,[(t(),l(g,null,f(U,e=>c(`tr`,{key:e.name},[c(`td`,bt,[c(`code`,null,y(e.name),1)]),c(`td`,xt,y(e.desc),1)])),64))])])]),r[61]||=c(`h3`,{class:`doc-h3`},` External correlation (W3C traceparent) `,-1),r[62]||=c(`p`,{class:`doc-prose`},[m(` Send a standard `),c(`code`,{class:`doc-chip`},`traceparent`),m(` header on the inbound HTTP request and Orva makes its trace a child of yours. The same trace_id is echoed back as `),c(`code`,{class:`doc-chip`},`X-Trace-Id`),m(` on every response, so external systems can correlate without parsing bodies. `)],-1),d(o(Z),{code:Vt,lang:`bash`}),r[63]||=h(`<h3 class="doc-h3"> Outlier detection </h3><p class="doc-prose"> Each function maintains an in-memory rolling P95 baseline over its last 100 successful warm executions. An invocation is flagged as an outlier when it has at least 20 baseline samples AND its duration exceeds <strong>P95 × 2</strong>. Cold starts and errors are excluded from the baseline so a flapping function can&#39;t drag it down. The flag and baseline P95 are stored on the execution row and rendered as an amber flag icon next to the span. </p><h3 class="doc-h3"> Where to look </h3><ul class="doc-list"><li><code class="doc-chip">/traces</code>: list of recent traces, filterable by function / status / outlier-only.</li><li><code class="doc-chip">/traces/:id</code>: waterfall + per-span detail. Click a span to jump to its execution in the Invocations log.</li><li><code class="doc-chip">GET /api/v1/traces/{id}</code>: full span tree as JSON. Pair with <code class="doc-chip">list_traces</code> / <code class="doc-chip">get_trace</code> MCP tools for AI agents.</li><li><code class="doc-chip">GET /api/v1/functions/{id}/baseline</code>: current P95/P99/mean for a function.</li></ul>`,4)]),c(`section`,St,[r[65]||=h(`<div class="doc-section-head"><span class="doc-section-num">10</span><div><h2 class="doc-section-title"> Errors &amp; recovery </h2><p class="doc-lede"> Every error response uses the same envelope so log scrapers and retries can match on <code class="doc-chip">code</code>. Deploys are content-addressed; rollback retargets the active version pointer and refreshes warm workers. </p></div></div>`,1),d(o(Z),{code:Ht,lang:`json`}),c(`div`,Ct,[c(`table`,wt,[r[64]||=c(`thead`,null,[c(`tr`,null,[c(`th`,null,`Code`),c(`th`,null,`When you see it`)])],-1),c(`tbody`,null,[(t(),l(g,null,f(yn,e=>c(`tr`,{key:e.code},[c(`td`,Tt,[c(`code`,null,y(e.code),1)]),c(`td`,Et,y(e.when),1)])),64))])])])]),c(`section`,Dt,[r[82]||=h(`<div class="doc-section-head"><span class="doc-section-num">11</span><div><h2 class="doc-section-title"> CLI </h2><p class="doc-lede"> Orva ships a full Linux server binary and a slim cross-platform CLI, both named <code class="doc-chip">orva</code>. They share every client command; only the full build adds <code class="doc-chip">serve</code>, <code class="doc-chip">setup</code>, and <code class="doc-chip">init</code>. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Install (server included) </div><div class="doc-card-body"><code class="doc-chip">curl … install.sh | sh</code><p class="mt-1.5 text-foreground-muted"> Full install: daemon + nsjail + rootfs + CLI. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Install (CLI only) </div><div class="doc-card-body"><code class="doc-chip">curl … install-cli.sh | sh</code><p class="mt-1.5 text-foreground-muted"> ~20 MB binary at <code>/usr/local/bin/orva</code>. No service. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Inside Docker </div><div class="doc-card-body"><code class="doc-chip">docker exec orva orva …</code><p class="mt-1.5 text-foreground-muted"> CLI auto-authed via <code>~/.orva/config.yaml</code>. </p></div></div></div><h3 class="doc-h3"> Authenticate </h3>`,3),c(`p`,Ot,[r[67]||=m(` The CLI reads `,-1),r[68]||=c(`code`,{class:`doc-chip`},`~/.orva/config.yaml`,-1),r[69]||=m(` for `,-1),r[70]||=c(`code`,{class:`doc-chip`},`endpoint`,-1),r[71]||=m(` + `,-1),r[72]||=c(`code`,{class:`doc-chip`},`api_key`,-1),r[73]||=m(`. Generate a key from `,-1),d(a,{to:`/api-keys`,class:`text-foreground hover:text-white underline decoration-dotted underline-offset-4`},{default:n(()=>[...r[66]||=[m(` Keys `,-1)]]),_:1}),r[74]||=m(` in the dashboard, then: `,-1)]),d(o(Z),{code:Ut,lang:`bash`}),r[83]||=c(`h3`,{class:`doc-h3`},` Command index `,-1),c(`div`,kt,[c(`table`,At,[r[75]||=c(`thead`,null,[c(`tr`,null,[c(`th`,null,`Command`),c(`th`,null,`Subcommands`),c(`th`,{class:`hidden md:table-cell`},` Purpose `)])],-1),c(`tbody`,null,[(t(),l(g,null,f(bn,e=>c(`tr`,{key:e.cmd},[c(`td`,jt,[c(`code`,null,`orva `+y(e.cmd),1)]),c(`td`,Mt,y(e.subs),1),c(`td`,Nt,y(e.purpose),1)])),64))])])]),r[84]||=c(`h3`,{class:`doc-h3`},` Common recipes `,-1),c(`div`,Pt,[r[76]||=c(`div`,{class:`doc-microlabel`},` Deploy a function from a directory `,-1),d(o(Z),{code:Wt,lang:`bash`})]),c(`div`,Ft,[r[77]||=c(`div`,{class:`doc-microlabel`},` Invoke + tail logs `,-1),d(o(Z),{code:Gt,lang:`bash`})]),c(`div`,It,[r[78]||=c(`div`,{class:`doc-microlabel`},` Manage KV state `,-1),d(o(Z),{code:Kt,lang:`bash`})]),c(`div`,Lt,[r[79]||=c(`div`,{class:`doc-microlabel`},` Secrets, cron, jobs, webhooks `,-1),d(o(Z),{code:qt,lang:`bash`})]),c(`div`,Rt,[r[80]||=c(`div`,{class:`doc-microlabel`},` System health, metrics, vacuum `,-1),d(o(Z),{code:Jt,lang:`bash`})]),d(o($),{icon:o(w),title:`Shell completion`},{default:n(()=>[...r[81]||=[m(` Generate completion for your shell: `,-1),c(`code`,{class:`doc-chip`},`orva completion bash | sudo tee /etc/bash_completion.d/orva`,-1),m(`, or `,-1),c(`code`,{class:`doc-chip`},`zsh`,-1),m(` / `,-1),c(`code`,{class:`doc-chip`},`fish`,-1),m(` / `,-1),c(`code`,{class:`doc-chip`},`powershell`,-1),m(`. Tab-completes commands, subcommands, and flags. `,-1)]]),_:1},8,[`icon`])])])}}};export{Yt as default};