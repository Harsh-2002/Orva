async page => {
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()) })
  const results = []
  for (const [runtime, width, theme] of [['node', 390, 'night'], ['python', 1440, 'day']]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1050 })
    await page.evaluate((value) => { document.documentElement.dataset.theme = value }, theme)
    await page.getByRole('button', { name: `scratch-${runtime}`, exact: true }).click()
    await page.getByRole('heading', { name: 'Permissions', exact: true }).waitFor()
    await page.getByRole('button', { name: 'Review detach', exact: true }).first().waitFor()
    // Managed access is nonempty, yet the independent inline editor is empty.
    await page.getByRole('button', { name: 'Create inline permissions', exact: true }).waitFor()
    const effective = page.getByRole('region', { name: 'Effective platform access', exact: true })
    if (!(await effective.innerText()).includes('Can read the key “demo”')) throw new Error('Effective human summary omitted actual managed access')
    const inline = page.locator('section[aria-labelledby="perm-outbound-title"]')
    const inlineText = await inline.innerText()
    if (!inlineText.includes('No inline platform grants') || inlineText.includes('No protected platform actions allowed')) throw new Error('Inline summary falsely described total access')
    const inlineCallers = await page.locator('section[aria-labelledby="perm-inbound-title"]').innerText()
    if (!inlineCallers.includes('No inline caller grants') || inlineCallers.includes('No callers accepted.')) throw new Error('Inline caller summary falsely described total acceptance')
    if (!(await page.getByRole('region', { name: 'Attached policies and effective access', exact: true }).innerText()).includes('Source allowance:')) throw new Error('Missing source attribution')
    const reply = page.waitForResponse((r) => r.url().includes('/fn/') && r.request().method() === 'POST')
    await page.getByRole('button', { name: 'Invoke own KV', exact: true }).click()
    const response = await reply
    const value = await response.json()
    if (value.decision !== 'ALLOWED' || value.value !== `scratch-value-${runtime}`) throw new Error('Actual browser SDK invocation did not read own namespace')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
    if (overflow) throw new Error('Function source view overflow')
    await page.screenshot({ path: `/evidence/managed-${runtime}-${theme}-sources.png`, fullPage: true })
    results.push({ runtime, decision: value.decision, overflow, independent_inline_editor: true, effective_summary: await effective.innerText(), inline_summary: inlineText, inline_callers: inlineCallers })
  }
  await page.getByRole('button', { name: 'Policies', exact: true }).click()
  await page.getByRole('button', { name: 'Create new version', exact: true }).waitFor()
  if (errors.length) throw new Error('Browser errors: ' + errors.join(';'))
  return { results, errors }
}
