import { session } from './lib/env.mjs'
const BASE='http://127.0.0.1:18500'
process.argv.push('--username','orva','--password','orva-dev-2026')
const a = await session(BASE)
const H={cookie:`session_token=${a.cookie}`,'content-type':'application/json'}
const fns = [
  { name:'py-logs', runtime:'python', entrypoint:'handler.py', code:`import json
import orva

def handler(event):
    print("print(): plain stdout, rerouted to stderr by the adapter")
    orva.log.info("structured info line", {"lang": "python", "step": 1})
    orva.log.warn("structured warn line", {"n": 7})
    body = event.get("body") or "{}"
    name = json.loads(body).get("name", "World") if body else "World"
    return {"statusCode": 200, "headers": {"Content-Type": "application/json"},
            "body": json.dumps({"message": f"Hello {name}!", "language": "Python"})}
` },
  { name:'node-logs', runtime:'node', entrypoint:'handler.js', code:`const orva = require('orva')
exports.handler = async (event) => {
  console.log('console.log(): plain stdout, rerouted to stderr by the adapter')
  orva.log.info('structured info line', { lang: 'node', step: 1 })
  orva.log.error('structured error line', { code: 'E_DEMO' })
  const name = JSON.parse(event.body || '{}').name || 'World'
  return { statusCode: 200, headers: { 'Content-Type': 'application/json' },
           body: JSON.stringify({ message: \`Hello \${name}!\`, language: 'Node' }) }
}
` },
]
for (const f of fns) {
  let r = await fetch(`${BASE}/api/v1/functions`,{method:'POST',headers:H,body:JSON.stringify({
    name:f.name, runtime:f.runtime, entrypoint:f.entrypoint, memory_mb:128, cpus:0.5, timeout_ms:30000})})
  let id=(await r.json()).id
  if(!id){const l=await(await fetch(`${BASE}/api/v1/functions`,{headers:H})).json()
    id=(l.functions||[]).find(x=>x.name===f.name)?.id}
  r=await fetch(`${BASE}/api/v1/functions/${id}/deploy-inline`,{method:'POST',headers:H,
    body:JSON.stringify({code:f.code, filename:f.entrypoint})})
  console.log(f.name, 'deploy', r.status, id)
  await new Promise(x=>setTimeout(x,3000))
  const inv=await fetch(`${BASE}/fn/${id}`,{method:'POST',headers:H,body:'{"name":"World"}'})
  console.log(' ', f.name, 'invoke', inv.status, (await inv.text()).slice(0,90))
}
