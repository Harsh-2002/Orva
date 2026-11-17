async page => {
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()) })
  await page.goto('http://127.0.0.1:4300/managed-iam.html')
  await page.getByRole('heading', { name: 'Policies', exact: true }).waitFor()
  if (await page.getByRole('button', { name: 'Create reusable policy', exact: true }).count()) throw new Error('Read-only user can create a reusable policy')
  const results = []
  for (const [runtime, width, theme] of [['node', 390, 'night'], ['python', 1440, 'day']]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1050 })
    await page.evaluate((value) => { document.documentElement.dataset.theme = value }, theme)
    await page.getByRole('button', { name: `scratch-${runtime}`, exact: true }).click()
    await page.getByRole('heading', { name: 'Effective access', exact: true }).waitFor()
    await page.getByText('Read only. An administrator must change these sources.', { exact: true }).waitFor()
    for (const name of ['Create inline permissions', 'Edit inline permissions', 'Manage policy attachments', 'Review detach', 'Detach reviewed policy']) {
      if (await page.getByRole('button', { name, exact: true }).count()) throw new Error(`Read-only control exposed: ${name}`)
    }
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
    if (overflow) throw new Error('Read-only view overflow')
    await page.screenshot({ path: `/evidence/managed-${runtime}-${theme}-readonly.png`, fullPage: true })
    results.push({ runtime, overflow, edit_attach_destructive_controls: 0 })
  }
  if (errors.length) throw new Error('Browser errors: ' + errors.join(';'))
  return { results, errors }
}
