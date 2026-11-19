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
    expect(view).toMatch(/onDeactivated\(\(\) => store\.setChatVisible\(false\)\)/)
    expect(view).toMatch(/onUnmounted\(\(\) => store\.setChatVisible\(false\)\)/)
    expect(view).toMatch(/onActivated\(\(\) => \{\s*store\.setChatVisible\(true\)/)
  })
})

// A stream the test feeds frame by frame, to deliver one after the view changed.
function controlledTurn() {
  const encoder = new globalThis.TextEncoder()
  let controller
  const body = new globalThis.ReadableStream({ start: (c) => { controller = c } })
  const tick = () => new Promise((resolve) => globalThis.setTimeout(resolve, 0))
  return {
    response: { ok: true, status: 200, body },
    async push(...frames) {
      for (const [event, data] of frames) controller.enqueue(encoder.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`))
      await tick()
    },
    close: () => controller.close(),
  }
}
const start = ['conversation', { id: CONV, title: 'keys' }]
const call = ['tool_call', { id: 'tc1', call_id: 'c1', name: 'create_api_key', group: 'keys', args: {}, requires_approval: false }]
const result = ['tool_result', { id: 'tc1', call_id: 'c1', status: 'succeeded', result: { id: 'k1', key: SECRET }, one_time: true }]

describe('frames that arrive after Chat stopped being live', () => {
  let store
  beforeEach(() => {
    setActivePinia(createPinia())
    store = useAIStore()
    api.get.mockReset().mockImplementation(async () => ({ data: stored() }))
    api.delete.mockReset()
  })

  it('never renders or keeps a value delivered while Chat is hidden', async () => {
    const turn = controlledTurn()
    vi.stubGlobal('fetch', vi.fn(async () => turn.response))
    const sending = store.sendMessage('mint a key')
    await turn.push(start, call)
    store.setChatVisible(false)
    await turn.push(result)
    // Straight after the frame, before the post-turn refresh.
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
    expect(toolItem(store).result).toBe(PLACEHOLDER)
    turn.close()
    await sending
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
    expect(JSON.stringify(toolItem(store).result)).toContain(PLACEHOLDER)
    expect(toolItem(store).oneTime).toBeFalsy()
    store.setChatVisible(true)
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
    await store.openConversation(CONV)
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
  })

  it('drops the visible value synchronously when a switch starts and ignores later frames', async () => {
    const turn2 = controlledTurn()
    vi.stubGlobal('fetch', vi.fn(async () => turn2.response))
    const sending2 = store.sendMessage('again')
    await turn2.push(start, call, result)
    expect(JSON.stringify(store.timeline)).toContain(SECRET)

    const pending = new Promise((resolve) => { api.get.mockImplementation(() => new Promise((r) => resolve(r))) })
    const switching = store.openConversation('other')
    // Before the destination request resolves, the old thread is still rendered.
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
    expect(store.timeline.some((it) => it.kind === 'tool')).toBe(true)

    // A late frame from the superseded stream must not bring the value back.
    await turn2.push(result)
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
    const resolveGet = await pending
    resolveGet({ data: stored() })
    await switching
    api.get.mockReset().mockImplementation(async () => ({ data: stored() }))
    turn2.close()
    await sending2
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
  })

  it('starting a new conversation also drops the value at once', async () => {
    const turn = controlledTurn()
    vi.stubGlobal('fetch', vi.fn(async () => turn.response))
    const sending = store.sendMessage('mint a key')
    await turn.push(start, call, result)
    expect(JSON.stringify(store.timeline)).toContain(SECRET)
    api.get.mockImplementation(() => new Promise(() => {}))
    store.newConversation()
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
    turn.close()
    await sending
    expect(JSON.stringify(store.timeline)).not.toContain(SECRET)
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
