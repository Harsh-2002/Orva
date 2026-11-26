import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { chromium } from 'playwright-core'
import { resolveBrowser } from './lib/env.mjs'

const [fresh, used, usedKeyFile] = process.argv.slice(2)
assert.ok(fresh && used && usedKeyFile && fresh !== used,
  'Usage: node onboarding.mjs <virgin loopback origin> <second virgin loopback origin> <second bootstrap key file>')
for (const origin of [fresh, used]) {
  assert.equal(new URL(origin).origin, origin)
  assert.equal(new URL(origin).hostname, '127.0.0.1', 'this test claims two disposable servers')
}
const usedKey = (await readFile(usedKeyFile, 'utf8')).trim()
assert.ok(usedKey)

const executablePath = resolveBrowser()
assert.ok(executablePath, 'set ORVA_BROWSER to a Chromium executable')
const browser = await chromium.launch({ executablePath, args: ['--no-sandbox', '--disable-dev-shm-usage'] })
const path = '/api/v1/auth/onboard'
const username = `qa-${randomUUID()}`
const password = `Qa1!${randomUUID()}`

async function openSetup(page, origin) {
  const status = await page.request.get(`${origin}/api/v1/auth/status`)
  assert.equal(status.ok(), true)
  assert.equal((await status.json()).has_user, false)
  await page.goto(`${origin}/web/onboarding`)
  await page.getByLabel('Username', { exact: true }).fill(username)
  await page.getByLabel('Password', { exact: true }).fill(password)
}

function submit(page) {
  return Promise.all([
    page.waitForResponse((response) =>
      response.url().endsWith(path) && response.request().method() === 'POST'),
    page.getByRole('button', { name: 'Create account' }).click(),
  ]).then(([response]) => response)
}

async function signedIn(page) {
  await page.waitForURL((url) => url.pathname === '/web/')
  const me = await page.request.get('/api/v1/auth/me')
  assert.equal(me.status(), 200)
  assert.equal((await me.json()).username, username)
}

try {
  const desktop = await browser.newPage({ baseURL: fresh, colorScheme: 'dark' })
  const phone = await browser.newPage({
    baseURL: used, viewport: { width: 375, height: 812 },
    hasTouch: true, isMobile: true, colorScheme: 'light',
  })
  assert.equal((await phone.request.post('/api/v1/functions', {
    headers: { 'X-Orva-API-Key': usedKey },
    data: { name: 'qa-api-first', runtime: 'node' },
  })).status(), 201)
  await openSetup(phone, used)
  const key = phone.getByLabel('Admin API key', { exact: true })
  assert.equal(await key.count(), 0)
  assert.equal((await submit(phone)).status(), 401)
  await phone.getByRole('alert').waitFor()
  assert.equal(await key.isVisible(), true, 'after API-first refusal, the dashboard must offer an admin-key field')
  await phone.waitForFunction(() => document.activeElement?.id === 'onboard-admin-key')
  assert.equal(await key.getAttribute('type'), 'password')
  assert.equal(await phone.getByLabel('Password', { exact: true }).inputValue() === password, true)
  assert.equal(await phone.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true)
  const scoped = await phone.request.post('/api/v1/keys', {
    headers: { 'X-Orva-API-Key': usedKey },
    data: { name: 'qa-read-only', permissions: ['read'] },
  })
  assert.equal(scoped.status(), 201)
  const readOnlyKey = (await scoped.json()).key
  assert.ok(readOnlyKey)
  for (const wrongKey of [`orva_${randomUUID()}`, readOnlyKey]) {
    await key.fill(wrongKey)
    assert.equal((await submit(phone)).status(), 401)
    await phone.waitForFunction(() => document.querySelector('#onboard-admin-key')?.value === '')
    assert.equal(await key.getAttribute('aria-invalid'), 'true')
  }
  assert.equal((await (await phone.request.get('/api/v1/auth/status')).json()).has_user, false)
  if (process.env.ORVA_ONBOARDING_SCREENSHOT) {
    await phone.getByLabel('Password', { exact: true }).clear()
    await phone.screenshot({ path: process.env.ORVA_ONBOARDING_SCREENSHOT })
    await phone.getByLabel('Password', { exact: true }).fill(password)
  }
  await key.fill(usedKey)
  const accepted = await submit(phone)
  assert.equal(accepted.status(), 200)
  assert.equal((await accepted.request().headerValue('x-orva-api-key')) === usedKey, true)
  assert.deepEqual(Object.keys(accepted.request().postDataJSON()).sort(), ['password', 'username'])
  await signedIn(phone)
  assert.equal(await phone.evaluate((secret) =>
    [localStorage, sessionStorage].every((storage) =>
      Object.values(storage).every((value) => !value.includes(secret))), usedKey), true)
  console.log('PASS API-first: unkeyed and invalid-key attempts refused; valid admin key completes browser setup')

  await openSetup(desktop, fresh)
  assert.equal(await desktop.getByLabel('Admin API key', { exact: true }).count(), 0)
  const virgin = await submit(desktop)
  assert.equal(virgin.status(), 200)
  assert.equal(await virgin.request().headerValue('x-orva-api-key'), null)
  await signedIn(desktop)
  console.log('PASS virgin: no key field or header; dashboard session established')
} finally {
  await browser.close()
}
