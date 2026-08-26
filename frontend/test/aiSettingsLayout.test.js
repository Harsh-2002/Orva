import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const settings = readFileSync(new URL('../src/components/ai/AISettingsPanel.vue', import.meta.url), 'utf8')
const modelMenu = readFileSync(new URL('../src/components/ai/ModelMenu.vue', import.meta.url), 'utf8')
const popover = readFileSync(new URL('../src/components/common/Popover.vue', import.meta.url), 'utf8')

test('active provider and model use the same field geometry', () => {
  assert.match(settings, /id="ai-active-provider"[\s\S]*?class="[^"]*h-10 w-full[^"]*rounded-md[^"]*text-sm/)
  assert.match(modelMenu, /wide[\s\S]*?'h-10 w-full justify-between rounded-md[^']*text-sm/)
})

test('wide model menus fill their settings column without changing compact chat menus', () => {
  assert.match(modelMenu, /<Popover[\s\S]*?:wide="wide"/)
  assert.match(popover, /:class="wide \? 'flex w-full' : 'inline-flex'"/)
  // The invariant is that the compact variant stays compact: a fixed height and
  // a width cap, never the wide variant's full-width field. The cap's exact
  // value is not the point and was pinned at 180px here, which is why widening
  // it to fit a real model id (nvidia/nemotron-3-nano-omni-30b-a3b-reasoning
  // truncated to "nvidia/nemotron-3…") failed a test about a different thing.
  const compact = modelMenu.match(/: '(touch-expand-sm h-8[^']*)'/)
  assert.ok(compact, 'compact trigger branch not found')
  assert.match(compact[1], /\bh-8\b/)
  assert.match(compact[1], /\bmax-w-\[/)
  assert.doesNotMatch(compact[1], /\bw-full\b/)
})

test('desktop popovers choose a direction from available viewport space', () => {
  assert.match(popover, /const spaceBelow =/)
  assert.match(popover, /const spaceAbove =/)
  assert.match(popover, /spaceAbove > spaceBelow/)
})

// The panel is anchored by the edge nearest its trigger, and capped to the space
// on that side.
//
// The previous version computed a `top` from the panel's CURRENT height on every
// call. A menu whose contents arrive asynchronously (the model list) was measured
// while nearly empty, so the direction and offset were chosen for a panel about
// 40px tall and the panel then grew somewhere else. Pinning `bottom` when opening
// upward means the anchor cannot drift while the height changes.
test('a popover stays anchored to its trigger while its contents load', () => {
  assert.match(popover, /bottom: `\$\{window\.innerHeight - r\.top \+ gap\}px`/)
  assert.match(popover, /maxHeight: `\$\{maxHeight\}px`/)
  // A cap without a scroller is just a clip.
  assert.match(popover, /class="fixed z-50[^"]*overflow-y-auto/)
  assert.match(popover, /ResizeObserver/)
})

// The bottom sheet is a touch affordance. Keying it off width alone turned a
// narrow desktop window into a phone, which is why the menu appeared to open
// somewhere unrelated to the control that summoned it.
test('the bottom sheet is reserved for coarse pointers', () => {
  assert.match(popover, /matchMedia\('\(max-width: 639px\) and \(pointer: coarse\)'\)/)
})

test('the agent loop budget stays an internal guardrail', () => {
  assert.doesNotMatch(settings, /Tool steps per reply|max_tool_iterations/)
})
