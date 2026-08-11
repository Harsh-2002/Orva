import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const styles = readFileSync(new URL('../src/style.css', import.meta.url), 'utf8')

test('compact touch targets do not create overlapping pseudo-element hit areas', () => {
  assert.doesNotMatch(styles, /\.touch-expand-(?:xs|sm|iconbtn)::before/)
})

test('coarse pointers receive real non-overlapping 44px targets', () => {
  assert.match(styles, /@media \(pointer: coarse\)/)
  assert.match(styles, /\.touch-expand-iconbtn[\s\S]*?min-height: 44px/)
  assert.match(styles, /\.touch-expand-iconbtn[\s\S]*?min-width: 44px/)
})

test('leaving overlays stop intercepting clicks immediately', () => {
  assert.match(styles, /\.fade-leave-active\s*\{[\s\S]*?pointer-events: none;/)
})
