import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const rail = readFileSync(new URL('../src/components/ai/ConversationRail.vue', import.meta.url), 'utf8')
const store = readFileSync(new URL('../src/stores/ai.js', import.meta.url), 'utf8')

test('conversation rail keeps clear-all visible outside the scrolling history', () => {
  const scrollEnd = rail.indexOf('</div>', rail.indexOf('overflow-y-auto scrollable'))
  const clearAction = rail.indexOf('Clear all chats')
  assert.ok(clearAction > scrollEnd)
  assert.match(rail, /v-if="store\.conversations\.length"/)
})

test('clear-all requires destructive confirmation and handles failures', () => {
  assert.match(rail, /title: 'Clear all chats\?'/)
  assert.match(rail, /danger: true/)
  assert.match(rail, /role="alert"/)
  assert.match(rail, /err\?\.response\?\.status === 409/)
})

test('chat store clears history with one bulk request and resets only after success', () => {
  const method = store.match(/async function clearConversations\(\) \{([\s\S]*?)\n {2}\}/)?.[1] || ''
  assert.match(method, /await apiClient\.delete\('\/ai\/conversations'\)/)
  assert.equal((method.match(/apiClient\.delete/g) || []).length, 1)
  assert.ok(method.indexOf('await apiClient.delete') < method.indexOf('conversations.value = []'))
  assert.match(method, /newConversation\(\)/)
})
