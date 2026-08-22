import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { runtimeLabel, isNode, isPython } from '../src/utils/runtime.js'

// The same function used to read "Node.js 24" on one screen and "node" on the
// next: RuntimeTag and the editor strip each spelled the labels out, and four
// more places printed the raw API value. The words now come from one module.

test('the two shipped runtimes get their pinned versions', () => {
  assert.equal(runtimeLabel('node'), 'Node.js 24')
  assert.equal(runtimeLabel('python'), 'Python 3.14')
})

test('TypeScript and JavaScript are the node runtime', () => {
  for (const rt of ['typescript', 'javascript', 'node', 'nodejs', 'Node']) {
    assert.ok(isNode(rt), `${rt} should resolve to node`)
    assert.equal(runtimeLabel(rt), 'Node.js 24')
  }
  for (const rt of ['python', 'Python', 'python3']) {
    assert.ok(isPython(rt), `${rt} should resolve to python`)
  }
})

test('an unknown runtime falls back to what the API said', () => {
  assert.equal(runtimeLabel('rust'), 'rust')
  assert.equal(runtimeLabel(''), '')
  assert.equal(runtimeLabel(undefined), '')
})

// Guard the consistency itself: no view may print the raw `runtime` field.
// Anything user-visible goes through RuntimeTag (which draws the mark) or
// runtimeLabel (for <option>, which cannot host a component).
const SRC = new URL('../src/', import.meta.url).pathname

const walk = (dir) => readdirSync(dir).flatMap((e) => {
  const p = join(dir, e)
  return statSync(p).isDirectory() ? walk(p) : [p]
})

test('no view interpolates the raw runtime value', () => {
  const offenders = []
  for (const file of walk(SRC).filter((f) => f.endsWith('.vue'))) {
    const src = readFileSync(file, 'utf8')
    // {{ fn.runtime }} / {{ f.runtime }} / {{ form.runtime }} — but not
    // {{ runtimeLabel(fn.runtime) }}, which is the sanctioned path.
    const raw = src.match(/\{\{\s*[A-Za-z_$][\w$.]*\.runtime\s*\}\}/g)
    if (raw) offenders.push(`${file.replace(SRC, '')}: ${raw.join(', ')}`)
  }
  assert.deepEqual(offenders, [],
    'these print the raw runtime ("node") instead of RuntimeTag or runtimeLabel ("Node.js 24")')
})
