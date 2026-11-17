async page => {
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()) })
  const results = []
  for (const [runtime, width, theme] of [['node', 390, 'night'], ['python', 1440, 'day']]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1050 })
    await page.evaluate((value) => { document.documentElement.dataset.theme = value }, theme)
    await page.getByRole('button', { name: `scratch-${runtime}`, exact: true }).click()
    await page.getByRole('heading', { name: 'Effective access', exact: true }).waitFor()
    const outbound = page.getByRole('region', { name: 'Effective platform access', exact: true })
    const inbound = page.getByRole('region', { name: 'Effective caller acceptance', exact: true })
    if (!(await outbound.innerText()).includes('No protected platform actions allowed.')) throw new Error('Last-source removal left an affirmative human grant')
    if (!(await inbound.innerText()).includes('No callers accepted.')) throw new Error('Last-source removal left caller acceptance')
    const reply = page.waitForResponse((r) => r.url().includes('/fn/') && r.request().method() === 'POST')
    await page.getByRole('button', { name: 'Invoke own KV', exact: true }).click()
    const value = await (await reply).json()
    if (value.decision !== 'IAM_DENIED') throw new Error('Last-source removal did not deny real private SDK')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
    if (overflow) throw new Error('Final source view overflow')
    await page.screenshot({ path: `/evidence/managed-${runtime}-${theme}-none.png`, fullPage: true })
    results.push({ runtime, decision: value.decision, overflow, effective_summary: await outbound.innerText(), caller_summary: await inbound.innerText() })
  }
  if (errors.length) throw new Error('Browser errors: ' + errors.join(';'))
  return { results, errors }
}
