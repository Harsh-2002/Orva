import test from 'node:test'
import assert from 'node:assert/strict'
import { Chunk, diff, Change } from '@codemirror/merge'
import { Text } from '@codemirror/state'
import { makeLineDiff } from '../src/utils/lineDiff.js'
import { inlineRanges } from '../src/utils/diffMarks.js'

// Word marks belong only inside edited lines. The library marked every change,
// so wholly added lines got a second box over their text.

const conf = { override: makeLineDiff({ diff, Change }) }

function marks(a, b, side) {
  const A = Text.of(a.split('\n')), B = Text.of(b.split('\n'))
  const doc = side === 'a' ? a : b
  const D = side === 'a' ? A : B
  return inlineRanges(Chunk.build(A, B, conf), side, (from, to) => doc.slice(from, to), (pos) => D.lineAt(pos)).map(([f, t]) => doc.slice(f, t))
}

test('an edited line marks only the words that changed, on both sides', () => {
  const a = 'keep\nconst total = 10;\nkeep too', b = 'keep\nconst total = 25;\nkeep too'
  assert.deepEqual(marks(a, b, 'a'), ['10'])
  assert.deepEqual(marks(a, b, 'b'), ['25'])
})

test('wholly added lines get no word marks, only the line tint', () => {
  const a = 'one\nthree', b = 'one\ntwo\nthree'
  assert.deepEqual(marks(a, b, 'b'), [])
  assert.deepEqual(marks(a, b, 'a'), [])
})

test('wholly removed lines get no word marks', () => {
  assert.deepEqual(marks('one\ntwo\nthree', 'one\nthree', 'a'), [])
})

test('an indentation-only change is not word-marked', () => {
  assert.deepEqual(marks('  a\n  b', '\ta\n\tb', 'b'), [])
  assert.deepEqual(marks('  a\n  b', '\ta\n\tb', 'a'), [])
})

test('the unified view (no side) reads the new document', () => {
  const a = 'x = 1', b = 'x = 2'
  const A = Text.of(a.split('\n')), B = Text.of(b.split('\n'))
  const got = inlineRanges(Chunk.build(A, B, conf), null, (f, t) => b.slice(f, t), (pos) => B.lineAt(pos))
  assert.deepEqual(got.map(([f, t]) => b.slice(f, t)), ['2'])
})

test('a mark that would cover nearly the whole line is left to the line tint', () => {
  assert.deepEqual(marks('keep\nalpha beta gamma\nkeep', 'keep\nzzzz yyyy xxxxx\nkeep', 'b'), [])
})

test('word marks stay inside one line when several lines are edited', () => {
  const a = 'a = 1;\nb = 2;\nc = 3;', b = 'a = 9;\nb = 2;\nc = 8;'
  assert.deepEqual(marks(a, b, 'b'), ['9', '8'])
  assert.deepEqual(marks(a, b, 'a'), ['1', '3'])
})
