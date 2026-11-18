// Real-browser driver for the scratch history controls fixture.
// Runs inside the owned VM only. Credentials come from the private ready.json
// and are never written to results.
import fs from 'node:fs'
import path from 'node:path'
import { chromium } from 'playwright'

const lang = process.argv[2]
const privateDir = path.join(process.env.ORVA_HISTORY_PRIVATE, lang)
const evidence = path.join(process.env.ORVA_HISTORY_EVIDENCE, lang)
fs.mkdirSync(evidence, { recursive: true })
const ready = JSON.parse(fs.readFileSync(path.join(privateDir, 'ready.json'), 'utf8'))
const profile = lang === 'node'
  ? { name: 'phone-night', viewport: { width: 390, height: 844 }, theme: 'night', scheme: 'dark', touch: true }
  : { name: 'desktop-day', viewport: { width: 1440, height: 1000 }, theme: 'day', scheme: 'light', touch: false }
const base = ready.url
const results = []
const check = (name, ok, detail = '') => { results.push({ name, ok: !!ok, detail: String(detail).slice(0, 300) }); if (!ok) throw new Error('FAILED: ' + name + ' ' + detail) }
const consoleErrors = [], pageErrors = [], failedStatuses = [], responses = [], consoleTasks = []
let phase = 'normal'
const safe = text => { let s=String(text); for(const k of Object.values(ready.keys)) s=s.split(k).join('[REDACTED]'); return s }
const pathname = url => { try { return new URL(url).pathname } catch { return '' } }

const browser = await chromium.launch()
let exitCode = 0
let cur
const out = { lang, profile: profile.name }

async function open(key) {
  const context = await browser.newContext({ viewport: profile.viewport, colorScheme: profile.scheme, hasTouch: profile.touch })
  await context.addInitScript(([k, theme]) => { localStorage.setItem('orva_api_key', k); localStorage.setItem('orva:theme', theme) }, [key, profile.theme])
  const page = await context.newPage()
  cur = page
  page.setDefaultTimeout(12000)
  page.on('console', m => {
    if(m.type() !== 'error') return
    const record={text:safe(m.text()), phase, path:pathname(m.location().url), line:m.location().lineNumber, column:m.location().columnNumber}
    consoleErrors.push(record)
    consoleTasks.push((async()=>{const args=m.args();try{record.first=await args[0]?.jsonValue();const data=await args[1]?.jsonValue();if(data?.error){record.code=String(data.error.code);record.message=safe(data.error.message)}}catch{}})())
  })
  page.on('pageerror', e => pageErrors.push(String(e).slice(0, 200)))
  page.on('response', r => { if(r.status()>=400){const p=pathname(r.url());failedStatuses.push(r.status()+' '+p);responses.push({status:r.status(),path:p,phase})} })
  await page.goto(base + '/web/history-controls.html')
  // The scratch entry has no inline theme bootstrap; apply the same attribute the app shell sets.
  await page.evaluate(t => { document.documentElement.dataset.theme = t }, profile.theme)
  const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor)
  const lum = bg.match(/\d+(\.\d+)?/g).slice(0, 3).map(Number).reduce((a, b) => a + b, 0) / 3
  check(`theme ${profile.theme} resolved (body ${bg})`, bg === (profile.theme === 'day' ? 'rgb(248, 247, 244)' : 'rgb(14, 13, 9)'), bg)
  return { context, page }
}
const noOverflow = async (page, label) => {
  const o = await page.evaluate(() => ({ s: document.documentElement.scrollWidth, w: window.innerWidth }))
  check('no horizontal overflow ' + label, o.s <= o.w, JSON.stringify(o))
}
const layoutFindings = []
async function containment(page, label) {
  const bad = await page.evaluate(() => {
    const card = document.querySelector('section[aria-label="Deleted-work history"]').getBoundingClientRect()
    return [...document.querySelectorAll('section[aria-label="Deleted-work history"] form :is(textarea,input,button)')]
      .map(e => ({ tag: e.tagName, label: e.getAttribute('aria-label') || e.textContent.trim().slice(0, 30), left:e.getBoundingClientRect().left, cardLeft:card.left, right: e.getBoundingClientRect().right, cardRight: card.right }))
      .filter(r => r.right > r.cardRight + 0.5 || r.left < r.cardLeft - 0.5)
  })
  layoutFindings.push({ label, offenders: bad })
  if (process.env.SOFT_LAYOUT !== '1') check('form controls stay inside the history card ' + label, bad.length === 0, JSON.stringify(bad))
}
const shot = (page, name) => page.screenshot({ path: path.join(evidence, `${lang}-${profile.name}-${name}.png`), fullPage: true })
const apiPost = async (ctx, key, p) => (await ctx.request.post(base + p, { headers: { 'X-Orva-API-Key': key } })).status()
const apiGet = async (ctx, key, p) => { const r = await ctx.request.get(base + p, { headers: { 'X-Orva-API-Key': key } }); return { status: r.status(), body: await r.json().catch(() => null) } }
async function showHistory(page, view) {
  if (view === 'cron') await page.getByRole('button', { name: 'Cron view' }).click(); else await page.getByRole('button', { name: 'Jobs view' }).click()
  const btn = page.getByRole('button', { name: 'History', exact: true })
  await btn.click()
  await page.getByRole('region', { name: 'Deleted-work history' }).waitFor()
  await page.getByText('Loading history…').waitFor({ state: 'hidden' })
}
async function inspect(page, kind, id) {
  await page.getByRole('button', { name: `Inspect ${kind} ${id}` }).click()
  await page.getByRole('heading', { name: `Archived ${kind}` }).waitFor()
  await page.getByText('Loading history…').waitFor({ state: 'hidden' })
}
async function fillDraft(page, kind, tag) {
  await page.getByRole('button', { name: 'Create fresh work' }).click()
  await page.getByRole('heading', { name: new RegExp('New operator-origin ' + kind) }).waitFor()
  await page.getByRole('button', { name: /New target UUID|Choose a new target UUID/ }).click()
  await page.getByRole('menuitemradio').first().click()
  const picked = await page.locator('p.font-mono').filter({ hasText: ready.target }).count()
  check(`${kind} explicit fresh target UUID displayed`, picked >= 1)
  await page.getByLabel('Fresh payload (JSON)').fill(JSON.stringify({ tag }))
  if (kind === 'job') {
    await page.getByLabel('Run at (UTC RFC3339)').fill(new Date().toISOString())
  } else {
    await page.getByLabel('name', { exact: true }).fill('ui-clock-' + lang)
    await page.getByLabel('expression', { exact: true }).fill('@every 2s')
    await page.getByLabel('timezone', { exact: true }).fill('UTC')
    await page.getByLabel('Enable new schedule').check()
  }
}
async function review(page) {
  const b = page.getByRole('button', { name: 'Review fresh work' })
  await b.focus()
  await page.keyboard.press('Enter')
  await page.getByRole('heading', { name: 'Confirm new operator work' }).waitFor()
  const focused = await page.evaluate(() => document.activeElement?.textContent || '')
  check('keyboard review moves focus to confirmation heading', await page.getByRole('heading', { name: 'Confirm new operator work' }).evaluate(el => el === document.activeElement), focused)
}
async function jobCount(ctx) { const r = await apiGet(ctx, ready.keys.admin, `/api/v1/jobs?function_id=${ready.target}&limit=100`); check('actual jobs list for target', r.status === 200); return r.body.jobs.length }
const newIdFrom = async page => {
  const t = await page.getByText(/Created new operator work/).first().innerText()
  const m = t.match(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i)
  check('success notice contains new work id', m, t)
  return m[0]
}
async function awaitEffect(ctx, kind, id) {
  const deadline = Date.now() + 30000
  while (Date.now() < deadline) {
    const r = await apiGet(ctx, ready.keys.admin, `/api/scratch/effect/${kind}/${id}?target=${ready.target}`)
    if (r.status === 200 && r.body.sdk_effect_matches) return true
    await new Promise(r => setTimeout(r, 250))
  }
  return false
}

try {
  // 1. Read-only session: inspection works, mutation controls absent, no old payload.
  {
    const { context, page } = await open(ready.keys.reader)
    for (const [view, kind, id] of [['jobs', 'job', ready.job], ['cron', 'cron', ready.cron]]) {
      await showHistory(page, view)
      await inspect(page, kind, id)
      const text = await page.locator('body').innerText()
      check(`read-only ${kind}: metadata-only text`, text.includes('Original payload is unavailable'))
      check(`read-only ${kind}: read-only notice`, text.includes('Read-only access'))
      check(`read-only ${kind}: no old payload`, !text.includes('old-private'))
      check(`read-only ${kind}: no Create/Review controls`, (await page.getByRole('button', { name: /Create fresh work|Review purge/ }).count()) === 0)
      await noOverflow(page, `read-only ${kind}`)
    }
    await shot(page, 'readonly')
    await context.close()
  }
  // 2. Admin (ui key): capacity refusal, then pending-confirmation credential revocation.
  const { context, page } = await open(ready.keys.ui)
  await showHistory(page, 'jobs')
  await inspect(page, 'job', ready.job)
  await fillDraft(page, 'job', 'ui-job-denied')
  await containment(page, 'job form')
  await review(page)
  await noOverflow(page, 'job review')
  await shot(page, 'job-review')
  // KeepAlive: leave and return; draft survives but the review/confirm must be redone.
  await page.getByRole('button', { name: 'Cron view' }).click()
  await page.getByRole('button', { name: 'Jobs view' }).click()
  await page.getByText('Loading history…').waitFor({ state: 'hidden' })
  check('KeepAlive return: draft retained', (await page.getByLabel('Fresh payload (JSON)').inputValue()).includes('ui-job-denied'))
  check('KeepAlive return: confirmation withdrawn', (await page.getByRole('button', { name: 'Confirm new work' }).count()) === 0)
  await review(page)
  // capacity refusal (real server 409), no false acknowledgement
  check('capacity block fixture', (await apiPost(context, ready.keys.admin, '/api/scratch/capacity/block')) === 204)
  const before = await jobCount(context)
  phase='capacity-denial'
  await page.getByRole('button', { name: 'Confirm new work' }).click()
  await page.getByText(/Conflict or capacity refusal/).waitFor()
  check('capacity refusal keeps draft', (await page.getByLabel('Fresh payload (JSON)').inputValue()).includes('ui-job-denied'))
  check('capacity refusal: no false acknowledgement', (await page.getByText(/Created new operator work/).count()) === 0)
  check('capacity refusal: stale warning shown', (await page.getByText(/stale or unknown/).count()) >= 1)
  check('capacity refusal: no work created', (await jobCount(context)) === before)
  await shot(page, 'capacity-refusal'); phase='normal'
  check('capacity reset fixture', (await apiPost(context, ready.keys.admin, '/api/scratch/capacity/reset')) === 204)
  await page.getByRole('button', { name: 'Refresh history' }).click()
  await page.getByText(/stale or unknown/).waitFor({ state: 'hidden' })
  await review(page)
  // revoke credential between review and confirm
  check('revoke ui credential fixture', (await apiPost(context, ready.keys.admin, '/api/scratch/revoke-ui')) === 204); phase='revoked-denial'
  await page.getByRole('button', { name: 'Confirm new work' }).click()
  await page.getByText(/Request failed or outcome unknown/).waitFor()
  check('revoked credential: no false acknowledgement', (await page.getByText(/Created new operator work/).count()) === 0)
  check('revoked credential: draft retained', (await page.getByLabel('Fresh payload (JSON)').inputValue()).includes('ui-job-denied'))
  check('revoked credential: no new work/audit', (await jobCount(context)) === before)
  await shot(page, 'revoked-confirmation'); phase='normal'
  await context.close()
  // 3. Admin session: UI job and cron reissue -> actual pooled SDK effects.
  const admin = await open(ready.keys.admin)
  await showHistory(admin.page, 'jobs')
  await inspect(admin.page, 'job', ready.job)
  // premature purge is server-refused (not yet eligible): UI keeps its truth
  await admin.page.getByRole('button', { name: 'Review purge' }).click()
  phase='ineligible-denial'
  await admin.page.getByRole('button', { name: 'Confirm purge' }).click()
  await admin.page.getByText(/Request failed or outcome unknown/).waitFor()
  const still = await admin.context.request.post(base + '/api/v1/work-history/get', { headers: { 'X-Orva-API-Key': ready.keys.admin }, data: { kind: 'job', id: ready.job } })
  check('ineligible purge refused by server; archive remains', still.status() === 200); phase='normal'
  await admin.page.getByRole('button', { name: 'Refresh history' }).click()
  await admin.page.getByText(/stale or unknown/).waitFor({ state: 'hidden' })
  await inspect(admin.page, 'job', ready.job)
  await fillDraft(admin.page, 'job', 'ui-job-' + lang)
  await review(admin.page)
  await admin.page.getByRole('button', { name: 'Confirm new work' }).click()
  await admin.page.getByText(/Created new operator work/).waitFor()
  const jobId = await newIdFrom(admin.page)
  check('UI new job id is not the archive id', jobId !== ready.job)
  check('actual SDK effect for UI-created job', await awaitEffect(admin.context, 'job', jobId))
  await noOverflow(admin.page, 'job success')
  await shot(admin.page, 'job-success')
  // Cron view
  await showHistory(admin.page, 'cron')
  await inspect(admin.page, 'cron', ready.cron)
  await fillDraft(admin.page, 'cron', 'ui-cron-' + lang)
  await containment(admin.page, 'cron form')
  await review(admin.page)
  await admin.page.getByRole('button', { name: 'Confirm new work' }).click()
  await admin.page.getByText(/Created new operator work/).waitFor()
  const cronId = await newIdFrom(admin.page)
  check('UI new cron id is not the archive id', cronId !== ready.cron)
  check('actual SDK effect for UI-created cron', await awaitEffect(admin.context, 'cron', cronId))
  await noOverflow(admin.page, 'cron success')
  await shot(admin.page, 'cron-success')
  // QA-only root eligibility was injected BEFORE browser; preserve premature job-purge negative above.
  check('eligible purge root supplied', !!ready.qa_purge_id)
  await inspect(admin.page, 'delete', ready.qa_purge_id)
  await admin.page.getByRole('button', {name:'Review purge', exact:true}).click()
  await admin.page.getByRole('button', {name:'Confirm purge', exact:true}).click()
  await admin.page.getByText('Selected metadata purged. Audit retained.', {exact:true}).waitFor()
  await admin.page.getByText('Loading history…').waitFor({state:'hidden'})
  check('real UI eligible purge removes selected archive row', await admin.page.getByRole('button',{name:`Inspect delete ${ready.qa_purge_id}`,exact:true}).count()===0)
  await shot(admin.page,'eligible-purge-success')
  out.purge_id=ready.qa_purge_id
  out.job_new_id = jobId
  out.cron_new_id = cronId
  await admin.context.close()
  // Exactly the three deliberate server refusals: ineligible purge (400), capacity (409), revoked credential (401).
  const expected = ['400 /api/v1/work-history/purge', '401 /api/v1/work-history/reissue', '409 /api/v1/work-history/reissue']
  check('exactly the deliberate server refusals occurred', JSON.stringify([...failedStatuses].sort()) === JSON.stringify(expected), failedStatuses.join(','))
  check('no page errors', pageErrors.length === 0, pageErrors.join('|'))
  out.expected_denials = failedStatuses.length
  out.console_errors = consoleErrors.length
  await Promise.all(consoleTasks)
  const allowed={
    'capacity-denial':{status:409,path:'/api/v1/work-history/reissue',code:'CONFLICT',message:'history request refused'},
    'revoked-denial':{status:401,path:'/api/v1/work-history/reissue',code:'UNAUTHORIZED',message:'authentication required'},
    'ineligible-denial':{status:400,path:'/api/v1/work-history/purge',code:'VALIDATION',message:'history request refused'},
  }
  for(const r of consoleErrors){const a=allowed[r.phase];const correlated=a&&responses.some(x=>x.phase===r.phase&&x.status===a.status&&x.path===a.path)
    const api=correlated&&r.first==='API Error:'&&r.code===a.code&&r.message===a.message
    const resource=correlated&&r.path===a.path&&r.text===`Failed to load resource: the server responded with a status of ${a.status} (${a.status===409?'Conflict':a.status===401?'Unauthorized':'Bad Request'})`
    r.allowed=!!(api||resource);r.category=api?'API Error':resource?'resource':'UNEXPLAINED'
  }
  for(const [p,a] of Object.entries(allowed)){
    check('exact expected response '+p,responses.filter(r=>r.phase===p&&r.status===a.status&&r.path===a.path).length===1)
    check('exact API/resource console pair '+p,consoleErrors.filter(r=>r.phase===p&&r.allowed).length===2)
  }
  check('no unexplained console errors',consoleErrors.every(r=>r.allowed),consoleErrors.filter(r=>!r.allowed).map(r=>r.text).join('|'))

} catch (e) {
  exitCode = 1
  out.error = String(e.message || e).slice(0, 500)
  try { await cur.screenshot({ path: path.join(evidence, `${lang}-failure.png`), fullPage: true }); out.failure_state_captured = true } catch {}
} finally {
  out.checks = results
  out.layout_findings = layoutFindings
  out.failed_statuses = failedStatuses
  out.console_lines = consoleErrors
  out.response_statuses = responses
  out.page_errors = pageErrors
  fs.writeFileSync(path.join(evidence, 'results.json'), JSON.stringify(out, null, 2))
  fs.writeFileSync(path.join(privateDir, 'ui-results.json'), JSON.stringify({ job_new_id: out.job_new_id, cron_new_id: out.cron_new_id, purge_id: out.purge_id }))
  await browser.close()
  process.exit(exitCode)
}
