import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const styles = readFileSync(new URL('../src/style.css', import.meta.url), 'utf8')

// The coarse-pointer block, isolated. Reading the whole file would let a rule
// from the fine-pointer half satisfy an assertion about the touch half.
function coarseBlock() {
  const at = styles.indexOf('Touch-target hit-area expansion')
  assert.ok(at > -1, 'the touch-target section is gone; these assertions are aimed at nothing')
  const from = styles.indexOf('@media (pointer: coarse)', at)
  assert.ok(from > -1, 'no coarse-pointer block follows the touch-target section')
  // Balance braces so the block ends where it really ends.
  let depth = 0
  for (let i = styles.indexOf('{', from); i < styles.length; i++) {
    if (styles[i] === '{') depth++
    else if (styles[i] === '}' && --depth === 0) return styles.slice(from, i + 1)
  }
  throw new Error('unbalanced coarse-pointer block')
}

// Tier -> the box height it takes on a phone, and the outward inset that buys
// the rest of the 44px target. These are the numbers the section's comment
// claims; the point of the test is that the claim and the CSS cannot drift.
const TIERS = { xs: 32, sm: 36, md: 40, iconbtn: 40 }

// An extension may not reach further than half the smallest gap the toolbars
// use (gap-2, 8px at the 95% root -> 7.6px). Beyond that it lands on the
// neighbour, which is exactly how the first implementation made the row below
// capture taps meant for the row above.
const MAX_INSET = 6

function declaredHeight(block, tier) {
  const m = new RegExp(`\\.touch-expand-${tier}\\s*\\{[^}]*?min-height:\\s*(\\d+)px`).exec(block)
  assert.ok(m, `.touch-expand-${tier} declares no min-height on coarse pointers`)
  return Number(m[1])
}

function declaredInset(block, tier) {
  // The tiers share one ::after rule for `content`/`position`, then each sets
  // its own insets. A plain search finds the shared rule first when the tier is
  // last in that selector list, so take the match that actually carries insets.
  const all = [...block.matchAll(new RegExp(`\\.touch-expand-${tier}::after\\s*\\{([^}]*)\\}`, 'g'))]
  const m = all.find((hit) => /top:\s*-?\d+px/.test(hit[1]))
  assert.ok(m, `.touch-expand-${tier}::after is missing; the tier has no hit extension`)
  const top = /top:\s*(-?\d+)px/.exec(m[1])
  const bottom = /bottom:\s*(-?\d+)px/.exec(m[1])
  assert.ok(top && bottom, `.touch-expand-${tier}::after sets no vertical inset`)
  assert.equal(top[1], bottom[1], `.touch-expand-${tier}::after is asymmetric; it would pull the target off centre`)
  return -Number(top[1])
}

test('no tier meets the touch floor by inflating its own box', () => {
  // The regression this replaces: min-height:44px on all four tiers. That is
  // box height, not a hit area, so every tier collapsed into one slab -- 439
  // of 602 visible controls measured exactly 44px on a 360px phone, and a chip
  // carrying 11.4px text became the same object as a primary button.
  const block = coarseBlock()
  for (const tier of Object.keys(TIERS)) {
    const h = declaredHeight(block, tier)
    assert.notEqual(h, 44,
      `.touch-expand-${tier} is back to a 44px box. Give it a real height and buy the target with ::after.`)
  }
})

test('each tier keeps its own height, in proportion to its type', () => {
  const block = coarseBlock()
  const seen = new Set()
  for (const [tier, expected] of Object.entries(TIERS)) {
    assert.equal(declaredHeight(block, tier), expected,
      `.touch-expand-${tier} should be ${expected}px on a phone`)
    seen.add(expected)
  }
  // Three distinct heights across four tiers (iconbtn shares md's 40px).
  assert.ok(seen.size >= 3, 'the tiers have collapsed back into a single height')
})

test('box plus extension is exactly the 44px target, in every tier', () => {
  const block = coarseBlock()
  for (const [tier, height] of Object.entries(TIERS)) {
    const inset = declaredInset(block, tier)
    assert.equal(height + inset * 2, 44,
      `.touch-expand-${tier}: ${height}px box + 2 x ${inset}px = ${height + inset * 2}px, not 44`)
  }
})

test('no extension reaches far enough to land on a neighbour', () => {
  const block = coarseBlock()
  for (const tier of Object.keys(TIERS)) {
    const inset = declaredInset(block, tier)
    assert.ok(inset <= MAX_INSET,
      `.touch-expand-${tier} extends ${inset}px, past the ${MAX_INSET}px bound. ` +
      'A gap-2 toolbar leaves 7.6px between controls; anything wider overlaps the next one.')
  }
})

test('a stacked row takes real height, because it has no gap to borrow', () => {
  const block = coarseBlock()
  assert.match(block, /\.touch-expand-row\s*\{[^}]*?min-height:\s*44px/,
    '.touch-expand-row must take the full 44px as box height')
  assert.doesNotMatch(block, /\.touch-expand-row::(?:after|before)/,
    'a stacked row must not extend outward: its neighbour is flush against it, ' +
    'which is how the sidebar produced 97 overlapping targets across the route set')
})

test('leaving overlays stop intercepting clicks immediately', () => {
  assert.match(styles, /\.fade-leave-active\s*\{[\s\S]*?pointer-events: none;/)
})
