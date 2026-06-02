// AI chat store — drives the in-product assistant. Owns conversation state,
// the streaming client (fetch + ReadableStream, since the chat POST carries a
// body and EventSource can't), provider/model/settings config, and the
// timeline the chat view renders.
//
// Timeline model: a flat ordered list the view renders top-to-bottom. Each
// item is either a message bubble or a tool-call card:
//   { kind: 'message', role: 'user'|'assistant', parts: [{type:'text'|'thinking', text}] }
//   { kind: 'tool', id, call_id, name, group, args, status, result }
// Assistant text/thinking stream into the current message item; tool calls and
// their results are separate items so approval cards render inline in order.
import { defineStore } from 'pinia'
import { ref } from 'vue'
import apiClient, { getApiKey } from '@/api/client'

export const useAIStore = defineStore('ai', () => {
  const conversations = ref([])
  const activeId = ref(null)
  const timeline = ref([])
  const streaming = ref(false)
  const awaitingApproval = ref(false)
  const error = ref('')

  const settings = ref(null)
  const providers = ref([])

  // Active provider/model/thinking selection lives in the CHAT (not settings):
  // the user switches provider + model + thinking inline. Models are fetched
  // dynamically per provider from its /v1/models endpoint (never hardcoded).
  const selectedProviderId = ref(null) // ai_provider_configs.id
  const selectedProvider = ref('')     // provider TYPE (openai/anthropic/…)
  const selectedModel = ref('')
  const thinking = ref('off')          // off | standard | deep
  const models = ref([])               // models for the selected provider
  const modelsError = ref('')
  const modelsLoading = ref(false)

  // Index of the streaming assistant message in timeline.value. We track an
  // INDEX, not a raw object reference: timeline is a ref([]) whose elements are
  // reactive proxies, and Vue only re-renders when a mutation goes THROUGH the
  // proxy. Mutating a captured raw object's nested props (the old bug) never
  // notified Vue, so streamed tokens piled up invisibly until the next
  // array-level change. Every write below goes through timeline.value[idx].
  let curIdx = -1
  let abort = null // AbortController for the in-flight stream (stop button)
  // The last turn-producing action, so the error card's Retry can re-run it.
  let lastAction = null

  // Optional per-request model overrides, sent with chat/regenerate/edit so a
  // re-run honours the model the user currently has selected.
  function overrides() {
    const o = { thinking: thinking.value }
    if (selectedProvider.value) o.provider = selectedProvider.value
    if (selectedModel.value) o.model = selectedModel.value
    return o
  }

  // Errors render as their own timeline item (kind 'error') with a Retry/Dismiss
  // affordance, in flow where the failure happened — not as a fake assistant turn.
  let errSeq = 0
  function pushError(message, code) {
    error.value = message
    timeline.value.push({ kind: 'error', id: `err-${++errSeq}`, message, code: code || '' })
  }
  function dismissError(id) {
    timeline.value = timeline.value.filter((it) => !(it.kind === 'error' && it.id === id))
  }
  function clearErrors() {
    timeline.value = timeline.value.filter((it) => it.kind !== 'error')
  }

  // Total token count from a stored token_usage object (shape varies by provider).
  function tokenCount(usage) {
    let u = usage
    if (typeof u === 'string') { try { u = JSON.parse(u) } catch { return null } }
    if (!u || typeof u !== 'object') return null
    const total = u.total_tokens ?? u.total ??
      ((u.prompt_tokens ?? u.input_tokens ?? 0) + (u.completion_tokens ?? u.output_tokens ?? 0))
    return total > 0 ? total : null
  }

  // Re-fetch the active conversation from the server, replacing the optimistic
  // timeline with server truth (correct ids + created_at + token_usage). Run
  // after a turn so regenerate/edit re-sync ids and metadata appears.
  async function refreshActive() {
    if (!activeId.value) return
    try {
      const { data } = await apiClient.get(`/ai/conversations/${activeId.value}`)
      timeline.value = buildTimeline(data)
    } catch { /* keep the optimistic timeline on a refresh failure */ }
  }

  // ensureAssistant guarantees there's a streaming assistant message to patch.
  function ensureAssistant() {
    if (curIdx >= 0 && timeline.value[curIdx]?.kind === 'message') return
    timeline.value.push({ kind: 'message', role: 'assistant', parts: [] })
    curIdx = timeline.value.length - 1
  }

  // patchAssistant applies an immutable update to the streaming assistant
  // message and writes it back by index, so the reactive proxy fires.
  function patchAssistant(mutate) {
    const msg = timeline.value[curIdx]
    if (!msg) return
    const next = { ...msg, parts: msg.parts.slice() }
    mutate(next)
    timeline.value[curIdx] = next
  }

  // ─── SSE frame parsing over a fetch ReadableStream ──────────────────────

  function parseFrame(frame) {
    let event = 'message'
    let data = ''
    for (const line of frame.split('\n')) {
      if (line.startsWith('event:')) event = line.slice(6).trim()
      else if (line.startsWith('data:')) data += line.slice(5).trim()
    }
    let parsed = {}
    if (data) {
      try { parsed = JSON.parse(data) } catch { parsed = { raw: data } }
    }
    return { event, data: parsed }
  }

  function handleFrame(event, data) {
    switch (event) {
      case 'conversation':
        activeId.value = data.id
        if (!conversations.value.find((c) => c.id === data.id)) {
          conversations.value.unshift({ id: data.id, title: data.title || 'New conversation', updated_at: new Date().toISOString() })
        }
        break
      case 'message_start':
        timeline.value.push({ kind: 'message', role: 'assistant', id: data.message_id, parts: [] })
        curIdx = timeline.value.length - 1
        break
      case 'delta':
        ensureAssistant()
        patchAssistant((m) => {
          const last = m.parts[m.parts.length - 1]
          if (last && last.type === 'text') {
            m.parts[m.parts.length - 1] = { ...last, text: last.text + data.text }
          } else {
            m.parts.push({ type: 'text', text: data.text })
          }
        })
        break
      case 'thinking':
        ensureAssistant()
        patchAssistant((m) => {
          const i = m.parts.findIndex((p) => p.type === 'thinking')
          if (i >= 0) {
            m.parts[i] = { ...m.parts[i], text: m.parts[i].text + data.text }
          } else {
            // streaming/startedAt are transient view fields that drive the live
            // ThinkingBlock (timer + shimmer); rehydrated parts simply lack them.
            m.parts.unshift({ type: 'thinking', text: data.text, streaming: true, startedAt: Date.now() })
          }
        })
        break
      case 'tool_call':
        timeline.value.push({
          kind: 'tool',
          id: data.id,
          call_id: data.call_id,
          name: data.name,
          group: data.group,
          args: data.args,
          status: data.requires_approval ? 'pending_approval' : 'running',
          result: null,
        })
        break
      case 'tool_result': {
        // Find the matching tool item and REPLACE it by index (reactive).
        for (let i = timeline.value.length - 1; i >= 0; i--) {
          const t = timeline.value[i]
          if (t.kind === 'tool' && t.id === data.id) {
            timeline.value[i] = { ...t, status: data.status, result: data.result }
            break
          }
        }
        break
      }
      case 'awaiting_approval':
        awaitingApproval.value = true
        streaming.value = false
        // Stop the thinking timer + collapse it: the turn is paused for approval,
        // not still reasoning, so it shouldn't keep ticking "Thinking… Ns".
        if (curIdx >= 0) {
          patchAssistant((m) => {
            const i = m.parts.findIndex((p) => p.type === 'thinking')
            if (i >= 0) m.parts[i] = { ...m.parts[i], streaming: false }
          })
        }
        break
      case 'message_end':
        // Mark the thinking part finished so ThinkingBlock stops its timer + collapses.
        if (curIdx >= 0) {
          patchAssistant((m) => {
            const i = m.parts.findIndex((p) => p.type === 'thinking')
            if (i >= 0) m.parts[i] = { ...m.parts[i], streaming: false }
          })
        }
        curIdx = -1
        break
      case 'done':
        streaming.value = false
        break
      case 'error':
        pushError(data.message || 'stream error', data.code)
        streaming.value = false
        break
    }
  }

  async function consumeStream(url, body) {
    const headers = { 'Content-Type': 'application/json' }
    const key = getApiKey()
    if (key) headers['X-Orva-API-Key'] = key
    const res = await fetch(url, {
      method: 'POST', credentials: 'include', headers,
      body: JSON.stringify(body), signal: abort?.signal,
    })
    if (!res.ok || !res.body) {
      throw new Error(`chat request failed (${res.status})`)
    }
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      let idx
      while ((idx = buf.indexOf('\n\n')) >= 0) {
        const frame = buf.slice(0, idx)
        buf = buf.slice(idx + 2)
        if (frame.trim()) {
          const { event, data } = parseFrame(frame)
          handleFrame(event, data)
        }
      }
    }
  }

  // ─── actions ────────────────────────────────────────────────────────────

  async function sendMessage(content) {
    if (!content.trim() || streaming.value) return
    error.value = ''
    clearErrors()
    awaitingApproval.value = false
    lastAction = { type: 'chat', content }
    timeline.value.push({ kind: 'message', role: 'user', parts: [{ type: 'text', text: content }] })
    streaming.value = true
    curIdx = -1
    abort = new AbortController()
    try {
      const body = { content, ...overrides() }
      if (activeId.value) body.conversation_id = activeId.value
      await consumeStream('/api/v1/ai/chat', body)
    } catch (e) {
      if (e.name !== 'AbortError') pushError(e.message)
    } finally {
      streaming.value = false
      abort = null
      await refreshActive()
    }
  }

  // regenerate drops the last assistant turn and re-runs the last user message.
  async function regenerate() {
    if (streaming.value || !activeId.value) return
    // Drop trailing items after the last user message (assistant turn + any
    // error item) so the fresh answer replaces them.
    const tl = timeline.value
    let cut = tl.length
    for (let i = tl.length - 1; i >= 0; i--) {
      if (tl[i].kind === 'message' && tl[i].role === 'user') break
      cut = i
    }
    timeline.value = tl.slice(0, cut)
    error.value = ''
    awaitingApproval.value = false
    lastAction = { type: 'regenerate' }
    streaming.value = true
    curIdx = -1
    abort = new AbortController()
    try {
      await consumeStream(`/api/v1/ai/conversations/${activeId.value}/regenerate`, overrides())
    } catch (e) {
      if (e.name !== 'AbortError') pushError(e.message)
    } finally {
      streaming.value = false
      abort = null
      await refreshActive()
    }
  }

  // editAndResend rewrites a user message and re-runs from there.
  async function editAndResend(messageId, content) {
    if (streaming.value || !activeId.value || !content.trim()) return
    const tl = timeline.value
    const idx = tl.findIndex((it) => it.kind === 'message' && it.id === messageId)
    if (idx < 0) return
    // Optimistically truncate from the edited message and show the new text.
    timeline.value = [...tl.slice(0, idx), { ...tl[idx], parts: [{ type: 'text', text: content }] }]
    error.value = ''
    awaitingApproval.value = false
    lastAction = { type: 'edit', messageId, content }
    streaming.value = true
    curIdx = -1
    abort = new AbortController()
    try {
      await consumeStream(`/api/v1/ai/conversations/${activeId.value}/messages/${messageId}/edit`, { content, ...overrides() })
    } catch (e) {
      if (e.name !== 'AbortError') pushError(e.message)
    } finally {
      streaming.value = false
      abort = null
      await refreshActive()
    }
  }

  // deleteMessageFrom truncates the conversation from a message onward.
  async function deleteMessageFrom(messageId) {
    if (!activeId.value || streaming.value) return
    const tl = timeline.value
    const idx = tl.findIndex((it) => it.kind === 'message' && it.id === messageId)
    if (idx < 0) return
    timeline.value = tl.slice(0, idx) // optimistic
    try {
      await apiClient.delete(`/ai/conversations/${activeId.value}/messages/${messageId}`)
    } catch (e) {
      pushError(e.message)
      await refreshActive() // restore server state on failure
    }
  }

  // retry re-runs the last failed action (used by the error card). For a
  // tool decision, re-decide. Otherwise sync to the server first: if the last
  // turn made it (the tail is a user message awaiting a reply) regenerate it;
  // if the turn never reached the server (e.g. the request failed outright),
  // resend its content so nothing is lost.
  async function retry() {
    clearErrors()
    const a = lastAction
    if (a?.type === 'tool') return decideTool(a.rowId, a.approved)
    if (a?.type === 'regenerate') return regenerate()
    await refreshActive()
    const last = timeline.value[timeline.value.length - 1]
    if (last && last.kind === 'message' && last.role === 'user') return regenerate()
    if (a?.content) return sendMessage(a.content)
  }

  // stop aborts the in-flight stream (the composer's Stop button).
  function stop() {
    if (abort) abort.abort()
    streaming.value = false
  }

  async function decideTool(rowId, approved) {
    if (streaming.value) return
    error.value = ''
    clearErrors()
    awaitingApproval.value = false
    lastAction = { type: 'tool', rowId, approved }
    streaming.value = true
    curIdx = -1
    abort = new AbortController()
    const verb = approved ? 'approve' : 'reject'
    try {
      await consumeStream(`/api/v1/ai/tool-calls/${rowId}/${verb}`, {})
    } catch (e) {
      if (e.name !== 'AbortError') pushError(e.message)
    } finally {
      streaming.value = false
      abort = null
      await refreshActive()
    }
  }
  const approveTool = (id) => decideTool(id, true)
  const rejectTool = (id) => decideTool(id, false)

  // ─── conversations ────────────────────────────────────────────────────

  function buildTimeline(detail) {
    const items = []
    const callsByMsg = {}
    for (const tc of detail.tool_calls || []) {
      ;(callsByMsg[tc.message_id] ||= []).push(tc)
    }
    for (const m of detail.messages || []) {
      const parts = displayParts(m.parts)
      if (m.role === 'user') {
        items.push({ kind: 'message', role: 'user', id: m.id, parts })
      } else if (m.role === 'assistant') {
        items.push({ kind: 'message', role: 'assistant', id: m.id, parts })
        for (const tc of callsByMsg[m.id] || []) {
          items.push({
            kind: 'tool', id: tc.id, call_id: tc.call_id, name: tc.tool_name,
            group: tc.tool_group, args: tc.args, status: tc.status, result: tc.result,
          })
        }
      }
      // role 'tool'/'system' are internal LLM-history rows — not rendered.
    }
    return items
  }

  // displayParts keeps only the renderable text/thinking parts (tool_call
  // parts are rendered as separate tool items, not inside the bubble).
  function displayParts(raw) {
    let ps = []
    try { ps = JSON.parse(raw || '[]') } catch { ps = [] }
    return ps.filter((p) => p.type === 'text' || p.type === 'thinking')
  }

  async function loadConversations() {
    const { data } = await apiClient.get('/ai/conversations')
    conversations.value = data.conversations || []
  }

  async function openConversation(id) {
    const { data } = await apiClient.get(`/ai/conversations/${id}`)
    activeId.value = id
    timeline.value = buildTimeline(data)
    awaitingApproval.value = false
    error.value = ''
  }

  function newConversation() {
    activeId.value = null
    timeline.value = []
    awaitingApproval.value = false
    error.value = ''
  }

  async function deleteConversation(id) {
    await apiClient.delete(`/ai/conversations/${id}`)
    conversations.value = conversations.value.filter((c) => c.id !== id)
    if (activeId.value === id) newConversation()
  }

  // exportActive downloads the active conversation as a Markdown transcript.
  function exportActive() {
    const items = timeline.value
    if (!items.length) return
    const conv = conversations.value.find((c) => c.id === activeId.value)
    const lines = [`# ${conv?.title || 'Conversation'}`, '']
    for (const it of items) {
      if (it.kind === 'message') {
        const text = (it.parts || []).filter((p) => p.type === 'text' && p.text).map((p) => p.text).join('\n\n').trim()
        if (text) lines.push(`## ${it.role === 'user' ? 'You' : 'Assistant'}`, '', text, '')
      } else if (it.kind === 'tool') {
        lines.push(`> tool \`${it.name}\` — ${it.status}`, '')
      }
    }
    const blob = new Blob([lines.join('\n')], { type: 'text/markdown' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${(conv?.title || 'conversation').replace(/[^\w.-]+/g, '-').slice(0, 60) || 'conversation'}.md`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  }

  async function renameConversation(id, title) {
    const clean = (title || '').trim()
    if (!clean) return
    const { data } = await apiClient.patch(`/ai/conversations/${id}`, { title: clean })
    const c = conversations.value.find((x) => x.id === id)
    if (c) c.title = data.conversation?.title ?? clean
  }

  // ─── settings / providers / models ──────────────────────────────────────

  async function loadSettings() {
    const { data } = await apiClient.get('/ai/settings')
    settings.value = data.settings
    if (!thinking.value || thinking.value === 'off') {
      thinking.value = data.settings?.thinking_level || 'off'
    }
  }
  async function saveSettings(s) {
    const { data } = await apiClient.put('/ai/settings', s)
    settings.value = data.settings
  }

  async function loadProviders() {
    const { data } = await apiClient.get('/ai/providers')
    providers.value = data.providers || []
    // Auto-select a provider on first load so the chat is usable immediately.
    if (!selectedProviderId.value) {
      const first = providers.value.find((p) => p.enabled) || providers.value[0]
      if (first) await selectProvider(first.id)
    } else if (!providers.value.find((p) => p.id === selectedProviderId.value)) {
      // The selected provider was removed — fall back.
      const first = providers.value[0]
      if (first) await selectProvider(first.id)
      else { selectedProviderId.value = null; selectedProvider.value = ''; selectedModel.value = ''; models.value = [] }
    }
  }

  // selectProvider switches the active provider and loads ITS models dynamically.
  async function selectProvider(providerId) {
    const p = providers.value.find((x) => x.id === providerId)
    if (!p) return
    selectedProviderId.value = p.id
    selectedProvider.value = p.provider
    selectedModel.value = ''
    await loadProviderModels(p.id)
    // Prefer a cheaper/faster model over whatever the endpoint lists first
    // (often the newest + priciest), so an operator doesn't unknowingly run a
    // large model. Falls back to the first listed model.
    if (models.value.length) {
      const cheap = models.value.find((m) => /\b(mini|small|fast|flash|lite|nano|haiku)\b/i.test(m.id))
      selectedModel.value = (cheap || models.value[0]).id
    }
  }

  async function loadProviderModels(providerId) {
    modelsLoading.value = true
    modelsError.value = ''
    models.value = []
    try {
      const { data } = await apiClient.get(`/ai/providers/${providerId}/models`)
      models.value = data.models || []
      if (data.error) modelsError.value = data.error
    } catch (e) {
      modelsError.value = e.message
    } finally {
      modelsLoading.value = false
    }
  }

  function selectModel(modelId) { selectedModel.value = modelId }
  function setThinking(level) { thinking.value = level }

  async function saveProvider(p) {
    await apiClient.post('/ai/providers', p)
    await loadProviders()
  }
  async function deleteProvider(id) {
    await apiClient.delete(`/ai/providers/${id}`)
    await loadProviders()
  }

  return {
    conversations, activeId, timeline, streaming, awaitingApproval, error,
    settings, providers,
    selectedProviderId, selectedProvider, selectedModel, thinking, models, modelsError, modelsLoading,
    sendMessage, approveTool, rejectTool, stop,
    regenerate, editAndResend, deleteMessageFrom, retry, dismissError,
    loadConversations, openConversation, newConversation, deleteConversation, renameConversation, exportActive,
    loadSettings, saveSettings, loadProviders, saveProvider, deleteProvider,
    selectProvider, selectModel, setThinking, loadProviderModels,
  }
})
