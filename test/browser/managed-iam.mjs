import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { randomUUID } from 'node:crypto'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'

const required = (name) => {
  const value = process.env[name]
  assert.ok(value, `${name} is required`)
  return value
}
const require = createRequire(import.meta.url)
const { chromium } = require(required('ORVA_MANAGED_PLAYWRIGHT'))
const origin = required('ORVA_MANAGED_BROWSER_ORIGIN')
assert.equal(new URL(origin).hostname, '127.0.0.1', 'browser acceptance must use an owned loopback listener')
const key = required('ORVA_MANAGED_BROWSER_KEY')
const owner = required('ORVA_MANAGED_BROWSER_OWNER')
const output = required('ORVA_MANAGED_BROWSER_OUTPUT')
const password = randomUUID()
await mkdir(output, { recursive: true })
const browser = await chromium.launch({
  executablePath: process.env.ORVA_MANAGED_CHROMIUM,
  headless: true,
  args: ['--disable-dev-shm-usage', '--disable-background-networking', '--no-first-run'],
})
try {
  for (const [name, viewport, isMobile, colorScheme] of [
    ['desktop-day', { width: 1380, height: 1000 }, false, 'light'],
    ['desktop-night', { width: 1380, height: 1000 }, false, 'dark'],
    ['phone-day', { width: 390, height: 844 }, true, 'light'],
    ['phone-night', { width: 390, height: 844 }, true, 'dark'],
  ]) {
    const context = await browser.newContext({ baseURL: origin, viewport, isMobile, hasTouch: isMobile, colorScheme })
    await context.addInitScript((theme) => localStorage.setItem('orva:theme', theme), colorScheme === 'dark' ? 'night' : 'day')
    await context.route('**/*', (route) => new URL(route.request().url()).origin === origin ? route.continue() : route.abort())
    try {
      const auth = await context.request.post(`/api/v1/auth/${name === 'desktop-day' ? 'onboard' : 'login'}`, {
        headers: { 'X-Orva-API-Key': key },
        data: { username: 'iam-fixture', password },
      })
      assert.ok(auth.ok(), `scratch browser authentication failed (${auth.status()})`)
      const page = await context.newPage()
      page.on('pageerror', (e) => { throw e })
      await page.goto('/web/functions')
      const heading = page.getByRole('heading', { name: 'Functions', exact: true })
      try { await heading.waitFor({ timeout: 7000 }) }
      catch (error) {
        const diagnostic = await heading.evaluate((el) => ({
          size: [el.offsetWidth, el.offsetHeight], visibility: getComputedStyle(el).visibility,
          font: getComputedStyle(el).fontFamily, fontStatus: document.fonts.status,
        }))
        throw new Error(`Functions heading not visible: ${JSON.stringify(diagnostic)}`, { cause: error })
      }
      await checkNavigation(page, isMobile, name)
      const permissions = page.getByRole('button', { name: 'Permissions', exact: true }).first()
      await permissions.waitFor()
      await permissions.focus()
      if (isMobile) {
        await permissions.scrollIntoViewIfNeeded()
        const tap = await permissions.evaluate((el) => {
          const r = el.getBoundingClientRect()
          const x = r.x + r.width / 2, y = r.y + r.height / 2
          const points = [[x, y], [x, y - 21], [x, y + 21], [x - 21, y], [x + 21, y]]
          return { hits: points.map(([a, b]) => el.contains(document.elementFromPoint(a, b))), x, y: y - 21 }
        })
        assert.deepEqual(tap.hits, [true, true, true, true, true], '44 px Permissions target is obstructed')
        await page.touchscreen.tap(tap.x, tap.y)
      } else {
        assert.ok(await permissions.evaluate((el) => el === document.activeElement), 'Permissions is not keyboard focusable')
        await permissions.press('Enter')
      }
      await page.getByRole('heading', { name: 'Permission sources' }).waitFor()
      assert.match(page.url(), /\/permissions$/)
      await checkViewport(page)
      await page.screenshot({ path: path.join(output, `${name}-permissions.png`), fullPage: true })
      await page.goto('/web/iam/policies')
      await page.getByRole('heading', { name: 'Policies', exact: true }).waitFor()
      await page.getByRole('button', { name: 'Create reusable policy', exact: true }).waitFor()
      await checkViewport(page)
      await page.screenshot({ path: path.join(output, `${name}-policies.png`), fullPage: true })
      if (name === 'desktop-night') await reusablePolicy(page, context)
      await page.goto(`/web/functions/${owner}/permissions`)
      await page.getByRole('heading', { name: 'Permission sources' }).waitFor()
      if (name === 'desktop-night') {
        await inlinePermissions(page, context)
        await chatApproval(page, context)
      }
      console.log(`${name}: session, navigation, permissions and policy pages passed`)
    } finally {
      await context.close()
    }
  }
} finally {
  await browser.close()
}

async function reusablePolicy(page, context) {
  const name = 'browser-review-fixture'
  const button = (label) => page.getByRole('button', { name: label, exact: true })
  await button('Create reusable policy').click()
  await page.getByLabel('Policy name', { exact: true }).fill(name)
  await addRead(page, 'browser-v1')
  await button('Review policy').click()
  await button('Create reusable policy').click()
  await page.getByRole('heading', { name, exact: true }).waitFor()
  await page.getByText(/Latest verified version 1\./).waitFor()
  const id = page.url().split('/').at(-1)
  assert.match(id, /^[0-9a-f-]{36}$/)
  await button('Create new version').click()
  await addRead(page, 'browser-v2')
  await button('Review policy').click()
  await button('Save new version').click()
  await page.getByText(/Latest verified version 2\./).waitFor()
  const versionOne = await context.request.get(`/api/v1/iam/policies/${id}?version=1`)
  assert.ok(versionOne.ok(), 'version 1 is not readable')
  const body = await versionOne.json()
  assert.deepEqual(body.version.policy.allow.map((entry) => entry.resource.own_kv.key), ['browser-v1'])
  await page.getByText('Not attached to a function.', { exact: true }).waitFor()
  await page.screenshot({ path: path.join(output, 'desktop-night-policy-version.png'), fullPage: true })

  const read = async () => {
    const res = await context.request.get(`/api/v1/functions/${owner}/permissions`)
    assert.ok(res.ok(), 'could not inspect current permission sources')
    return res.json()
  }
  const pin = async () => (await read()).attachments.find((entry) => entry.policy_id === id)
  const attach = async (version, previous) => {
    await page.getByLabel('Pinned version', { exact: true }).fill(version)
    await page.getByRole('checkbox', { name: 'managed-node', exact: true }).check()
    await button('Review attachment').click()
    const confirm = button('Attach reviewed version')
    await confirm.waitFor()
    assert.equal((await pin())?.version, previous, 'preview changed the pinned version')
    await confirm.click()
    await page.getByText(`managed-node · Version ${version} · Enabled`, { exact: true }).waitFor()
    assert.equal((await pin()).version, version)
  }
  const before = (await read()).inline_policy
  await attach('1', undefined)
  await attach('2', '1')
  await page.goto(`/web/functions/${owner}/permissions`)
  const sources = page.getByRole('button', { name: `${name} · Version 2`, exact: true }).locator('xpath=ancestor::li[1]')
  await sources.getByRole('button', { name: 'Review detach', exact: true }).click()
  const detach = button('Detach reviewed policy')
  await detach.waitFor()
  assert.equal((await pin()).version, '2')
  await detach.click()
  await sources.waitFor({ state: 'hidden' })
  assert.equal(await pin(), undefined)
  assert.deepEqual((await read()).inline_policy, before, 'changing a pinned source changed inline permissions')
  console.log('desktop-night: reviewed attachment, rollout and detach preserved inline policy')
}

async function addRead(page, keyName) {
  const button = (label) => page.getByRole('button', { name: label, exact: true })
  await button('Add permission').click()
  await page.getByText('Storage', { exact: true }).last().click()
  await button('Next').click()
  await page.getByRole('checkbox', { name: /^Read a key kv\.get\b/ }).check()
  await button('Next').click()
  await page.getByRole('radio', { name: 'Exact key' }).click()
  await page.getByLabel('Key', { exact: true }).fill(keyName)
  await button('Next').click()
  await button('Add to policy').click()
}

async function inlinePermissions(page, context) {
  const read = async () => {
    const response = await context.request.get(`/api/v1/functions/${owner}/permissions`)
    assert.ok(response.ok(), 'could not read inline policy')
    return (await response.json()).inline_policy.allow.map((entry) => entry.resource.own_kv.key)
  }
  const button = (label) => page.getByRole('button', { name: label, exact: true })
  const before = await read()
  await button('Edit inline permissions').click()
  await addRead(page, 'browser-inline')
  await button('Review changes').click()
  assert.deepEqual(await read(), before, 'review saved the draft without confirmation')
  await page.screenshot({ path: path.join(output, 'desktop-night-inline-review.png'), fullPage: true })
  await button('Save permissions').click()
  await button('Edit inline permissions').waitFor()
  assert.deepEqual((await read()).sort(), [...before, 'browser-inline'].sort())
  await page.goto('/web/functions/managed-node?mode=edit')
  const code = page.locator('.cm-content')
  await code.getByText(/kv\.get/).waitFor()
  assert.equal(await button('Save permissions').count(), 0, 'permission Save leaked into the deployment Editor')
  assert.ok(!(await code.innerText()).includes('browser-inline'), 'policy draft leaked into source')
}

async function chatApproval(page, context) {
  const read = async () => {
    const response = await context.request.get('/api/v1/iam/policies')
    assert.ok(response.ok(), 'could not inspect policies from the browser session')
    return (await response.json()).items.filter((item) => item.name === 'from-chat')
  }
  assert.equal((await read()).length, 0)
  await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Chat', exact: true }).click()
  await page.getByRole('textbox', { name: 'Message the assistant' }).fill('Create an empty named policy')
  await page.getByRole('button', { name: 'Send message' }).click()
  const approve = page.getByRole('button', { name: 'Approve', exact: true })
  await approve.waitFor()
  assert.equal((await read()).length, 0, 'Chat changed the policy list before approval')
  await approve.press('Enter')
  await page.getByText('Named policy created.', { exact: true }).waitFor()
  assert.equal((await read()).length, 1, 'explicit approval did not create exactly one policy')
  await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Policies', exact: true }).click()
  await page.getByRole('button', { name: 'from-chat', exact: true }).waitFor()
  console.log('desktop-night: Chat approval committed once and appeared in Policies')
}

async function checkViewport(page) {
  const geometry = await page.evaluate(() => ({
    open: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }))
  assert.ok(geometry.open <= geometry.viewport, 'page exceeds viewport width')
}

async function checkNavigation(page, mobile, name) {
  const nav = page.getByRole('navigation', { name: 'Main navigation' })
  const items = ['Overview', 'Functions', 'Policies', 'Schedules', 'Jobs', 'Webhooks',
    'Invocations', 'Traces', 'Activity', 'Keys', 'Channels', 'Egress', 'Chat', 'Settings', 'Docs']
  const toggle = page.getByRole('button', { name: 'Open menu' })
  if (mobile) {
    assert.equal(await nav.count(), 0, 'closed mobile drawer exposed to assistive tech')
    await toggle.focus()
    await toggle.press('Enter')
    await nav.waitFor()
  }
  await nav.getByRole('link', { name: 'Policies', exact: true }).waitFor()
  assert.deepEqual(await nav.getByRole('link').allTextContents(), items)
  if (mobile) {
    const first = nav.getByRole('link', { name: 'Overview', exact: true })
    assert.ok(await first.evaluate((el) => el === document.activeElement),
      'opening the drawer did not focus the first link')
    if (name === 'phone-night') {
      await page.screenshot({ path: path.join(output, 'phone-night-navigation.png'), animations: 'disabled' })
    }
    const docs = nav.getByRole('link', { name: 'Docs', exact: true })
    await docs.scrollIntoViewIfNeeded()
    assert.ok(await docs.evaluate((el) => {
      const r = el.getBoundingClientRect()
      return r.height >= 44 && r.bottom <= innerHeight
        && el.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2))
    }), 'final navigation link was not reachable by touch')
    await docs.press('Escape')
    await nav.waitFor({ state: 'hidden' })
    assert.ok(await toggle.evaluate((el) => el === document.activeElement), 'Escape did not restore menu focus')
  }
}
