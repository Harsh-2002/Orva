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
    if (!(await outbound.innerText()).includes('Can read the key “demo”')) throw new Error('Residual effective grant missing')
    if (!(await inbound.innerText()).includes('may start this function by a direct call')) throw new Error('Managed inbound acceptance missing from human summary')
    const inline = await page.locator('section[aria-labelledby="perm-inbound-title"]').innerText()
    if (!inline.includes('No inline caller grants') || inline.includes('No callers accepted.')) throw new Error('Empty inline callers misrepresented total acceptance')
    const sources = await page.getByRole('region', { name: 'Attached policies and effective access', exact: true }).innerText()
    if (!sources.includes('Source caller:') || !sources.includes('overlap-')) throw new Error('Residual caller source attribution missing')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
    if (overflow) throw new Error('Residual source view overflow')
    await page.screenshot({ path: `/evidence/managed-${runtime}-${theme}-residual-inbound.png`, fullPage: true })
    results.push({ runtime, overflow, effective_summary: await outbound.innerText(), caller_summary: await inbound.innerText(), inline_callers: inline, sources })
  }
  if (errors.length) throw new Error('Browser errors: ' + errors.join(';'))
  return { results, errors }
}
