async page => {
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()) })
  await page.setViewportSize({ width: 1440, height: 1050 })
  await page.evaluate(() => { document.documentElement.dataset.theme = 'day' })
  await page.getByRole('button', { name: 'Create new version', exact: true }).click()
  await page.getByRole('button', { name: 'Remove permission 1', exact: true }).click()
  await page.getByRole('button', { name: 'Review policy', exact: true }).click()
  const saved = page.waitForResponse((r) => r.url().includes('/api/v1/iam/policies/') && r.url().endsWith('/update'))
  await page.getByRole('button', { name: 'Save new version', exact: true }).click()
  const response = await saved
  if (response.status() !== 200) throw new Error('UI immutable version creation failed')
  const result = await response.json()
  await page.getByRole('button', { name: 'Create new version', exact: true }).waitFor()
  await page.getByText('Version 2 · Available to attach', { exact: true }).waitFor()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
  await page.screenshot({ path: '/evidence/managed-desktop-day-version2.png', fullPage: true })
  if (errors.length || overflow) throw new Error('UI version errors or overflow: ' + errors.join(';'))
  return { policy: result.policy, errors, overflow }
}
