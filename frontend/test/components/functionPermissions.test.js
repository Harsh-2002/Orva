import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import FunctionPermissions from '@/views/FunctionPermissions.vue'
import {
  ACTOR_ID, CALLER_ID, DIGEST_A, DIGEST_B, FN_ID, OTHER_FN_ID, caps, makeSnapshot, policy,
} from '../fixtures/permissionsFixtures.js'

// jsdom has no matchMedia; Popover (inside FilterSelect) reads it on mount.
if (!window.matchMedia) {
  window.matchMedia = () => ({
    matches: false, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
  })
}

const mounted = []
const make = (props = {}) => {
  const w = mount(FunctionPermissions, {
    props: { functionId: FN_ID, snapshot: makeSnapshot(), ...props },
    attachTo: document.body,
  })
  mounted.push(w)
  return w
}
afterEach(() => { while (mounted.length) mounted.pop().unmount() })

const deepFreeze = (o) => {
  Object.values(o).forEach((v) => { if (v && typeof v === 'object') deepFreeze(v) })
  return Object.freeze(o)
}
const buttons = (w) => w.findAll('button')
const btn = (w, text) => buttons(w).find((b) => b.text().includes(text))
const has = (w, text) => !!btn(w, text)
const field = (w, label) => {
  const l = w.findAll('label').find((x) => x.text().replace('*', '').trim() === label)
  return l && w.find(`#${l.attributes('for')}`)
}
const snap = (host, rest = {}) => makeSnapshot({ host, ...rest })
const removeFirst = async (w) => {
  await btn(w, 'Remove').trigger('click')
  await flushPromises()
}

describe('FunctionPermissions states', () => {
  it('shows real data with one h1 and sectioned h2 headings', () => {
    const w = make()
    expect(w.findAll('h1')).toHaveLength(1)
    expect(w.findAll('h2').map((h) => h.text())).toEqual([
      'What this function may do', 'Who may call this function', 'Review', 'History',
    ])
    expect(w.text()).toContain('config/')
    expect(w.text()).toContain(OTHER_FN_ID)
    expect(w.text()).toContain('Enforcement active')
  })

  it('read only: no edit controls, and the reason is stated', () => {
    const w = make({ snapshot: snap({ capabilities: caps({ canManage: false }) }) })
    expect(has(w, 'Remove')).toBe(false)
    expect(has(w, 'Add to draft')).toBe(false)
    expect(has(w, 'Accept caller')).toBe(false)
    expect(w.text()).toContain('Read only')
    expect(w.text()).toContain('You can view this policy but not change it.')
  })

  it('manage plus active enforcement exposes labelled edit controls', () => {
    const w = make()
    expect(has(w, 'Remove')).toBe(true)
    expect(has(w, 'Add to draft')).toBe(true)
    const inputs = w.findAll('input')
    expect(inputs.length).toBeGreaterThan(0)
    for (const i of inputs) {
      expect(w.find(`label[for="${i.attributes('id')}"]`).exists()).toBe(true)
    }
    expect(w.findAll('select')).toHaveLength(0)
  })

  it.each(['inactive', 'unknown'])('%s enforcement denies editing and says so', (enforcement) => {
    const w = make({ snapshot: snap({ enforcement }) })
    expect(has(w, 'Remove')).toBe(false)
    expect(has(w, 'Add to draft')).toBe(false)
    expect(w.text()).toMatch(enforcement === 'inactive' ? /enforcement is not enabled/ : /did not report whether/)
  })

  it('without canRead nothing of the policy is rendered', () => {
    const w = make({ snapshot: snap({ capabilities: caps({ canRead: false }) }) })
    expect(w.text()).toContain('do not have access')
    expect(w.text()).not.toContain('config/')
    expect(w.text()).not.toContain(OTHER_FN_ID)
    expect(w.text()).not.toContain(CALLER_ID)
    expect(w.findAll('button').some((b) => b.text() === 'Request review')).toBe(false)
  })

  it('a snapshot of another function is denied, malformed capabilities fail false', () => {
    const other = make({ functionId: OTHER_FN_ID })
    expect(other.text()).toContain('belongs to a different function')
    expect(other.text()).not.toContain('config/')
    const bad = makeSnapshot()
    bad.host.capabilities = { canRead: 'true', canManage: 1 }
    const w = make({ snapshot: bad })
    expect(w.text()).toContain('do not have access')
  })

  it('preview adds only the banner and never widens capabilities', () => {
    const ok = make({ preview: true })
    expect(ok.text()).toContain('UI preview. No permissions are applied.')
    expect(has(ok, 'Remove')).toBe(true)
    const denied = make({ preview: true, snapshot: snap({ capabilities: caps({ canManage: false }) }) })
    expect(denied.text()).toContain('UI preview. No permissions are applied.')
    expect(has(denied, 'Remove')).toBe(false)
    const off = make({ preview: true, snapshot: snap({ enforcement: 'inactive' }) })
    expect(has(off, 'Remove')).toBe(false)
  })

  it('empty policy says no protected actions, with no SDK claim', () => {
    const w = make({ snapshot: makeSnapshot({ policy: { schema_version: 1, allow: [], inbound: [] } }) })
    expect(w.text()).toContain('No protected platform actions allowed.')
    expect(w.text()).not.toMatch(/SDK/)
  })

  it('quarantine and review-required read as retained, not in force', () => {
    const w = make({ snapshot: snap({ quarantined: true }) })
    expect(w.text()).toContain('Retained, not in force.')
    expect(w.text()).toContain('Quarantined')
    expect(w.text()).not.toMatch(/\bapplied\b/)
  })

  it('unresolved targets keep the UUID and never claim deletion', () => {
    const s = makeSnapshot()
    s.targets = [{ id: OTHER_FN_ID, label: null, state: 'missing' }]
    const w = make({ snapshot: s })
    expect(w.text()).toContain('Unresolved')
    expect(w.text()).toContain(OTHER_FN_ID)
    expect(w.text()).not.toMatch(/deleted/i)
    expect(w.text()).not.toContain('preview-renderer')
  })

  it('load error and loading are announced, retry emits', async () => {
    const e = make({ snapshot: null, loadError: 'boom' })
    expect(e.find('[role="alert"]').text()).toContain('could not be loaded')
    await e.find('[role="alert"] button').trigger('click')
    expect(e.emitted('retry')).toHaveLength(1)
    const l = make({ snapshot: null, loading: true })
    expect(l.text()).toContain('Loading permissions')
  })

  it('history is collapsed until asked and exposes expanded state', async () => {
    const w = make()
    const toggle = btn(w, 'revisions')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(w.find('#permissions-history-list').attributes('hidden')).toBeDefined()
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(w.text()).toContain(DIGEST_B)
    expect(w.text()).not.toMatch(/secret/i)
  })
})

describe('FunctionPermissions draft and save', () => {
  it('editing is local: a save bar appears, props stay untouched, nothing is emitted as applied', async () => {
    const frozen = deepFreeze(makeSnapshot())
    const w = make({ snapshot: frozen })
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    await removeFirst(w)
    expect(w.find('[aria-label="Unsaved permission changes"]').text()).toContain('1 unsaved change')
    expect(w.emitted('dirty-change').at(-1)).toEqual([true])
    expect(w.emitted('save')).toBeUndefined()
    expect(w.text()).toContain('Not saved or applied')
    expect(frozen.policy.allow).toHaveLength(3)
  })

  it('Save emits one payload with the policy and the expected host state only', async () => {
    const w = make()
    await removeFirst(w)
    const save = btn(w, 'Save permissions')
    await save.trigger('click')
    await save.trigger('click')
    expect(w.emitted('save')).toHaveLength(1)
    const [payload] = w.emitted('save')[0]
    expect(Object.keys(payload).sort()).toEqual(['expected', 'policy'])
    expect(payload.expected).toEqual({ revision: '7', currentArtifact: DIGEST_A, approvedArtifact: DIGEST_A })
    expect(payload.policy.allow).toHaveLength(2)
    expect(JSON.stringify(payload)).not.toMatch(/capab|actor|approved":true/)
    // Saving locks every action, including add and remove.
    await w.setProps({ saving: true })
    expect(btn(w, 'Discard').attributes('disabled')).toBeDefined()
    expect(btn(w, 'Remove').attributes('disabled')).toBeDefined()
    expect(btn(w, 'Save permissions').attributes('disabled')).toBeDefined()
  })

  it('the draft only becomes the saved policy when a fresh snapshot says so', async () => {
    const w = make()
    await removeFirst(w)
    await btn(w, 'Save permissions').trigger('click')
    await w.setProps({ saving: true })
    await w.setProps({ saving: false })
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    const next = makeSnapshot({ host: { revision: '8' } })
    next.policy.allow.splice(0, 1)
    await w.setProps({ snapshot: next })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    expect(w.text()).toContain('Revision 8')
    expect(w.emitted('dirty-change').at(-1)).toEqual([false])
  })

  it('discard restores the server policy', async () => {
    const w = make()
    await removeFirst(w)
    await btn(w, 'Discard').trigger('click')
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    expect(w.text()).toContain('config/')
  })

  it('a new server revision while dirty is a conflict, not an overwrite', async () => {
    const w = make()
    await removeFirst(w)
    const next = makeSnapshot({ host: { revision: '8', currentArtifact: DIGEST_B } })
    next.policy.inbound = []
    await w.setProps({ snapshot: next })
    await flushPromises()
    expect(w.text()).toContain('The server version changed while you were editing')
    expect(btn(w, 'Save permissions').attributes('disabled')).toBeDefined()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    const compare = btn(w, 'Compare with server')
    await compare.trigger('click')
    expect(compare.attributes('aria-expanded')).toBe('true')
    expect(w.find('#permissions-server-changes').text()).toContain('Removed')
    await btn(w, 'Discard draft and reload').trigger('click')
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    expect(w.text()).toContain('Revision 8')
  })

  it('revoking manage keeps the draft inspectable but removes edit controls and saving', async () => {
    const w = make()
    await removeFirst(w)
    await w.setProps({ snapshot: snap({ capabilities: caps({ canManage: false }) }) })
    await flushPromises()
    expect(has(w, 'Remove')).toBe(false)
    expect(has(w, 'Add to draft')).toBe(false)
    expect(w.text()).toContain('Your permission to change this policy was removed')
    expect(btn(w, 'Save permissions').attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('1 unsaved change')
  })

  it('save errors keep the draft and follow their kind', async () => {
    const w = make()
    await removeFirst(w)
    await w.setProps({ saveError: { kind: 'conflict', message: 'revision moved' } })
    expect(w.find('[role="alert"]').text()).toContain('changed on the server')
    expect(btn(w, 'Save permissions').attributes('disabled')).toBeDefined()
    await btn(w, 'Refresh').trigger('click')
    expect(w.emitted('retry')).toHaveLength(1)

    await w.setProps({ saveError: { kind: 'forbidden', message: 'no' } })
    expect(has(w, 'Remove')).toBe(false)
    expect(btn(w, 'Save permissions').attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('1 unsaved change')

    await w.setProps({ saveError: { kind: 'error', message: 'timeout' } })
    expect(has(w, 'Retry save')).toBe(true)
    expect(btn(w, 'Retry save').attributes('disabled')).toBeUndefined()
    await btn(w, 'Retry save').trigger('click')
    expect(w.emitted('save')).toHaveLength(1)

    await w.setProps({ saveError: { kind: 'validation', message: 'bad' } })
    expect(w.find('[role="alert"]').text()).toContain('rejected this policy')
    expect(has(w, 'Remove')).toBe(true)
  })

  it('changing the owner clears the draft and the messages', async () => {
    const w = make()
    await removeFirst(w)
    await w.setProps({ functionId: OTHER_FN_ID, saveError: null })
    expect(w.text()).toContain('belongs to a different function')
    const theirs = makeSnapshot()
    theirs.function.id = OTHER_FN_ID
    await w.setProps({ snapshot: theirs })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    expect(w.text()).toContain('config/')
  })

  it('adding counters never adds Read for you, and warns that the statement is incomplete', async () => {
    const w = make({ snapshot: makeSnapshot({ policy: { schema_version: 1, allow: [], inbound: [] } }) })
    await field(w, 'Key').setValue('visits')
    await btn(w, 'kv.incr').trigger('click')
    await btn(w, 'Add to draft').trigger('click')
    await flushPromises()
    const list = w.find('[aria-label="Allowed platform actions"]')
    expect(list.text()).toContain('visits')
    expect(list.text()).toContain('kv.incr')
    expect(list.findAll('li[title]').map((li) => li.attributes('title'))).toEqual(['kv.incr'])
    expect(list.find('[role="note"]').text()).toContain('also needs kv.get')
    expect(w.find('[aria-label="Unsaved permission changes"]').text()).toContain('1 unsaved change')
    expect(w.emitted('save')).toBeUndefined()
  })

  it('rejects an invalid add with an announced field error and no change', async () => {
    const w = make()
    await btn(w, 'Add to draft').trigger('click')
    await flushPromises()
    expect(w.findAll('[role="alert"]').length).toBeGreaterThan(0)
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    await btn(w, 'Accept caller').trigger('click')
    expect(w.text()).toContain('Enter the caller UUID in lowercase.')
  })

  it('accepting a caller is local, validated and shown with its triggers', async () => {
    const w = make()
    const id = '0190a1b2-c3d4-7e5f-9a6b-1c2d3e4f5a99'
    await field(w, 'Caller function UUID').setValue(id.toUpperCase())
    await btn(w, 'Accept caller').trigger('click')
    expect(w.text()).toContain('Enter the caller UUID in lowercase.')
    await field(w, 'Caller function UUID').setValue(id)
    await btn(w, 'Background job').trigger('click')
    await btn(w, 'Accept caller').trigger('click')
    await flushPromises()
    expect(w.find('[aria-label="Accepted callers"]').text()).toContain(id)
    expect(w.find('[aria-label="Unsaved permission changes"]').text()).toContain('Added')
  })
})

describe('FunctionPermissions review', () => {
  const reviewer = (host = {}) => snap({ capabilities: caps({ canReview: true }), ...host })

  it('offers review only with canReview and a known artifact, and never says approve', async () => {
    expect(has(make(), 'Request review')).toBe(false)
    expect(has(make({ snapshot: reviewer({ currentArtifact: null }) }), 'Request review')).toBe(false)
    const w = make({ snapshot: reviewer({ currentArtifact: DIGEST_B }) })
    expect(has(w, 'Request review')).toBe(true)
    expect(w.text()).not.toMatch(/Approve\b/)
    await btn(w, 'Request review').trigger('click')
    await btn(w, 'Request review').trigger('click')
    expect(w.emitted('review')).toHaveLength(1)
    const [payload] = w.emitted('review')[0]
    expect(Object.keys(payload).sort()).toEqual(['expected', 'policy'])
    expect(payload.expected.currentArtifact).toBe(DIGEST_B)
    expect(payload.policy).toEqual(policy())
  })

  it('is unavailable while there are unsaved changes', async () => {
    const w = make({ snapshot: reviewer() })
    await removeFirst(w)
    expect(btn(w, 'Request review').attributes('disabled')).toBeDefined()
    await btn(w, 'Request review').trigger('click')
    expect(w.emitted('review')).toBeUndefined()
  })
})

describe('FunctionPermissions conflict, review and unconfirmed-state regressions', () => {
  const saveBtn = (w) => btn(w, 'Save permissions')

  it('a conflict survives an A, B, A snapshot sequence until the operator discards', async () => {
    const w = make()
    await removeFirst(w)
    await w.setProps({ snapshot: makeSnapshot({ host: { revision: '8' } }) })
    await flushPromises()
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
    // A late response that looks like the original base must not release it.
    await w.setProps({ snapshot: makeSnapshot() })
    await flushPromises()
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('The server version changed while you were editing')
    // The banner reports the newest revision seen, not the late one.
    expect(w.find('[role="alert"]').text()).toMatch(/revision\s*7\s*to\s*8/)
    await saveBtn(w).trigger('click')
    expect(w.emitted('save')).toBeUndefined()
    await btn(w, 'Discard draft and reload').trigger('click')
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
  })

  it('a matching snapshot beside a conflict error neither adopts the draft nor moves the expected tokens', async () => {
    const w = make()
    await removeFirst(w)
    const next = makeSnapshot({ host: { revision: '8' } })
    next.policy.allow.shift()
    await w.setProps({ snapshot: next, saveError: { kind: 'conflict', message: 'revision moved' } })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
    // Clearing the error alone does not release the held conflict either.
    await w.setProps({ saveError: null })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
    expect(w.emitted('save')).toBeUndefined()
  })

  it('a matching snapshot adopts only when loading, errors and conflicts are all absent', async () => {
    for (const blocker of [{ loading: true }, { loadError: 'could not load' }]) {
      const w = make()
      await removeFirst(w)
      const next = makeSnapshot({ host: { revision: '8' } })
      next.policy.allow.shift()
      await w.setProps({ snapshot: next, ...blocker })
      await flushPromises()
      expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    }
  })

  describe('review retry', () => {
    const reviewer = () => makeSnapshot({ host: { capabilities: caps({ canReview: true }) } })
    const requested = async (props = {}) => {
      const w = make({ snapshot: reviewer(), ...props })
      await btn(w, 'Request review').trigger('click')
      await w.setProps({ saving: true })
      return w
    }

    it.each(['error', 'validation'])('a terminal %s error with nothing in flight allows a second request', async (kind) => {
      const w = await requested()
      await w.setProps({ saving: false, saveError: { kind, message: 'synthetic' } })
      expect(btn(w, 'Request review').attributes('disabled')).toBeUndefined()
      await btn(w, 'Request review').trigger('click')
      expect(w.emitted('review')).toHaveLength(2)
    })

    it('an error that lands while the request is still in flight frees it once saving ends', async () => {
      const w = await requested()
      await w.setProps({ saveError: { kind: 'error', message: 'synthetic' } })
      expect(btn(w, 'Request review').attributes('disabled')).toBeDefined()
      await w.setProps({ saving: false })
      expect(btn(w, 'Request review').attributes('disabled')).toBeUndefined()
    })

    it('stays locked while the request is in flight', async () => {
      const w = await requested()
      expect(btn(w, 'Request review').attributes('disabled')).toBeDefined()
      expect(w.emitted('review')).toHaveLength(1)
    })

    it.each(['forbidden', 'conflict'])('a %s error keeps review off', async (kind) => {
      const w = await requested()
      await w.setProps({ saving: false, saveError: { kind, message: 'synthetic' } })
      expect(btn(w, 'Request review').attributes('disabled')).toBeDefined()
      await btn(w, 'Request review').trigger('click')
      expect(w.emitted('review')).toHaveLength(1)
    })
  })

  describe('an empty list is not success copy while unconfirmed', () => {
    const empty = () => makeSnapshot({
      policy: { schema_version: 1, allow: [], inbound: [] },
      history: [],
    })
    const SUCCESS = ['No protected platform actions allowed', 'No callers accepted', 'No other function is accepted']

    it.each([
      ['loading', { loading: true }],
      ['a load error', { loadError: 'synthetic load failure' }],
    ])('with an old empty snapshot and %s', async (_name, over) => {
      const w = make({ snapshot: empty(), ...over })
      for (const copy of SUCCESS) expect(w.text()).not.toContain(copy)
      expect(w.text()).toContain('Not confirmed')
      expect(w.text()).not.toContain('No revisions recorded')
    })

    it('with no snapshot at all while loading', () => {
      const w = make({ snapshot: null, loading: true })
      expect(w.text()).toContain('Loading permissions')
      for (const copy of SUCCESS) expect(w.text()).not.toContain(copy)
    })

    it('shows the success copy once the data is confirmed', () => {
      const w = make({ snapshot: empty() })
      expect(w.text()).toContain('No protected platform actions allowed.')
      expect(w.text()).toContain('No callers accepted.')
    })
  })

  it('the unsaved-change list names the selector once, not twice', async () => {
    const w = make()
    await w.find('button[aria-label^="Remove"]').trigger('click')
    await flushPromises()
    await btn(w, 'Review changes').trigger('click')
    const text = w.find('#permissions-change-list').text()
    expect(text).toContain('Removed')
    expect(text).toMatch(/keys with a prefix config\/: kv\.get, kv\.list \(at startup\)/)
    expect(text).not.toMatch(/\bprefix prefix\b|\bkey key\b/)
  })

  it('history shows a labelled, canonical actor and never a free-text one', async () => {
    const s = makeSnapshot()
    s.history[0].actor = { kind: 'api_key', id: 'Bearer SYNTHETIC_NOT_A_REAL_CREDENTIAL' }
    const w = make({ snapshot: s })
    await btn(w, 'revision').trigger('click')
    expect(w.text()).not.toContain('Bearer')
    expect(w.find('#permissions-history-list').text()).toContain('Revision 6')
    expect(w.find('#permissions-history-list').text()).not.toContain('Revision 7')
    const ok = make()
    await btn(ok, 'revisions').trigger('click')
    expect(ok.find('#permissions-history-list').text()).toContain('By API key')
    expect(ok.find('#permissions-history-list').text()).toContain(ACTOR_ID)
  })
})

describe('FunctionPermissions revision ordering and in-flight snapshots', () => {
  const rev = (n, over = {}) => makeSnapshot({ host: { revision: String(n), ...over } })
  const saveBtn = (w) => btn(w, 'Save permissions')
  const matching = (n) => {
    const s = rev(n)
    s.policy.allow.shift()
    return s
  }

  it('a clean page that saw 8 will not save against a late 7, and recovers at 8 or newer', async () => {
    const w = make()
    await w.setProps({ snapshot: rev(8) })
    await w.setProps({ snapshot: rev(7) })
    await removeFirst(w)
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('older than revision 8')
    await saveBtn(w).trigger('click')
    expect(w.emitted('save')).toBeUndefined()
    await w.setProps({ snapshot: rev(9) })
    await flushPromises()
    await btn(w, 'Discard draft and reload').trigger('click')
    await removeFirst(w)
    expect(saveBtn(w).attributes('disabled')).toBeUndefined()
    expect(w.text()).not.toContain('older than revision')
  })

  it('discarding against an obsolete response does not lower the high-water mark', async () => {
    const w = make()
    await removeFirst(w)
    await w.setProps({ snapshot: rev(8) })
    await w.setProps({ snapshot: rev(7) })
    await btn(w, 'Discard draft and reload').trigger('click')
    await removeFirst(w)
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('older than revision 8')
  })

  it('an obsolete response also denies review, while its capability revocation still applies', async () => {
    const reviewer = (n, caps2) => rev(n, { capabilities: caps({ canReview: true, ...caps2 }) })
    const w = make({ snapshot: reviewer(8) })
    await w.setProps({ snapshot: reviewer(7) })
    expect(btn(w, 'Request review').attributes('disabled')).toBeDefined()
    await btn(w, 'Request review').trigger('click')
    expect(w.emitted('review')).toBeUndefined()
    await w.setProps({ snapshot: reviewer(7, { canManage: false }) })
    expect(has(w, 'Remove')).toBe(false)
  })

  it('an owner change resets the high-water mark, nothing else does', async () => {
    const w = make({ snapshot: rev(8) })
    const theirs = makeSnapshot({ host: { revision: '3' } })
    theirs.function.id = OTHER_FN_ID
    await w.setProps({ functionId: OTHER_FN_ID, snapshot: theirs })
    await flushPromises()
    await removeFirst(w)
    expect(saveBtn(w).attributes('disabled')).toBeUndefined()
  })

  it('a matching snapshot mid-save is not an acknowledgement, and a delayed conflict keeps the base', async () => {
    const w = make()
    await removeFirst(w)
    await saveBtn(w).trigger('click')
    await w.setProps({ saving: true })
    await w.setProps({ snapshot: matching(8) })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    await w.setProps({ saving: false, saveError: { kind: 'conflict', message: 'synthetic delayed conflict' } })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
    expect(w.emitted('save')).toHaveLength(1)
    expect(w.emitted('save')[0][0].expected.revision).toBe('7')
    // Even a later matching snapshot cannot settle a conflict that was reported.
    await w.setProps({ saveError: null, snapshot: matching(9) })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
  })

  it('a matching snapshot mid-save settles only on a fresh one after completion', async () => {
    const w = make()
    await removeFirst(w)
    await saveBtn(w).trigger('click')
    await w.setProps({ saving: true })
    await w.setProps({ snapshot: matching(8) })
    await w.setProps({ saving: false })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
    await w.setProps({ snapshot: matching(8) })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    expect(w.text()).toContain('Revision 8')
  })

  it('without a saving prop, a snapshot right after Save is still not an acknowledgement', async () => {
    const w = make()
    await removeFirst(w)
    await saveBtn(w).trigger('click')
    await w.setProps({ snapshot: matching(8) })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
  })
})
