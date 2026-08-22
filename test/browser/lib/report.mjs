// Result collection and reporting.
//
// A check is one assertion at one place. `where` is the location a human needs
// in order to reproduce it (viewport + route, or the flow step), and `detail`
// is the measurement that failed, never a restatement of the rule. "phone
// /functions: Delete button is 27x27" is actionable; "touch target too small"
// is not.

const ICON = { pass: '✓', fail: '✗', skip: '○' }

export class Report {
  constructor() {
    this.rows = []
    this.startedAt = Date.now()
  }

  pass(suite, where, name) {
    this.rows.push({ suite, where, name, status: 'pass' })
  }

  fail(suite, where, name, detail) {
    this.rows.push({ suite, where, name, status: 'fail', detail })
  }

  skip(suite, where, name, detail) {
    this.rows.push({ suite, where, name, status: 'skip', detail })
  }

  // record folds the common "assert or explain" shape into one call so a suite
  // reads as a list of rules rather than a list of if-statements.
  record(suite, where, name, failures) {
    if (!failures || failures.length === 0) return this.pass(suite, where, name)
    for (const f of failures) this.fail(suite, where, name, f)
  }

  counts() {
    const c = { pass: 0, fail: 0, skip: 0 }
    for (const r of this.rows) c[r.status]++
    return c
  }

  get failed() {
    return this.rows.some((r) => r.status === 'fail')
  }

  // Grouped by suite, then by check name, so a rule that fails in eight places
  // reads as one problem with eight instances rather than eight problems.
  print() {
    const bySuite = new Map()
    for (const r of this.rows) {
      if (!bySuite.has(r.suite)) bySuite.set(r.suite, [])
      bySuite.get(r.suite).push(r)
    }

    for (const [suite, rows] of bySuite) {
      const c = { pass: 0, fail: 0, skip: 0 }
      for (const r of rows) c[r.status]++
      const verdict = c.fail ? ICON.fail : ICON.pass
      console.log(`\n${verdict} ${suite}  (${c.pass} passed, ${c.fail} failed${c.skip ? `, ${c.skip} skipped` : ''})`)

      const byName = new Map()
      for (const r of rows.filter((x) => x.status !== 'pass')) {
        if (!byName.has(r.name)) byName.set(r.name, [])
        byName.get(r.name).push(r)
      }
      for (const [name, instances] of byName) {
        const bad = instances.filter((i) => i.status === 'fail')
        const icon = bad.length ? ICON.fail : ICON.skip
        console.log(`    ${icon} ${name}${bad.length > 1 ? `  (${bad.length} instances)` : ''}`)
        for (const i of instances) console.log(`        ${i.where}: ${i.detail}`)
      }
    }

    const c = this.counts()
    const secs = ((Date.now() - this.startedAt) / 1000).toFixed(1)
    console.log(`\n${'-'.repeat(64)}`)
    console.log(`${c.pass} passed, ${c.fail} failed, ${c.skip} skipped  (${secs}s)`)
    console.log(this.failed ? `\n${ICON.fail} FAIL` : `\n${ICON.pass} PASS`)
  }

  toJSON() {
    return JSON.stringify({ counts: this.counts(), rows: this.rows }, null, 1)
  }
}
