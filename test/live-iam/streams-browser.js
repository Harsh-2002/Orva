async page => {
  const errors = []
  const responses = []
  page.on('pageerror', e => errors.push(e.message))
  page.on('response', async r => {
    if (!r.url().endsWith('/permissions')) return
    try { responses.push({ method: r.request().method(), status: r.status(), body: await r.json() }) } catch { /* page closed */ }
  })
  const metadata = page.waitForResponse(r => r.url().endsWith('/api/scratch/functions'))
  await page.goto('http://127.0.0.1:4300/live-iam.html')
  const functions = await (await metadata).json()
  await page.getByRole('heading', { name: 'Permissions', exact: true }).waitFor()
  const fn = runtime => functions.find(f => f.runtime === runtime)
  const label = runtime => runtime === 'node' ? 'Node.js' : 'Python'
  const theme = async (name, width) => {
    await page.setViewportSize({ width, height: 1050 })
    await page.emulateMedia({ colorScheme: name === 'day' ? 'light' : 'dark' })
    await page.evaluate(t => { document.documentElement.dataset.theme = t }, name)
  }
  const shot = (name) => page.screenshot({ path: `/evidence/${name}.png`, fullPage: true, animations: 'disabled' })
  const owner = async runtime => {
    const control = page.getByRole('radio', { name: label(runtime), exact: true })
    if (!(await control.getAttribute('aria-checked') === 'true')) {
      const reply = page.waitForResponse(r => r.url().endsWith(`/functions/${fn(runtime).id}/permissions`) && r.request().method() === 'GET')
      await control.click()
      const snapshot = await (await reply).json()
      if (snapshot.targets.length !== 2 || snapshot.targets.some(t => t.state !== 'resolved' || !functions.some(f => f.id === t.id && f.name === t.label))) {
        throw Error('Picker not derived from actual visible active metadata')
      }
    }
    await page.getByText(`What ${fn(runtime).name}`, { exact: false }).waitFor()
  }
  const edit = async runtime => {
    await owner(runtime)
    await page.getByRole('button', { name: /^(Create policy|Edit permissions)$/ }).click()
  }
  const save = async () => {
    await page.getByRole('button', { name: 'Review changes', exact: true }).click()
    const reply = page.waitForResponse(r => r.url().endsWith('/permissions') && r.request().method() === 'PUT')
    await page.getByRole('button', { name: 'Save permissions', exact: true }).click()
    const r = await reply, s = await r.json()
    if (r.status() !== 200 || typeof s.host.revision !== 'string' || !s.history.some(a => a.revision === s.host.revision && a.actor.kind === 'api_key')) throw Error('Save missing real audit revision')
    await page.getByRole('button', { name: /^(Create policy|Edit permissions)$/ }).waitFor()
    return s
  }
  const outbound = async (caller, callee, slug) => {
    await edit(caller)
    const add = page.getByRole('button', { name: 'Add permission', exact: true })
    if (await add.isVisible()) await add.click()
    await page.getByRole('radio', { name: /^Functions / }).check()
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    await page.getByRole('checkbox', { name: /^Call functions.invoke/ }).check()
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    await page.locator('#wizard-function').click()
    await page.getByRole('menuitemradio', { name: new RegExp(`^${fn(callee).name} ·`) }).click()
    await page.getByRole('menuitemradio').first().waitFor({ state: 'hidden' })
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    if (slug) await shot(`${slug}-outbound-review-night`)
    await page.getByRole('button', { name: 'Add to policy', exact: true }).click()
    return save()
  }
  const inbound = async (callee, caller, slug) => {
    await edit(callee)
    await page.getByRole('radio', { name: 'Who can call it', exact: true }).click()
    await page.getByRole('button', { name: 'Add caller', exact: true }).click()
    await page.locator('#wizard-caller').click()
    await page.getByRole('menuitemradio', { name: new RegExp(`^${fn(caller).name} ·`) }).click()
    await page.getByRole('menuitemradio').first().waitFor({ state: 'hidden' })
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    await page.getByRole('checkbox', { name: /^Function call f2f/ }).check()
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    if (slug) await shot(`${slug}-inbound-review-night`)
    await page.getByRole('button', { name: 'Add to policy', exact: true }).click()
    return save()
  }
  const remove = async (runtime, part) => {
    await edit(runtime)
    await page.getByRole('radio', { name: part === 'inbound' ? 'Who can call it' : 'What it can do', exact: true }).click()
    await page.getByRole('button', { name: /^Remove / }).first().click()
    return save()
  }
  const ui = async () => ({
    phase: await page.getByTestId('stream-phase').innerText(),
    count: await page.getByTestId('stream-count').innerText(),
    note: (await page.getByTestId('stream-note').count()) ? await page.getByTestId('stream-note').innerText() : '',
    calleeShown: (await page.getByTestId('stream-callee').count()) > 0,
  })
  const waitPhase = (phase, timeout = 25000) => page.locator('[data-testid="stream-phase"]', { hasText: phase }).waitFor({ timeout })
  const waitChunks = n => page.waitForFunction(m => parseInt((document.querySelector('[data-testid="stream-count"]') || {}).textContent || '0', 10) >= m, n, { timeout: 25000 })
  const start = async (caller, callee, length) => {
    await owner(caller)
    await page.getByRole('radio', { name: 'Stream', exact: true }).click()
    await page.locator('#scratch-call-target').click()
    await page.getByRole('menuitemradio', { name: fn(callee).name, exact: true }).click()
    await page.getByRole('menuitemradio').first().waitFor({ state: 'hidden' })
    await page.getByRole('radio', { name: length === 'long' ? 'Long, about 12 s' : 'Short', exact: true }).click()
    const reply = page.waitForResponse(r => r.url().includes(`/fn/${fn(caller).id}/stream`))
    await page.getByRole('button', { name: 'Start stream', exact: true }).click()
    const r = await reply
    const h = r.headers()
    return { status: r.status(), execution: h['x-orva-execution-id'], worker: h['x-scratch-worker'], artifact: h['x-scratch-artifact'] }
  }
  const clear = async () => { await page.getByRole('button', { name: 'Clear', exact: true }).click(); await page.getByTestId('stream-panel').waitFor({ state: 'detached' }) }
  const runShort = async (caller, callee, phase) => {
    const head = await start(caller, callee, 'short')
    await waitPhase(phase)
    const final = await ui()
    return { head, final }
  }
  const cut = async (caller, callee, who, slug) => {
    const head = await start(caller, callee, 'long')
    await waitChunks(3)
    const before = await ui()
    if (before.phase !== 'Streaming') throw Error('Stream was not in flight before revocation')
    const saved = who === 'caller' ? await remove(caller, 'outbound') : await remove(callee, 'inbound')
    const atAck = await ui()
    await waitPhase('Denied')
    const final = await ui()
    await theme('night', 390)
    await shot(`${slug}-${who}-revoked-night`)
    return { head, before, saved, atAck, final }
  }

  const journeys = []
  for (const [caller, callee] of [['node', 'python'], ['python', 'node']]) {
    const slug = `${caller}-to-${callee}-stream`
    await theme('night', 390)
    const outboundOnlySave = await outbound(caller, callee, slug)
    const outboundOnly = await runShort(caller, callee, 'Denied')
    await clear()
    const outboundRemovedSave = await remove(caller, 'outbound')
    const inboundOnlySave = await inbound(callee, caller, slug)
    const inboundOnly = await runShort(caller, callee, 'Denied')
    await clear()
    const bothSave = await outbound(caller, callee)
    const both = await runShort(caller, callee, 'Completed')
    await theme('day', 1440)
    await shot(`${slug}-allowed-day`)
    await clear()
    await theme('night', 390)
    // Stop is operated from the keyboard; the client disconnect must cancel the callee.
    const stopHead = await start(caller, callee, 'long')
    await waitChunks(2)
    const atStop = await ui()
    const stop = page.getByRole('button', { name: 'Stop', exact: true })
    await stop.focus()
    if (!(await stop.evaluate(el => el === document.activeElement))) throw Error('Stop is not keyboard focusable')
    await page.keyboard.press('Enter')
    await waitPhase('Stopped')
    const stopped = { head: stopHead, atStop, final: await ui() }
    await shot(`${slug}-stopped-night`)
    await clear()
    const callerCut = await cut(caller, callee, 'caller', slug)
    await clear()
    const regrantSave = await outbound(caller, callee)
    const restored = await runShort(caller, callee, 'Completed')
    await clear()
    const calleeCut = await cut(caller, callee, 'callee', slug)
    await clear()
    await remove(caller, 'outbound')
    journeys.push({
      caller: fn(caller), callee: fn(callee),
      outboundOnlySave, outboundOnly, outboundRemovedSave, inboundOnlySave, inboundOnly,
      bothSave, both, stopped, callerCut, regrantSave, restored, calleeCut,
    })
  }
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
  if (errors.length || overflow) throw Error('Browser errors or overflow')
  return { journeys, responses, pageErrors: errors, overflow, streamEnabled: !(await page.getByRole('radio', { name: 'Stream', exact: true }).isDisabled()) }
}
