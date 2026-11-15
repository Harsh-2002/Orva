import assert from 'node:assert/strict'
import { test } from 'node:test'
import { templates } from '../src/templates/index.js'
import { describeSnapshotDiff } from '../src/utils/rollbackDiff.js'

test('SDK snapshot diff treats absent as false, but reports grants and revocations', () => {
  assert.deepEqual(describeSnapshotDiff({}, { sdk: false }), [])
  assert.deepEqual(describeSnapshotDiff({ sdk: false }, {}), [])
  assert.deepEqual(describeSnapshotDiff({}, { sdk: true }), ['~ Orva SDK: false → true'])
  assert.deepEqual(describeSnapshotDiff({ sdk: true }, {}), ['~ Orva SDK: true → false'])
  assert.deepEqual(describeSnapshotDiff({ sdk: true }, { sdk: true }), [])
})

test('only SDK-dependent templates explicitly require SDK access', () => {
  const all = Object.values(templates).flat()
  assert.deepEqual(all.filter((t) => t.requiresSDK).map((t) => t.id).sort(), [
    'node-counter-cas', 'node-url-shortener', 'py-guestbook', 'py-traced-pipeline',
  ])
  for (const template of all) {
    const importsSDK = /from orva import|require\(['"]orva['"]\)/.test(template.code)
    assert.equal(template.requiresSDK === true, importsSDK, template.id)
  }
})
