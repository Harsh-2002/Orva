import assert from 'node:assert/strict'
import test from 'node:test'
import vm from 'node:vm'
import { PROBE } from './suites/touch-targets.mjs'

function measure({ border = 1, height = 40, top = -2, pseudoHeight = 42, pointerEvents = 'auto' } = {}) {
  const el = {
    tagName: 'BUTTON', className: 'touch-expand-md', id: '',
    closest: () => null,
    getBoundingClientRect: () => ({ width: 80, height }),
  }
  const style = { display: 'inline-flex', visibility: 'visible', borderLeftWidth: `${border}px`, borderTopWidth: `${border}px` }
  const after = {
    content: '""', position: 'absolute', pointerEvents, transform: 'none',
    display: 'block', boxSizing: 'border-box', left: '0px', top: `${top}px`,
    width: `${80 - border * 2}px`, height: `${pseudoHeight}px`,
  }
  return vm.runInNewContext(`(${PROBE.toString()})(43.5)`, {
    matchMedia: () => ({ matches: true }),
    document: { querySelectorAll: () => [el] },
    getComputedStyle: (_, pseudo) => pseudo ? after : style,
  })
}

test('bordered old inset target is 42px, not the falsely inferred 44px', () => {
  const result = measure()
  assert.equal(result.small.length, 1)
  assert.equal(result.small[0].h, 42)
})

test('borderless old inset target really reaches 44px', () => {
  assert.equal(measure({ border: 0, pseudoHeight: 44 }).small.length, 0)
})

test('fixed centered target reaches 44px with or without borders across tiers', () => {
  for (const border of [0, 1, 2]) {
    for (const height of [32, 36, 40]) {
      assert.equal(measure({ border, height, top: (height - 2 * border) / 2 - 22, pseudoHeight: 44 }).small.length, 0)
    }
  }
})

test('pointer-events none cannot extend a target', () => {
  assert.equal(measure({ top: -3, pseudoHeight: 44, pointerEvents: 'none' }).small[0].h, 40)
})
