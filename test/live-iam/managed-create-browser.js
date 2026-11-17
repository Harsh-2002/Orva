async page => {
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()) })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('http://127.0.0.1:4300/managed-iam.html')
  await page.evaluate(() => { document.documentElement.dataset.theme = 'night' })
  await page.getByRole('heading', { name: 'Policies', exact: true }).waitFor()
  await page.getByRole('button', { name: 'Create reusable policy', exact: true }).click()
  const name = `own-config-reader-${Date.now()}`
  await page.getByLabel('Policy name', { exact: true }).fill(name)
  await page.getByRole('button', { name: 'Add permission', exact: true }).click()
  await page.getByRole('radio', { name: /Storage/ }).check()
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('checkbox', { name: /^Read a key kv\.get/ }).check()
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByLabel('Key', { exact: true }).fill('demo')
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Add to policy', exact: true }).click()
  await page.getByRole('button', { name: 'Review policy', exact: true }).click()
  const saved = page.waitForResponse((r) => r.url().endsWith('/api/v1/iam/policies') && r.request().method() === 'POST')
  await page.getByRole('button', { name: 'Create reusable policy', exact: true }).last().click()
  const response = await saved
  if (response.status() !== 200) throw new Error('UI policy creation failed')
  const created = await response.json()
  await page.getByRole('heading', { name, exact: true }).waitFor()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
  await page.screenshot({ path: '/evidence/managed-phone-night-created.png', fullPage: true })
  if (errors.length || overflow) throw new Error('UI creation errors or overflow: ' + errors.join(';'))
  return { policy: created.policy, affected_owners: created.affected_owners, errors, overflow }
}
