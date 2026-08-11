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
  assert.match(modelMenu, /: 'touch-expand-sm h-8 max-w-\[180px\]/)
})

test('desktop popovers choose a direction from available viewport space', () => {
  assert.match(popover, /const spaceBelow =/)
  assert.match(popover, /const spaceAbove =/)
  assert.match(popover, /const opensDown =/)
  assert.match(popover, /top: `\$\{top\}px`/)
})

test('the agent loop budget stays an internal guardrail', () => {
  assert.doesNotMatch(settings, /Tool steps per reply|max_tool_iterations/)
})
