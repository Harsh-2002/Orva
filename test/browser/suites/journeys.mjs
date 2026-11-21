// Interaction flows, driven the way an operator drives them.
//
// The page suites assert the state of a rendered route. These assert what
// happens when you press things, which is where the worst defect in this
// project's history lived: ConfirmDialog bound Enter to `window` and settled
// true regardless of focus, so pressing Enter on the CANCEL button of a
// destructive dialog performed the deletion. Every destructive path in the
// product routes through that one component. It was reproduced here first, at
// which point it stopped being an opinion.
//
import { randomUUID } from 'node:crypto'

export const meta = {
  id: 'journeys',
  title: 'Interaction flows an operator actually performs',
}

async function checkDeleteConfirmation(page, base, report, where) {
  // Never remove a function the viewport matrix or another tester is using.
  const name = `browser-delete-${randomUUID()}`
  const created = await page.request.post(`${base}/api/v1/functions`, {
    data: { name, runtime: 'node' },
  })
  if (!created.ok()) throw new Error(`create dialog fixture: HTTP ${created.status()}`)
  const { id } = await created.json()
  if (!id) throw new Error('create dialog fixture: no function ID')
  const url = `${base}/api/v1/functions/${id}`
  let deleteRequests = 0
  const trackDelete = (request) => {
    if (request.method() === 'DELETE' && request.url() === url) deleteRequests++
  }
  page.on('request', trackDelete)
  try {
    await page.goto(`${base}/web/functions`, { waitUntil: 'networkidle', timeout: 30000 })
    const search = page.getByPlaceholder('Search by name, runtime, or function id…')
    await search.fill(name)
    const row = page.getByRole('row').filter({ has: page.getByRole('link', { name, exact: true }) })
    const dialog = page.getByRole('dialog')
    const open = async () => {
      await row.getByRole('button', { name: 'Delete function' }).click()
      await dialog.waitFor()
    }
    const checkCancel = async (key, label) => {
      await page.keyboard.press(key)
      await dialog.waitFor({ state: 'hidden' })
      const status = (await page.request.get(url)).status()
      report.record('journeys', where, label, deleteRequests === 0 && status === 200
        ? [] : [`sent ${deleteRequests} DELETE requests; fixture GET returned ${status}`])
    }

    await open()
    const focused = () => page.evaluate(() => document.activeElement?.textContent.trim())
    const initial = await focused()
    report.record('journeys', where, 'focus lands on the non-destructive control',
      initial === 'Cancel' ? [] : [`focus landed on "${initial}", not Cancel`])
    await checkCancel('Enter', 'Enter on Cancel does not confirm a destructive dialog')

    await open()
    await checkCancel('Escape', 'Escape closes a confirm without acting')

    await open()
    await page.keyboard.press('Tab')
    const next = await focused()
    const [response] = await Promise.all([
      page.waitForResponse((r) => r.url() === url && r.request().method() === 'DELETE'),
      page.keyboard.press('Enter'),
    ])
    const status = (await page.request.get(url)).status()
    report.record('journeys', where, 'the confirm button is operable by keyboard',
      next === 'Delete' && response.ok() && status === 404 && deleteRequests === 1
        ? [] : [`Tab reached "${next}"; DELETE returned ${response.status()}, fixture GET returned ${status}`])
  } finally {
    page.off('request', trackDelete)
    const current = await page.request.get(url)
    if (current.status() !== 404) {
      const cleanup = await page.request.delete(url)
      if (!cleanup.ok() && cleanup.status() !== 404) {
        report.fail('journeys', where, 'destructive fixture cleaned up', `HTTP ${cleanup.status()}`)
      }
    }
  }
}

// Data rows only. An empty table still renders one <tr> holding the "nothing
// here yet" message as a single cell spanning every column, and counting that
// as data made the keyboard-reachability check look for a focusable control
// inside a placeholder and report a Level A failure against an empty list.
const countRows = (page) => page.evaluate(() => {
  const rows = [...document.querySelectorAll('tbody tr')]
  return rows.filter((tr) => {
    const cells = tr.querySelectorAll('td')
    if (cells.length === 0) return false
    if (cells.length === 1 && cells[0].hasAttribute('colspan')) return false
    return true
  }).length
})

export async function run({ context, base, report, destructive }) {
  const page = await context.newPage()
  const where = 'laptop'

  await page.goto(`${base}/web/functions/new`, { waitUntil: 'networkidle', timeout: 30000 })
  const editor = page.locator('.cm-content').first()
  await editor.waitFor({ timeout: 10000 })
  report.record('journeys', where, 'code textbox has an accessible name',
    await editor.getAttribute('aria-label') === 'Function source code'
      ? [] : ['CodeMirror textbox has no accessible name'])
  await editor.fill('def handler(:\n    pass')
  const issue = page.locator('.cm-lint-marker-error').first()
  const issueVisible = await issue.waitFor({ state: 'visible', timeout: 5000 }).then(() => true, () => false)
  report.record('journeys', where, 'invalid Python marks the error in the gutter',
    issueVisible ? [] : ['editor did not mark the syntax error'])
  const announcement = page.locator('.sr-only[role="status"]').first()
  const errorAnnouncement = await announcement.textContent()
  report.record('journeys', where, 'screen readers get the first error location',
    /^Syntax error at line \d+, column \d+\.$/.test(errorAnnouncement?.trim() || '')
      ? [] : [`missing error location: ${errorAnnouncement}`])
  report.record('journeys', where, 'syntax hint has no separate status or problems panel',
    await page.locator('.cm-panel-lint').count() === 0 &&
      await page.getByText('No syntax issues', { exact: true }).count() === 0
      ? [] : ['an extra syntax status or panel is visible'])
  await editor.fill('def handler(event):\n    return 200')
  const cleared = await issue.waitFor({ state: 'hidden', timeout: 5000 }).then(() => true, () => false)
  report.record('journeys', where, 'corrected Python clears the gutter marker',
    cleared ? [] : ['editor did not clear the syntax marker after correction'])
  report.record('journeys', where, 'corrected code clears the screen-reader error',
    (await announcement.textContent())?.trim() === ''
      ? [] : ['the screen-reader error remained after correction'])

  const mobileCodePage = await context.newPage()
  await mobileCodePage.setViewportSize({ width: 390, height: 844 })
  await mobileCodePage.goto(`${base}/web/functions/new`, { waitUntil: 'networkidle', timeout: 30000 })
  const mobileEditor = mobileCodePage.locator('.cm-content').first()
  await mobileEditor.waitFor({ timeout: 10000 })
  await mobileEditor.fill(`def handler(event):\n    return "${'x'.repeat(200)}"`)
  const phoneOverflow = await mobileCodePage.locator('.cm-scroller').evaluate((el) => el.scrollWidth - el.clientWidth)
  report.record('journeys', 'phone', 'long code lines soft-wrap instead of clipping',
    phoneOverflow <= 1 ? [] : [`editor still scrolls horizontally by ${phoneOverflow}px`])
  await mobileCodePage.setViewportSize({ width: 1280, height: 800 })
  // The editor re-applies wrapping from a media-query change event, which lands
  // a frame or two after the resize: wait for the state rather than racing it.
  let desktopOverflow = 0
  for (let i = 0; i < 25 && desktopOverflow <= 1; i++) {
    desktopOverflow = await mobileCodePage.locator('.cm-scroller').evaluate((el) => el.scrollWidth - el.clientWidth)
    if (desktopOverflow <= 1) await mobileCodePage.waitForTimeout(100)
  }
  report.record('journeys', where, 'desktop restores horizontal code scrolling',
    desktopOverflow > 1 ? [] : ['long code line remained wrapped after resizing to desktop'])
  await mobileCodePage.close()

  // ---- A detail view is reachable without a pointer ----------------------
  await page.goto(`${base}/web/invocations`, { waitUntil: 'networkidle', timeout: 30000 })
  await page.waitForTimeout(700)
  const hasRows = await countRows(page)
  if (!hasRows) {
    report.skip('journeys', where, 'a row detail opens from the keyboard', 'no invocations on this instance')
  } else {
    const reached = await page.evaluate(async () => {
      // Same placeholder guard as countRows: pick the first row that is data.
      const first = [...document.querySelectorAll('tbody tr')].find((tr) => {
        const cells = tr.querySelectorAll('td')
        return cells.length > 1 || (cells.length === 1 && !cells[0].hasAttribute('colspan'))
      })
      if (!first) return { ok: false, why: 'no rows' }
      // Something inside the row must be focusable, or the drawer behind it is
      // pointer-only. This is the shape of the Level A failure that shipped.
      const focusable = first.querySelector(
        'button, a[href], [tabindex]:not([tabindex="-1"]), input, select, textarea',
      )
      return { ok: !!focusable, why: focusable ? '' : 'no focusable control inside the row' }
    })
    report.record('journeys', where, 'a row detail opens from the keyboard',
      reached.ok ? [] : [reached.why])
  }

  // ---- Destructive confirm: Enter must not confirm from the Cancel button ---
  if (!destructive) {
    report.skip('journeys', where, 'Enter on Cancel does not confirm a destructive dialog',
      'pass --destructive to run flows that delete data')
  } else {
    await checkDeleteConfirmation(page, base, report, where)
  }

  // ---- The mobile drawer opens and closes --------------------------------
  const mobile = await context.browser().newContext({
    viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true,
  })
  const cookies = await context.cookies()
  await mobile.addCookies(cookies)
  const mp = await mobile.newPage()
  // The laptop page from the enclosing run is still open and foreground, which
  // leaves this one backgrounded -- and a backgrounded page has its
  // requestAnimationFrame throttled. Playwright decides an element is
  // "stable" by comparing its box across animation frames, so without this the
  // click waits out its full timeout on an element that is visible, enabled and
  // unobstructed the whole time.
  await mp.bringToFront()
  await mp.goto(`${base}/web/functions`, { waitUntil: 'networkidle', timeout: 30000 })
  await mp.waitForTimeout(600)
  const toggle = mp.locator('button[aria-controls="primary-navigation"]').first()
  if (!(await toggle.count())) {
    report.fail('journeys', 'phone', 'the navigation drawer opens and closes',
      'no control with aria-controls="primary-navigation" in the mobile top bar')
  } else {
    const state = async () => mp.evaluate(() => {
      const btn = document.querySelector('button[aria-controls="primary-navigation"]')
      const nav = document.getElementById('primary-navigation')
      return {
        expanded: btn?.getAttribute('aria-expanded'),
        onScreen: nav ? nav.getBoundingClientRect().left >= -1 : false,
      }
    })
    const before = await state()
    await toggle.click()
    await mp.waitForTimeout(450)
    const opened = await state()
    await toggle.click()
    await mp.waitForTimeout(450)
    const closed = await state()

    const problems = []
    if (before.onScreen) problems.push('the drawer is on screen before the toggle is pressed')
    if (!opened.onScreen) problems.push('the drawer did not slide into view when opened')
    if (opened.expanded !== 'true') problems.push(`aria-expanded was "${opened.expanded}" while open`)
    if (closed.onScreen) problems.push('the drawer stayed on screen after closing')
    report.record('journeys', 'phone', 'the navigation drawer opens and closes', problems)
  }
  await mobile.close()

  // ---- A list that failed to load must not claim the account is empty ----
  //
  // Every list view used to catch a failed request, log it to the console and
  // fall through to its empty state, so a server error and an empty account
  // rendered identically. On this screen that meant being told "No API keys
  // yet" -- as fact -- while the request had actually failed. A false negative
  // about outstanding credentials is the wrong way for this to break.
  await page.route('**/api/v1/keys*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: { code: 'INTERNAL', message: 'database is locked' } }),
    }))
  await page.goto(`${base}/web/api-keys`, { waitUntil: 'networkidle', timeout: 30000 })
  await page.waitForTimeout(700)

  const failed = await page.evaluate(() => {
    const text = document.body.innerText || ''
    return {
      claimsEmpty: /no api keys yet/i.test(text),
      saysFailed: !!document.querySelector('[role="alert"]'),
      showsReason: /database is locked/i.test(text),
    }
  })
  await page.unroute('**/api/v1/keys*')

  const loadProblems = []
  if (failed.claimsEmpty) loadProblems.push('said "No API keys yet" when the request had failed')
  if (!failed.saysFailed) loadProblems.push('no alert told the operator the list could not be loaded')
  if (!failed.showsReason) loadProblems.push("the server's reason was not shown")
  report.record('journeys', where, 'a failed list load says so instead of claiming empty', loadProblems)

  // ---- An unknown route renders the not-found view, not a blank page -----
  await page.goto(`${base}/web/definitely-not-a-route`, { waitUntil: 'networkidle', timeout: 30000 })
  await page.waitForTimeout(500)
  const nf = await page.evaluate(() => (document.body.innerText || '').trim().length)
  report.record('journeys', where, 'an unknown route renders something',
    nf > 10 ? [] : ['an unknown route rendered a blank page'])

  await page.close()
}
