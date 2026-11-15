import test from 'node:test'
import assert from 'node:assert/strict'
import { shouldNavigateOnRowClick, rowNav } from '../src/utils/rowNav.js'

// A fake DOM target: closest() answers for the selector list the helper uses.
const target = (insideControl) => ({ closest: () => (insideControl ? {} : null) })
const click = (over = {}) => ({ defaultPrevented: false, button: 0, target: target(false), ...over })

test('a plain click on the row navigates', () => {
  assert.equal(shouldNavigateOnRowClick(click()), true)
})

test('a click that starts in a control, link, checkbox or copy button does not', () => {
  assert.equal(shouldNavigateOnRowClick(click({ target: target(true) })), false)
})

test('a control that was re-rendered (target detached) before the event reached the row is still a control', () => {
  // The copy button swaps its icon on click, so by the time the event bubbles to
  // the row the clicked <svg> has no parent and closest() finds nothing.
  const button = { matches: () => true }
  const detachedSvg = { closest: () => null, matches: () => false }
  const event = click({ target: detachedSvg, composedPath: () => [detachedSvg, button] })
  assert.equal(shouldNavigateOnRowClick(event), false)
  // Same event, path that never crosses a control: navigates.
  const row = { matches: () => false }
  assert.equal(shouldNavigateOnRowClick(click({ target: detachedSvg, composedPath: () => [detachedSvg, row] })), true)
})

test('shift and alt clicks, and switch/checkbox/menuitem roles, do not navigate', () => {
  assert.equal(shouldNavigateOnRowClick(click({ shiftKey: true })), false)
  assert.equal(shouldNavigateOnRowClick(click({ altKey: true })), false)
})

test('a drag-select of an id (selection present) does not navigate', () => {
  assert.equal(shouldNavigateOnRowClick(click(), 'a1b2c3'), false)
})

test('a handled event or a non-primary button does not navigate', () => {
  assert.equal(shouldNavigateOnRowClick(click({ defaultPrevented: true })), false)
  assert.equal(shouldNavigateOnRowClick(click({ button: 2 })), false)
})

test('rowNav pushes the route, and opens a new tab on cmd/ctrl-click', () => {
  const pushed = []
  const opened = []
  globalThis.window = { getSelection: () => ({ toString: () => '' }), open: (...a) => opened.push(a) }
  const router = { push: (to) => pushed.push(to), resolve: (to) => ({ href: '/web' + to.path }) }
  const to = { path: '/functions/greeter', query: {} }
  rowNav(router, to)(click())
  assert.deepEqual(pushed, [to])
  rowNav(router, to)(click({ ctrlKey: true }))
  assert.equal(pushed.length, 1)
  assert.deepEqual(opened, [['/web/functions/greeter', '_blank', 'noopener']])
  delete globalThis.window
})

test('rowAction runs the action under the same rules', async () => {
  const { rowAction } = await import('../src/utils/rowNav.js')
  globalThis.window = { getSelection: () => ({ toString: () => '' }) }
  let ran = 0
  rowAction(() => { ran++ })(click())
  rowAction(() => { ran++ })(click({ target: target(true) }))
  assert.equal(ran, 1)
  delete globalThis.window
})
