import test from 'node:test'
import assert from 'node:assert/strict'
import { performance } from 'node:perf_hooks'
import { diff, Change, Chunk } from '@codemirror/merge'
import { Text } from '@codemirror/state'
import { makeLineDiff } from '../src/utils/lineDiff.js'

// The Compare versions view used @codemirror/merge's default scanLimit of 500,
// which gives up on any substantial edit and marks the whole range replaced.
// These pin the replacement: correct changes, identical lines left alone, and a
// bounded running time.

const lineDiff = makeLineDiff({ diff, Change })

// Applying every change to `a` must reproduce `b`; anything else is a wrong diff.
function apply(a, b, changes) {
  let out = '', at = 0
  for (const c of changes) {
    assert.ok(c.fromA >= at, 'changes must be sorted and must not overlap')
    out += a.slice(at, c.fromA) + b.slice(c.fromB, c.toB)
    at = c.toA
  }
  return out + a.slice(at)
}

const lines = (n, f = (i) => `line ${i} const v${i} = ${i};`) => Array.from({ length: n }, (_, i) => f(i)).join('\n')

test('identical documents produce no changes', () => {
  assert.deepEqual(lineDiff('a\nb\n', 'a\nb\n'), [])
})

test('applying the changes reproduces the new document', () => {
  const cases = [
    ['', 'x\ny\n'],
    ['x\ny\n', ''],
    ['a\nb\nc', 'a\nb\nc\n'], // gains a trailing newline
    ['one\ntwo\nthree\n', 'one\n2\nthree\nfour\n'],
    ['  a\n  b\n  c\n', '\ta\n\tb\n\tc\n'],
    [lines(200), lines(200).replace(/v(\d*)7\b/g, 'w$1')],
    ['héllo wörld ✓ 日本語 🚀\nx', 'héllo wörld ✓ 日本語 🚀 !\nx\n'],
  ]
  for (const [a, b] of cases) assert.equal(apply(a, b, lineDiff(a, b)), b, JSON.stringify([a.slice(0, 20), b.slice(0, 20)]))
})

test('lines that are identical in both versions are not marked changed', () => {
  const a = 'keep one\nold a\nold b\nkeep two\nold c\nkeep three\n'
  const b = 'keep one\nnew a\nnew b\nkeep two\nnew c\nkeep three\n'
  const marked = (text, side) => lineDiff(a, b).flatMap((c) => {
    const [from, to] = side === 'a' ? [c.fromA, c.toA] : [c.fromB, c.toB]
    return text.slice(from, to).split('\n')
  })
  for (const l of [...marked(a, 'a'), ...marked(b, 'b')]) assert.ok(!l.startsWith('keep'), `marked an unchanged line: ${l}`)
})

test('a larger edit stays precise where the 500-unit default collapses it to one chunk', () => {
  const a = lines(300)
  const b = a.split('\n').map((l, i) => (i % 3 === 0 ? l.replace('const', 'let') : l)).join('\n').replace('line 150', '\t\tline 150 ' + 'lorem ipsum '.repeat(40))
  const A = Text.of(a.split('\n')), B = Text.of(b.split('\n'))
  const old = Chunk.build(A, B, { scanLimit: 500 })
  const now = Chunk.build(A, B, { override: lineDiff })
  assert.equal(old.length, 1, 'premise: the old default collapses this edit to a single chunk')
  assert.ok(now.length > 50, `expected many precise chunks, got ${now.length}`)
  assert.equal(apply(a, b, lineDiff(a, b)), b)
})

test('a 3,000-line file with a third of its lines edited finishes quickly', () => {
  const a = lines(3000)
  const b = a.split('\n').flatMap((l, i) => (i % 5 === 0 ? [] : i % 3 === 0 ? [l.replace('const', 'let')] : i % 11 === 0 ? [l, `added ${i}`] : [l])).join('\n')
  const t = performance.now()
  const changes = lineDiff(a, b)
  const ms = performance.now() - t
  assert.equal(apply(a, b, changes), b)
  assert.ok(ms < 1500, `took ${ms.toFixed(0)} ms; the unbounded character diff took ~15 s here`)
})

test('more distinct lines than the encoder allows falls back to the bounded character diff', () => {
  const a = lines(60000), b = lines(60000, (i) => `line ${i} const w${i} = ${i};`)
  assert.equal(apply(a, b, lineDiff(a, b)), b)
})
