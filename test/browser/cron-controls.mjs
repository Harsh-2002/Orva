import assert from 'node:assert/strict'
import { chromium } from '/browser/node_modules/playwright/index.mjs'

const phone = process.env.CRON_VIEWPORT === 'phone'
const viewport = { width: phone ? 390 : 1440, height: phone ? 844 : 1000 }
const theme = phone ? 'night' : 'day'
const base = process.env.CRON_URL
const id = process.env.CRON_ID
const owner = process.env.CRON_OWNER
const runtime = process.env.CRON_RUNTIME
assert.ok(['node', 'python'].includes(runtime))
const capture = (page, state) => page.screenshot({ path: `/evidence/browser-${runtime}-${process.env.CRON_VIEWPORT}-${theme}-${state}.png`, fullPage: true })
const errors = []
let expectedFailure = false
const browser = await chromium.launch({ executablePath: '/usr/bin/chromium', args: ['--no-sandbox'] })
const contextOptions = { viewport, colorScheme: phone ? 'dark' : 'light', isMobile: phone, hasTouch: phone }
const context = await browser.newContext(contextOptions)
const configure = async (ctx, key) => ctx.addInitScript(({ key, theme }) => {
  localStorage.setItem('orva_api_key', key)
  localStorage.setItem('orva:theme', theme)
}, { key, theme })
const state = async () => {
  const res = await fetch(`${base}/api/v1/cron`, { headers: { 'X-Orva-API-Key': process.env.CRON_ADMIN } })
  assert.equal(res.status, 200)
  return (await res.json()).schedules.find(row => row.id === id)
}
const geometry = async page => {
  const bad = await page.evaluate(() => {
    const width = document.documentElement.clientWidth
    const elements = [...document.querySelectorAll('button,input,[data-schedule-id],main')]
    return elements.filter(el => {
      const b = el.getBoundingClientRect()
      return b.width > 0 && b.height > 0 && (b.left < -1 || b.right > width + 1)
    }).map(el => el.tagName)
  })
  assert.deepEqual(bad, [], 'control or row overflows viewport')
}
try {
  await configure(context, process.env.CRON_ADMIN)
  const page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => { if (message.type() === 'error' && !expectedFailure) errors.push(message.text()) })
  await page.goto(`${base}/cron-controls.html`)
  const row = page.locator(`[data-schedule-id="${id}"]:visible`)
  await row.getByText('suspended', { exact: true }).waitFor()
  assert.match(await row.innerText(), /Source: function/)
  assert.match(await row.innerText(), /Config revision:/)
  assert.match(await row.innerText(), /revoked|policy_denied/)
  assert.match(await page.locator('main').innerText(), /Legacy origin requires separate adoption review/)
  await geometry(page)

  // A real external disable invalidates the inspected UI CAS, without adoption.
  const original = await state()
  const disable = await fetch(`${base}/api/v1/functions/${owner}/cron/${id}`, {
    method: 'PUT', headers: { 'X-Orva-API-Key': process.env.CRON_ADMIN, 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled: false, expected_config_revision: original.config_revision, expected_authority_generation: original.authority_generation }),
  })
  assert.equal(disable.status, 200)
  expectedFailure = true
  await row.getByRole('button', { name: 'Resume schedule', exact: true }).focus()
  await page.keyboard.press('Enter')
  await page.getByRole('dialog').waitFor()
  await geometry(page)
  await capture(page, 'confirmation')
  await page.getByRole('dialog').getByRole('button', { name: 'Resume schedule', exact: true }).press('Enter')
  await page.getByText('Schedule changed or is not eligible. Refresh and review before trying again.', { exact: true }).waitFor()
  await row.getByText('unknown (stale snapshot)', { exact: true }).waitFor()
  assert.equal(await row.getByRole('button', { name: 'Resume schedule', exact: true }).count(), 0)
  await page.getByRole('button', { name: 'Refresh', exact: true }).click()
  await row.getByText('suspended', { exact: true }).waitFor()
  assert.equal(await page.getByText('Schedule changed or is not eligible. Refresh and review before trying again.', { exact: true }).count(), 1)
  expectedFailure = false

  const before = await state()
  await row.getByRole('button', { name: 'Resume schedule', exact: true }).focus()
  await page.keyboard.press('Enter')
  await page.getByRole('dialog').getByRole('button', { name: 'Resume schedule', exact: true }).press('Enter')
  await row.getByText('active', { exact: true }).waitFor()
  const resumed = await state()
  assert.equal(resumed.origin_kind, 'function')
  assert.equal(resumed.creator_id, owner)
  assert.equal(resumed.creation_caller_revision, original.creation_caller_revision)
  assert.ok(BigInt(resumed.config_revision) > BigInt(before.config_revision))
  const next = Date.parse(resumed.next_run_at)
  assert.ok(next > Date.now(), 'UI resume must select a strictly future fire')
  const deadline = Date.now() + 6000
  while (Date.now() < deadline) {
    const current = await state()
    if (Date.parse(current.last_run_at) >= next && current.last_status === 'ok') break
    await page.waitForTimeout(25)
  }
  assert.ok(Date.parse((await state()).last_run_at) >= next, 'UI resume must lead to real Scheduler firing')
  assert.equal(await page.getByRole('button', { name: 'Refresh', exact: true }).evaluate(el => document.activeElement === el), true, 'post-resume focus returns to Refresh')
  await geometry(page)
  await capture(page, 'active')

  expectedFailure = true
  await page.route('**/api/v1/cron', route => route.abort('connectionfailed'), { times: 1 })
  await page.getByRole('button', { name: 'Refresh', exact: true }).click()
  await row.getByText('unknown (stale snapshot)', { exact: true }).waitFor()
  assert.equal(await page.getByRole('button', { name: 'New schedule', exact: true }).isDisabled(), true)
  await page.getByRole('link', { name: 'Other view', exact: true }).click()
  await page.getByRole('link', { name: 'Schedules', exact: true }).click()
  await row.getByText('active', { exact: true }).waitFor()
  expectedFailure = false

  const readContext = await browser.newContext(contextOptions)
  await configure(readContext, process.env.CRON_READER)
  const readonly = await readContext.newPage()
  readonly.on('pageerror', error => errors.push(error.message))
  readonly.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
  await readonly.goto(`${base}/cron-controls.html`)
  await readonly.getByText('Read-only. Schedule management requires current admin authority.', { exact: true }).waitFor()
  for (const name of ['Resume schedule', 'New schedule', 'Delete', 'Edit', 'Enable schedule', 'Disable schedule']) {
    assert.equal(await readonly.getByRole('button', { name, exact: true }).count(), 0, `read-only exposes ${name}`)
  }
  await geometry(readonly)
  await capture(readonly, 'readonly')
  await readContext.close()
  assert.deepEqual(errors, [], 'unexpected browser errors')
  console.log(JSON.stringify({ viewport, theme, keyboardResume: 'PASS', staleCAS409: 'PASS', nextFutureRealFire: 'PASS', source: 'PASS', readonly: 'PASS', networkFailure: 'PASS', activation: 'PASS', overflow: 'PASS', unexpectedErrors: 0 }))
} finally {
  await context.close()
  await browser.close()
}
