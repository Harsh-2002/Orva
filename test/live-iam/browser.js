async page => {
  const responses = []
  const errors = []
  page.on('pageerror', e => errors.push(e.message))
  page.on('response', async response => {
    const path = new URL(response.url()).pathname
    if ((path.startsWith('/api/v1/functions/') && path.endsWith('/permissions')) || path.startsWith('/api/scratch/invoke/')) {
      responses.push({ path, status: response.status(), body: await response.json() })
    }
  })
  await page.goto('http://127.0.0.1:4300/live-iam.html')
  await page.getByRole('heading', { name: 'Permissions', exact: true }).waitFor()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.evaluate(() => { document.documentElement.dataset.theme = 'night' })
  const journeys = []
  for (const runtime of ['Node.js', 'Python']) {
    await page.getByRole('radio', { name: runtime, exact: true }).click()
    await page.getByRole('button', { name: 'Create policy', exact: true }).waitFor()
    const invoke = async decision => {
      const reply = page.waitForResponse(r => r.url().includes('/api/scratch/invoke/') && r.request().method() === 'POST')
      await page.getByRole('button', { name: 'Invoke KV demo', exact: true }).click()
      const body = await (await reply).json()
      if (body.response.decision !== decision) throw new Error(`Actual SDK expected ${decision}`)
      await page.getByTestId('invocation-result').filter({ hasText: decision }).waitFor()
      return body
    }
    const before = await invoke('IAM_DENIED')
    await page.getByRole('button', { name: 'Create policy', exact: true }).click()
    await page.getByRole('radio', { name: /Storage/ }).check()
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    await page.getByRole('checkbox', { name: /^Read a key kv.get/ }).check()
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    await page.getByRole('radio', { name: 'Exact key', exact: true }).click()
    await page.getByLabel('Key', { exact: true }).fill('demo')
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    await page.getByRole('button', { name: 'Add to policy', exact: true }).click()
    await page.getByRole('button', { name: 'Review changes', exact: true }).click()
    if (runtime === 'Node.js') await page.screenshot({ path: '/evidence/phone-night-review.png', fullPage: true })
    const saved = page.waitForResponse(r => r.url().endsWith('/permissions') && r.request().method() === 'PUT')
    await page.getByRole('button', { name: 'Save permissions', exact: true }).click()
    const savedResponse = await saved
    if (savedResponse.status() !== 200) throw new Error('Actual save failed')
    const savedSnapshot = await savedResponse.json()
    if (savedSnapshot.host.currentArtifact !== before.artifact || savedSnapshot.host.approvedArtifact !== before.artifact
      || typeof savedSnapshot.host.revision !== 'string'
      || !savedSnapshot.history.some(e => e.revision === savedSnapshot.host.revision && e.actor.kind === 'api_key')
      || savedSnapshot.policy.allow.length !== 1) throw new Error('Save lacks actual pinned artifact/policy/audit')
    await page.getByRole('button', { name: 'Edit permissions', exact: true }).waitFor()
    const allowed = await invoke('ALLOWED')
    if (runtime === 'Node.js') {
      await page.evaluate(() => { document.documentElement.dataset.theme = 'day' })
      await page.setViewportSize({ width: 1440, height: 1050 })
      await page.screenshot({ path: '/evidence/desktop-day-allowed.png', fullPage: true })
    }
    await page.getByRole('button', { name: 'Edit permissions', exact: true }).click()
    await page.getByRole('button', { name: /^Remove / }).first().click()
    await page.getByRole('button', { name: 'Review changes', exact: true }).click()
    const revoked = page.waitForResponse(r => r.url().endsWith('/permissions') && r.request().method() === 'PUT')
    await page.getByRole('button', { name: 'Save permissions', exact: true }).click()
    const revokedResponse = await revoked
    if (revokedResponse.status() !== 200) throw new Error('Actual revoke failed')
    const revokedSnapshot = await revokedResponse.json()
    if (revokedSnapshot.policy.allow.length !== 0 || BigInt(revokedSnapshot.host.revision) <= BigInt(savedSnapshot.host.revision)) {
      throw new Error('Revocation was not persisted at a newer revision')
    }
    await page.getByRole('button', { name: 'Create policy', exact: true }).waitFor()
    const after = await invoke('IAM_DENIED')
    if (before.worker !== allowed.worker || before.worker !== after.worker || before.artifact !== after.artifact) {
      throw new Error('Workflow did not reuse the same pinned worker')
    }
    if (runtime === 'Node.js') {
      await page.evaluate(() => { document.documentElement.dataset.theme = 'night' })
      await page.setViewportSize({ width: 390, height: 844 })
      await page.screenshot({ path: '/evidence/phone-night-revoked.png', fullPage: true })
    }
    journeys.push({ runtime, before, allowed, after, savedSnapshot, revokedSnapshot })
  }
  if (errors.length) throw new Error('Browser page errors: ' + errors.join('; '))
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
  if (overflow) throw new Error('Horizontal overflow')
  return { journeys, responses, pageErrors: errors, overflow }
}
