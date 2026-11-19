async page => {
  // Render-only replay, not a backend quarantine/review transition or invocation proof.
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()) })
  const results = []
  for (const [runtime, width, theme, flag] of [
    ['node', 390, 'night', 'quarantined'],
    ['python', 1440, 'day', 'review_required'],
  ]) {
    await page.goto('http://127.0.0.1:4300/managed-iam.html')
    await page.getByRole('heading', { name: 'Policies', exact: true }).waitFor()
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1050 })
    await page.evaluate(async ({ runtime, theme, flag }) => {
      document.documentElement.dataset.theme = theme
      const functions = await (await fetch('/api/scratch/functions')).json()
      const fn = functions.find((f) => f.runtime === runtime)
      const source = await (await fetch(`/api/v1/functions/${fn.id}/permissions`)).json()
      source.host = { ...source.host, [flag]: true }
      const vueURL = performance.getEntriesByType('resource').find((r) => r.name.includes('/node_modules/.vite/deps/vue.js?'))?.name
      if (!vueURL) throw new Error('Loaded Vue module not found')
      const [{ createApp, h }, { default: EffectiveAccess }, { default: FunctionPermissions }, { inlineSnapshot }] = await Promise.all([
        import(vueURL),
        import('/src/components/permissions/EffectiveAccess.vue'),
        import('/src/views/FunctionPermissions.vue'),
        import('/src/api/managedPolicies.js'),
      ])
      const root = document.getElementById('app')
      root.__vue_app__.unmount()
      createApp({
        render: () => h('main', { class: 'mx-auto max-w-5xl space-y-6 p-4 sm:p-8' }, [
          h('p', { role: 'status', class: 'text-sm text-warning-fg' },
            `UI rendering replay only: live ${runtime} source DTO with ${flag}=true. Not a live host transition or invocation proof.`),
          h(EffectiveAccess, { source }),
          h(FunctionPermissions, { functionId: fn.id, snapshot: inlineSnapshot(source), inlineOnly: true }),
        ]),
      }).mount(root)
    }, { runtime, theme, flag })
    await page.getByRole('heading', { name: 'Stored effective grants, not active', exact: true }).waitFor()
    const effective = await page.getByRole('region', { name: 'Effective platform access', exact: true }).innerText()
    const inbound = await page.getByRole('region', { name: 'Effective caller acceptance', exact: true }).innerText()
    if (effective.includes('Can read') || !effective.includes('No protected platform actions allowed.')) throw new Error('Retained replay falsely claimed active outbound access')
    if (inbound.includes('may start') || !inbound.includes('No callers accepted.')) throw new Error('Retained replay falsely claimed active inbound access')
    const stored = await page.getByRole('region', { name: 'Stored effective grants, not active', exact: true }).innerText()
    if (!stored.includes('Stored allowance, not active') || !stored.includes('Stored caller, not active')) throw new Error('Retained source inspection missing')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
    if (overflow) throw new Error('Retained replay overflow')
    await page.screenshot({ path: `/evidence/managed-${runtime}-${theme}-${flag}-replay.png`, fullPage: true })
    results.push({ runtime, flag, overflow, evidence_kind: 'render-only replay, not backend transition', effective_summary: effective, caller_summary: inbound, stored_summary: stored })
  }
  await page.goto('http://127.0.0.1:4300/managed-iam.html')
  await page.getByRole('heading', { name: 'Policies', exact: true }).waitFor()
  if (errors.length) throw new Error('Browser errors: ' + errors.join(';'))
  return { results, errors }
}
