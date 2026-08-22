import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// The KV inspect drawer is an editor: Save writes back whatever the textarea
// holds. It used to display a *normalised* rendering -- deepParse strips
// JSON-string wrapping, so a value double-encoded by the Python SDK showed as
// the dict it contained. That rendering does not round-trip. Opening the string
// "123", changing nothing and pressing Save stored the number 123; "true"
// became a boolean; '{"foo":1}' became an object. Reproduced in a browser
// against a real instance: str -> int with no edit at all.
//
// These tests pin the property that matters: whatever the drawer shows must
// parse back to the value it was given.

const SRC = readFileSync(new URL('../src/views/KVStore.vue', import.meta.url), 'utf8')

// The drawer must render with a faithful serialiser, never the normalising one.
test('the inspect drawer does not render through deepParse', () => {
  const openInspect = SRC.slice(SRC.indexOf('const openInspect'), SRC.indexOf('const saveInspect'))
  assert.match(openInspect, /inspect\.text = faithfulJSON\(/,
    'the editable textarea must be filled with a faithful rendering')
  assert.doesNotMatch(openInspect, /inspect\.text = prettyJSON\(/,
    'prettyJSON unwraps double-encoding, which Save then writes back as a different type')
})

// And the serialiser itself must be a pure round-trip for the shapes that broke.
const faithfulJSON = (v) => {
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}

test('every value the drawer shows parses back to itself', () => {
  const cases = [
    ['a string that looks like a number', '123'],
    ['a string that looks like a boolean', 'true'],
    ['a string that looks like null', 'null'],
    ['a string containing JSON (Python SDK double-encoding)', '{"foo":1}'],
    ['a string containing a JSON array', '[1,2,3]'],
    ['a negative-looking string', '-5'],
    ['a real number', 123],
    ['a real boolean', true],
    ['a real object', { foo: 1, nested: { a: [1, 2] } }],
    ['a real array', [1, 'two', null]],
    ['null itself', null],
    ['an empty string', ''],
  ]
  for (const [label, value] of cases) {
    const shown = faithfulJSON(value)
    assert.deepEqual(JSON.parse(shown), value,
      `${label}: the drawer showed ${shown} which does not parse back to the stored value`)
    assert.equal(typeof JSON.parse(shown), typeof value,
      `${label}: type changed from ${typeof value} to ${typeof JSON.parse(shown)}`)
  }
})
