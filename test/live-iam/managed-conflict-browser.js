async page => {
  const base = 'http://127.0.0.1:4300'
  const pageErrors = [], consoleErrors = [], statuses = []
  page.on('pageerror', (e) => pageErrors.push(e.message))
  page.on('console', (m) => { if (m.type() === 'error') consoleErrors.push(m.text()) })
  page.on('response', (r) => {
    if (r.status() >= 400) statuses.push({ url: r.url(), method: r.request().method(), status: r.status() })
  })
  const call = async (method, path, data) => {
    const r = await page.request.fetch(base + path, { method, data })
    if (r.status() !== 200) throw Error(`${method} ${path}: ${r.status()}`)
    return r.json()
  }
  const functions = await call('GET', '/api/scratch/functions')
  const source = (id) => call('GET', `/api/v1/functions/${id}/permissions`)
  const owners = (s) => [{ function_id: s.function.id, expected: { revision: s.host.revision, current_artifact: s.host.current_artifact, approved_artifact: s.host.approved_artifact } }]
  const reader = { schema_version: 1, allow: [{ actions: ['kv.get'], resource: { own_kv: { key: 'demo' } }, context: 'execution' }], inbound: [] }
  const results = []
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Network.enable')
  for (const [runtime, width, theme] of [['node', 390, 'night'], ['python', 1440, 'day']]) {
    const fn = functions.find((f) => f.runtime === runtime)
    let p = (await call('POST', '/api/v1/iam/policies', { name: `conflict-${runtime}-${Date.now()}`, policy: reader })).policy
    p = (await call('POST', `/api/v1/iam/policies/${p.id}/attach`, { membership_revision: p.membership_revision, version: '1', owners: owners(await source(fn.id)) })).policy
    await page.goto(base + '/managed-iam.html')
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1050 })
    await page.evaluate((value) => { document.documentElement.dataset.theme = value }, theme)
    await page.getByRole('button', { name: `scratch-${runtime}`, exact: true }).click()
    const effective = page.getByRole('region', { name: 'Effective access', exact: true })
    await effective.getByText('Can read the key “demo” in its own storage.', { exact: true }).waitFor()
    const before = await call('POST', `/fn/${fn.id}/demo`, {})
    if (before.decision !== 'ALLOWED') throw Error('Initial real SDK grant missing')
    await page.getByRole('button', { name: 'Create inline permissions', exact: true }).click()
    await page.getByRole('radio', { name: /Storage/ }).check()
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    await page.getByRole('checkbox', { name: /^Read a key kv\.get/ }).check()
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    await page.getByLabel('Key', { exact: true }).fill('other')
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    await page.getByRole('button', { name: 'Add to policy', exact: true }).click()
    await page.getByRole('button', { name: 'Review changes', exact: true }).click()
    const loaded = await source(fn.id)
    await call('POST', `/api/v1/iam/policies/${p.id}/revoke`, {
      definition_revision: p.definition_revision, membership_revision: p.membership_revision,
      owners: owners(loaded), confirm: true,
    })
    const denied = await call('POST', `/fn/${fn.id}/demo`, {})
    if (denied.decision !== 'IAM_DENIED') throw Error('Competing real service revoke did not deny SDK')
    await page.evaluate(() => {
      const target = document.querySelector('[aria-label="Effective access"]')
      window.__iamFreshnessProbe = []
      const collect = () => {
        const text = target.innerText
        const entry = { stale: text.includes('Stale snapshot'), old_grant_active: text.includes('Can read the key “demo”') && text.includes('What this function may do now'), none: text.includes('No protected platform actions allowed.') }
        const last = window.__iamFreshnessProbe.at(-1)
        if (JSON.stringify(entry) !== JSON.stringify(last)) window.__iamFreshnessProbe.push(entry)
      }
      window.__iamFreshnessObserver = new MutationObserver(collect)
      window.__iamFreshnessObserver.observe(target, { subtree: true, childList: true, characterData: true })
    })
    // Real Chromium latency, not a route mock: gives the stale in-flight render a capture window.
    await cdp.send('Network.emulateNetworkConditions', { offline: false, latency: 700, downloadThroughput: -1, uploadThroughput: -1 })
    const reply = page.waitForResponse((r) => r.request().method() === 'PUT' && r.url().endsWith('/permissions'))
    const save = page.getByRole('button', { name: 'Save permissions', exact: true })
    await save.click()
    await effective.getByText(/Stale snapshot\. Current access is not confirmed/).waitFor()
    if ((await effective.innerText()).includes('What this function may do now')) throw Error('Stale render still claims current authority')
    await page.screenshot({ path: `/evidence/conflict-${runtime}-${theme}-stale.png`, fullPage: true })
    const response = await reply
    if (response.status() !== 409) throw Error(`Expected genuine stale Save 409, got ${response.status()}`)
    await page.getByRole('alert').filter({ hasText: /changed|conflict/i }).first().waitFor()
    await effective.getByText('No protected platform actions allowed.', { exact: true }).waitFor()
    const states = await page.evaluate(() => { window.__iamFreshnessObserver.disconnect(); return window.__iamFreshnessProbe })
    if (!states.some((s) => s.stale) || states.some((s) => s.old_grant_active)) throw Error('An unrefreshed render restored the revoked grant')
    if (!(await save.isDisabled())) throw Error('Stale draft Save was re-enabled by inspector refresh')
    const text = await page.locator('main').innerText()
    if (!text.includes('other') || !text.includes('Snapshot stale')) throw Error('Unsaved inline draft/baseline was lost or promoted')
    const final = await source(fn.id)
    if (final.inline_policy.allow.length || final.effective_policy.allow.length) throw Error('Draft leaked into persisted/effective policy')
    const after = await call('POST', `/fn/${fn.id}/demo`, {})
    if (after.decision !== 'IAM_DENIED') throw Error('Independent refresh changed real SDK denial')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
    if (overflow) throw Error('Conflict view overflow')
    await page.screenshot({ path: `/evidence/conflict-${runtime}-${theme}-fresh-none-draft-retained.png`, fullPage: true })
    results.push({ runtime, width, theme, before: before.decision, after: after.decision, stale_save_status: response.status(),
      save_disabled: true, draft_other_retained: true, states, loaded_revision: loaded.host.revision,
      current_revision: final.host.revision, effective_text: await effective.innerText(), overflow })
    await cdp.send('Network.emulateNetworkConditions', { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 })
  }
  await cdp.detach()
  const expectedConsole = consoleErrors.filter((e) => /\b409\b/.test(e))
  const unexpectedConsole = consoleErrors.filter((e) => !/\b409\b/.test(e))
  if (pageErrors.length || unexpectedConsole.length || statuses.length !== 2
    || statuses.some((s) => s.status !== 409 || s.method !== 'PUT') || expectedConsole.length !== 2) {
    throw Error('Unexpected browser errors or untracked failed requests: ' + JSON.stringify({ pageErrors, consoleErrors, statuses }))
  }
  return { results, pageErrors, expected_409_console_errors: expectedConsole, failed_requests: statuses, unexpected_console_errors: unexpectedConsole }
}
