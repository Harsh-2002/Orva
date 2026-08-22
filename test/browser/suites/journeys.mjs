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
// Flows are destructive by nature, so they run against whatever instance they
// are pointed at. Point this at a scratch instance, not one you care about.

export const meta = {
  id: 'journeys',
  title: 'Interaction flows an operator actually performs',
}

const BASE_OF = (ctx) => ctx.base

// Opens the delete confirm on the first Functions row, if there is one.
async function openDeleteConfirm(page, base) {
  await page.goto(`${base}/web/functions`, { waitUntil: 'networkidle', timeout: 30000 })
  await page.waitForTimeout(600)
  const rows = await page.evaluate(() => document.querySelectorAll('tbody tr').length)
  if (!rows) return { rows: 0 }
  const del = page.locator('tbody tr').first()
    .locator('button[aria-label*="Delete" i], button[title*="Delete" i]').first()
  if (!(await del.count())) return { rows, noButton: true }
  await del.click()
  await page.waitForTimeout(500)
  return { rows, opened: await page.locator('[role="dialog"]').count() > 0 }
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

  // The detail-view check runs BEFORE the destructive block on purpose: that
  // block deletes a function, and deleting one cascades away its executions,
  // so the invocations table this check needs would be empty by the time it
  // ran and the check would skip itself out of existence every time.

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
    const opened = await openDeleteConfirm(page, base)
    if (!opened.rows) {
      report.skip('journeys', where, 'Enter on Cancel does not confirm a destructive dialog',
        'no functions on this instance to delete')
    } else if (!opened.opened) {
      report.fail('journeys', where, 'Enter on Cancel does not confirm a destructive dialog',
        'could not open the delete confirmation dialog')
    } else {
      const focused = await page.evaluate(() => (document.activeElement?.textContent || '').trim())
      const before = opened.rows
      await page.keyboard.press('Enter')
      await page.waitForTimeout(1200)
      const after = await countRows(page)
      report.record('journeys', where, 'Enter on Cancel does not confirm a destructive dialog',
        after < before
          ? [`focus was on "${focused}" and Enter deleted a function (${before} then ${after})`]
          : [])
      report.record('journeys', where, 'focus lands on the non-destructive control',
        /cancel/i.test(focused) ? [] : [`focus landed on "${focused}", not Cancel`])
    }

    // ---- Escape still cancels -------------------------------------------
    const esc = await openDeleteConfirm(page, base)
    if (esc.opened) {
      const before = esc.rows
      await page.keyboard.press('Escape')
      await page.waitForTimeout(900)
      const closed = await page.locator('[role="dialog"]').count() === 0
      const after = await countRows(page)
      report.record('journeys', where, 'Escape closes a confirm without acting',
        [...(closed ? [] : ['dialog stayed open after Escape']),
          ...(after === before ? [] : ['Escape deleted something'])])
    }

    // ---- The confirm button still works by keyboard ----------------------
    const ok = await openDeleteConfirm(page, base)
    if (ok.opened) {
      const before = ok.rows
      await page.keyboard.press('Tab')
      const focused = await page.evaluate(() => (document.activeElement?.textContent || '').trim())
      await page.keyboard.press('Enter')
      await page.waitForTimeout(1500)
      const after = await countRows(page)
      report.record('journeys', where, 'the confirm button is operable by keyboard',
        after === before - 1
          ? []
          : [`Tab reached "${focused}" and Enter did not confirm (${before} then ${after})`])
    }
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
