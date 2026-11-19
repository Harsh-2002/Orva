import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount } from '@vue/test-utils'
import ToolCallCard from '@/components/ai/ToolCallCard.vue'

const SECRET = 'live-secret-0123456789abcdef'
const PLACEHOLDER = '[redacted: shown once to the operator]'
const CONV = 'conv-1'

const api = vi.hoisted(() => ({ get: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ default: api, getApiKey: () => '' }))

const { useAIStore } = await import('@/stores/ai.js')

// What the server returns after the turn: the stored, redacted copy.
function stored() {
  return {
    conversation: { id: CONV },
    messages: [
      { id: 'u1', role: 'user', parts: JSON.stringify([{ type: 'text', text: 'mint a key' }]) },
      { id: 'a1', role: 'assistant', parts: JSON.stringify([{ type: 'text', text: 'done' }]) },
    ],
    tool_calls: [{
      id: 'tc1', message_id: 'a1', call_id: 'c1', tool_name: 'create_api_key', tool_group: 'keys',
      args: '{}', status: 'succeeded', result: JSON.stringify({ id: 'k1', key: PLACEHOLDER }),
    }],
  }
}

function sse(frames) {
  const text = frames.map(([event, data]) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`).join('')
  return { ok: true, status: 200, body: new globalThis.Response(text).body }
}

function liveTurn() {
  return sse([
    ['conversation', { id: CONV, title: 'keys' }],
    ['message_start', { message_id: 'a1' }],
    ['tool_call', { id: 'tc1', call_id: 'c1', name: 'create_api_key', group: 'keys', args: {}, requires_approval: false }],
    ['tool_result', { id: 'tc1', call_id: 'c1', status: 'succeeded', result: { id: 'k1', key: SECRET }, one_time: true }],
    ['done', { conversation_id: CONV }],
  ])
}

const toolItem = (store) => store.timeline.find((it) => it.kind === 'tool')

describe('live one-time credential in the Chat store', () => {
  let store
  beforeEach(() => {
    setActivePinia(createPinia())
    store = useAIStore()
    api.get.mockReset().mockImplementation(async () => ({ data: stored() }))
    vi.stubGlobal('fetch', vi.fn(async () => liveTurn()))
    localStorage.clear()
    window.sessionStorage.clear()
  })

  it('survives the real post-turn refresh and is never written to browser storage', async () => {
    await store.sendMessage('mint a key')
    expect(api.get).toHaveBeenCalledWith(`/ai/conversations/${CONV}`)
    const tool = toolItem(store)
    expect(JSON.stringify(tool.result)).toContain(SECRET)
    expect(tool.oneTime).toBe(true)
    expect(tool.status).toBe('succeeded')
    const dump = JSON.stringify({ ...localStorage }) + JSON.stringify({ ...window.sessionStorage })
    expect(dump).not.toContain(SECRET)
  })

  it('shows only the placeholder on a fresh load or a conversation switch', async () => {
    await store.sendMessage('mint a key')
    await store.openConversation(CONV)
    expect(JSON.stringify(toolItem(store).result)).not.toContain(SECRET)
    expect(JSON.stringify(toolItem(store).result)).toContain(PLACEHOLDER)
    expect(toolItem(store).oneTime).toBeFalsy()

    setActivePinia(createPinia())
    const fresh = useAIStore()
    await fresh.openConversation(CONV)
    expect(JSON.stringify(fresh.timeline)).not.toContain(SECRET)
  })

  it('drops the value on explicit dismissal and when the view is left', async () => {
    await store.sendMessage('mint a key')
    store.dismissOneTime('tc1')
    expect(toolItem(store).result).toBe(PLACEHOLDER)
    expect(toolItem(store).oneTime).toBe(false)

    await store.sendMessage('again')
    expect(JSON.stringify(toolItem(store).result)).toContain(SECRET)
    store.dismissOneTime()
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
    await store.openConversation(CONV)
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
  })

  it('does not resurrect a value after the call disappears from the conversation', async () => {
    await store.sendMessage('mint a key')
    api.get.mockImplementation(async () => ({ data: { ...stored(), tool_calls: [] } }))
    await store.sendMessage('regen')
    api.get.mockImplementation(async () => ({ data: stored() }))
    await store.sendMessage('again')
    // The third turn minted a new live value; the pruned first one is not merged.
    expect(JSON.stringify(store.timeline).split(SECRET).length - 1).toBe(1)
  })

  it('leaving the Chat view drops the value (deactivate and unmount hooks)', () => {
    const view = readFileSync(resolve(globalThis.process.cwd(), 'src/views/AI.vue'), 'utf8')
    expect(view).toMatch(/onDeactivated\(\(\) => store\.dismissOneTime\(\)\)/)
    expect(view).toMatch(/onUnmounted\(\(\) => store\.dismissOneTime\(\)\)/)
  })
})

describe('Hide value control', () => {
  it('asks the owner to dismiss that tool call', async () => {
    const tool = { id: 'tc1', name: 'create_api_key', status: 'succeeded', args: {}, result: { key: SECRET }, oneTime: true }
    const view = mount(ToolCallCard, { props: { tool } })
    await view.get('[data-testid="one-time-note"] button').trigger('click')
    expect(view.emitted('dismiss-one-time')).toEqual([['tc1']])
  })
})
