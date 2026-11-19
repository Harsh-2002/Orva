import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import FunctionPermissions from '@/views/FunctionPermissions.vue'
import SaveBar from '@/components/permissions/SaveBar.vue'
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
  const l = w.findAll('label').find((x) => x.text().replaceAll('*', '').trim() === label)
  return l && w.find(`#${l.attributes('for')}`)
}
const snap = (host, rest = {}) => makeSnapshot({ host, ...rest })
// The page now opens as a read view. These helpers only walk the new screens;
// they change how a test gets to a state, never what it asserts about it.
const enterEdit = async (w) => {
  if (has(w, 'Edit permissions')) {
    await btn(w, 'Edit permissions').trigger('click')
    await flushPromises()
  }
}
const removeFirst = async (w) => {
  await enterEdit(w)
  await btn(w, 'Remove').trigger('click')
  await flushPromises()
}
// Save lives only on the review screen.
const toReview = async (w) => {
  if (!has(w, 'Review changes')) return
  await btn(w, 'Review changes').trigger('click')
  await flushPromises()
}
const dirtyReview = async (w) => {
  await removeFirst(w)
  await toReview(w)
}
const confirmDiscard = async (w) => {
  await btn(w, 'Discard').trigger('click')
  await btn(w, 'Discard changes').trigger('click')
  await flushPromises()
}
const radioGroupChecked = (w, text) => buttons(w).find((b) => b.text() === text).attributes('aria-checked') === 'true'
const radio = (w, text) => w.findAll('label').find((l) => l.text().includes(text)).find('input')
const pick = async (w, text) => {
  const i = radio(w, text)
  await i.setValue(true)
  await flushPromises()
}
const click = async (w, text) => {
  await btn(w, text).trigger('click')
  await flushPromises()
}

describe('stored schedule target restrictions', () => {
  it('describes a foreign target honestly and refuses to edit it into a new grant', async () => {
    const snapshot = makeSnapshot({
      policy: { schema_version: 1, allow: [{ actions: ['crons.upsert'], resource: { own_cron: { name: 'nightly', target_id: OTHER_FN_ID } }, context: 'execution' }], inbound: [] },
    })
    const w = make({ snapshot })
    expect(w.text()).toContain(`Unsupported stored schedule target ${OTHER_FN_ID}`)
    expect(w.text()).not.toContain('running this function')
    await enterEdit(w)
    await w.find('button[aria-label^="Edit Own schedule"]').trigger('click')
    await click(w, 'Next')
    await field(w, 'Schedule name').setValue('changed')
    await click(w, 'Next')
    expect(w.text()).toContain('Step 3 of 4: Resources')
    expect(w.text()).not.toContain('Step 4 of 4: Confirm')
    expect(w.text()).toContain('Schedules may run only this function')
    expect(w.emitted('save')).toBeUndefined()
    expect(snapshot.policy.allow[0].resource.own_cron).toEqual({ name: 'nightly', target_id: OTHER_FN_ID })
  })
})

describe('FunctionPermissions states', () => {
  it('shows real data with one h1 and sectioned h2 headings', () => {
    const w = make()
    expect(w.findAll('h1')).toHaveLength(1)
    expect(w.findAll('h2').map((h) => h.text())).toEqual([
      'What this function may do', 'Who may call this function', 'Advanced',
    ])
    expect(w.text()).toContain('config/')
    expect(w.text()).toContain(OTHER_FN_ID)
    expect(w.text()).toContain('Enforcement active')
  })

  it('read only: no edit controls, and the reason is stated', () => {
    const w = make({ snapshot: snap({ capabilities: caps({ canManage: false }) }) })
    expect(has(w, 'Remove')).toBe(false)
    expect(has(w, 'Add permission')).toBe(false)
    expect(has(w, 'Edit permissions')).toBe(false)
    expect(has(w, 'Create policy')).toBe(false)
    expect(w.text()).toContain('Read only')
    expect(w.text()).toContain('You can view this policy but not change it.')
  })

  it('manage plus active enforcement offers one Edit action, and editing exposes labelled controls', async () => {
    const w = make()
    // The default view is for reading: no forms, no row actions.
    expect(has(w, 'Edit permissions')).toBe(true)
    expect(has(w, 'Remove')).toBe(false)
    expect(has(w, 'Add permission')).toBe(false)
    expect(w.findAll('input')).toHaveLength(0)
    await enterEdit(w)
    expect(has(w, 'Remove')).toBe(true)
    expect(has(w, 'Add permission')).toBe(true)
    await click(w, 'Add permission')
    const inputs = w.findAll('input')
    expect(inputs.length).toBeGreaterThan(0)
    for (const i of inputs) {
      const named = w.find(`label[for="${i.attributes('id')}"]`).exists() || !!i.element.closest('label')
      expect(named).toBe(true)
    }
    expect(w.findAll('select')).toHaveLength(0)
  })

  it.each(['inactive', 'unknown'])('%s enforcement denies editing and says so', (enforcement) => {
    const w = make({ snapshot: snap({ enforcement }) })
    expect(has(w, 'Remove')).toBe(false)
    expect(has(w, 'Edit permissions')).toBe(false)
    expect(has(w, 'Add permission')).toBe(false)
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
    expect(has(ok, 'Edit permissions')).toBe(true)
    const denied = make({ preview: true, snapshot: snap({ capabilities: caps({ canManage: false }) }) })
    expect(denied.text()).toContain('UI preview. No permissions are applied.')
    expect(has(denied, 'Edit permissions')).toBe(false)
    const off = make({ preview: true, snapshot: snap({ enforcement: 'inactive' }) })
    expect(has(off, 'Edit permissions')).toBe(false)
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
    await dirtyReview(w)
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
    expect(btn(w, 'Back to editing').attributes('disabled')).toBeDefined()
    expect(btn(w, 'Save permissions').attributes('disabled')).toBeDefined()
  })

  it('saving locks every mutation on the edit screen too', async () => {
    const w = make()
    await removeFirst(w)
    await w.setProps({ saving: true })
    for (const label of ['Remove', 'Edit', 'Add permission', 'Review changes', 'Discard']) {
      expect(btn(w, label).attributes('disabled')).toBeDefined()
    }
    await w.find('button[aria-label^="Remove"]').trigger('click')
    expect(w.find('[aria-label="Unsaved permission changes"]').text()).toContain('1 unsaved change')
  })

  it('the draft only becomes the saved policy when a fresh snapshot says so', async () => {
    const w = make()
    await dirtyReview(w)
    await btn(w, 'Save permissions').trigger('click')
    await w.setProps({ saving: true })
    await w.setProps({ saving: false })
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    // The stored view is not changed by a pending save, only by a fresh snapshot.
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
    // Discarding asks first, inline, and Keep editing leaves the draft alone.
    await btn(w, 'Discard').trigger('click')
    expect(w.find('[aria-label="Confirm discard"]').exists()).toBe(true)
    await btn(w, 'Keep editing').trigger('click')
    expect(w.find('[aria-label="Unsaved permission changes"]').text()).toContain('1 unsaved change')
    await confirmDiscard(w)
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    expect(w.text()).toContain('config/')
  })

  it('a new server revision while dirty is a conflict, not an overwrite', async () => {
    const w = make()
    await dirtyReview(w)
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
    expect(has(w, 'Edit')).toBe(false)
    expect(has(w, 'Add permission')).toBe(false)
    expect(w.text()).toContain('Your permission to change this policy was removed')
    expect(w.text()).toContain('1 unsaved change')
    // The draft stays readable, and its changes can still be inspected.
    await toReview(w)
    expect(btn(w, 'Save permissions').attributes('disabled')).toBeDefined()
    expect(w.find('#permissions-change-list').text()).toContain('Removed')
  })

  it('save errors keep the draft and follow their kind', async () => {
    const w = make()
    await dirtyReview(w)
    await w.setProps({ saveError: { kind: 'conflict', message: 'revision moved' } })
    expect(w.find('[role="alert"]').text()).toContain('changed on the server')
    expect(btn(w, 'Save permissions').attributes('disabled')).toBeDefined()
    await btn(w, 'Refresh').trigger('click')
    expect(w.emitted('retry')).toHaveLength(1)

    await w.setProps({ saveError: { kind: 'forbidden', message: 'no' } })
    expect(btn(w, 'Save permissions').attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('1 unsaved change')
    await click(w, 'Back to editing')
    expect(has(w, 'Remove')).toBe(false)
    expect(has(w, 'Add permission')).toBe(false)
    await toReview(w)

    await w.setProps({ saveError: { kind: 'error', message: 'timeout' } })
    expect(has(w, 'Retry save')).toBe(true)
    expect(btn(w, 'Retry save').attributes('disabled')).toBeUndefined()
    await btn(w, 'Retry save').trigger('click')
    expect(w.emitted('save')).toHaveLength(1)

    await w.setProps({ saveError: { kind: 'validation', message: 'bad' } })
    expect(w.find('[role="alert"]').text()).toContain('rejected this policy')
    await click(w, 'Back to editing')
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

  const emptySnap = (over = {}) => makeSnapshot({ policy: { schema_version: 1, allow: [], inbound: [] }, ...over })
  const nextStep = (w) => click(w, 'Next')

  it('adding counters never adds Read for you, and warns that the statement is incomplete', async () => {
    const w = make({ snapshot: emptySnap() })
    await click(w, 'Create policy')
    await pick(w, 'Storage')
    await nextStep(w)
    await pick(w, 'Increment')
    expect(w.find('[role="note"]').text()).toContain('also need Read a key')
    expect(radio(w, 'Read a key').element.checked).toBe(false)
    await nextStep(w)
    await field(w, 'Key prefix').setValue('visits')
    await nextStep(w)
    expect(w.text()).toContain('Incomplete')
    await click(w, 'Add permission')
    const list = w.find('[aria-label="Allowed platform actions"]')
    expect(list.text()).toContain('visits')
    expect(list.text()).toContain('kv.incr')
    expect(list.findAll('li[title]').map((li) => li.attributes('title'))).toEqual(['kv.incr'])
    expect(list.find('[role="note"]').text()).toContain('also needs kv.get')
    expect(w.find('[aria-label="Unsaved permission changes"]').text()).toContain('1 unsaved change')
    expect(w.emitted('save')).toBeUndefined()
  })

  it('an incomplete step keeps Next disabled and says what is missing, with no change', async () => {
    const w = make()
    await enterEdit(w)
    await click(w, 'Add permission')
    const next = () => w.findAll('button').find((b) => b.text() === 'Next')
    expect(next().attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('Choose a service')
    expect(w.text()).toContain('Step 1 of 4')
    await pick(w, 'Storage')
    expect(next().attributes('disabled')).toBeUndefined()
    await nextStep(w)
    expect(next().attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('Choose at least one action')
    expect(w.text()).toContain('Step 2 of 4')
    await pick(w, 'Read a key')
    await nextStep(w)
    expect(w.text()).toContain('Step 3 of 4')
    expect(next().attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('Enter a key or prefix')
    // A literal that cannot be stored never becomes a draft entry.
    await field(w, 'Key prefix').setValue('x'.repeat(257))
    expect(next().attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('at most 256 characters')
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
  })

  it('accepting a caller is guided, local, validated and shown with its triggers', async () => {
    const w = make({ snapshot: makeSnapshot({ targets: [] }) })
    const id = '0190a1b2-c3d4-7e5f-9a6b-1c2d3e4f5a99'
    await enterEdit(w)
    await click(w, 'Who can call it')
    await click(w, 'Add caller')
    await field(w, 'Caller function ID').setValue(id.toUpperCase())
    await nextStep(w)
    expect(w.text()).toContain('Enter the caller function ID in lowercase.')
    await field(w, 'Caller function ID').setValue(id)
    await nextStep(w)
    await nextStep(w)
    expect(w.find('[role="alert"]').text()).toContain('Pick at least one')
    await pick(w, 'Background job')
    await nextStep(w)
    expect(w.text()).toContain('This is one half')
    await click(w, 'Add to policy')
    expect(w.find('[aria-label="Accepted callers"]').text()).toContain(id)
    await toReview(w)
    expect(w.find('#permissions-change-list').text()).toContain('Added')
  })

  describe('the guided path', () => {
    it('the default view has no forms; an empty policy offers one Create policy and default deny', () => {
      const w = make({ snapshot: emptySnap() })
      expect(has(w, 'Create policy')).toBe(true)
      expect(has(w, 'Edit permissions')).toBe(false)
      expect(w.findAll('input')).toHaveLength(0)
      expect(w.text()).toContain('denied by default')
      expect(w.text()).not.toMatch(/Effective permissions/i)
    })

    it('Create policy walks service, actions, resources, confirm, then Add', async () => {
      const w = make({ snapshot: emptySnap() })
      await click(w, 'Create policy')
      expect(w.text()).toContain('Step 1 of 4: Service')
      await pick(w, 'Storage')
      await nextStep(w)
      expect(w.text()).toContain('Step 2 of 4: Actions')
      await pick(w, 'Read a key')
      await pick(w, 'Write')
      await nextStep(w)
      expect(w.text()).toContain('Step 3 of 4: Resources')
      await field(w, 'Key prefix').setValue('state')
      await nextStep(w)
      expect(w.text()).toContain('Step 4 of 4: Confirm')
      expect(w.text()).toContain('Can read and write keys starting with \u201cstate\u201d in its own storage.')
      expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
      await click(w, 'Add permission')
      expect(w.find('[aria-label="Unsaved permission changes"]').text()).toContain('1 unsaved change')
      expect(w.findAll('li[title]').map((l) => l.attributes('title'))).toEqual(['kv.get', 'kv.put'])
      expect(w.emitted('save')).toBeUndefined()
    })

    it('Back keeps every choice, Cancel applies nothing', async () => {
      const w = make({ snapshot: emptySnap() })
      await click(w, 'Create policy')
      await pick(w, 'Storage')
      await nextStep(w)
      await pick(w, 'Read a key')
      await pick(w, 'List keys')
      await nextStep(w)
      expect(radioGroupChecked(w, 'Prefix')).toBe(true)
      await field(w, 'Key prefix').setValue('cfg/')
      await click(w, 'Back')
      expect(radio(w, 'Read a key').element.checked).toBe(true)
      expect(radio(w, 'List keys').element.checked).toBe(true)
      await nextStep(w)
      expect(field(w, 'Key prefix').element.value).toBe('cfg/')
      await click(w, 'Back')
      await click(w, 'Back')
      expect(radio(w, 'Storage').element.checked).toBe(true)
      await click(w, 'Cancel')
      expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
      expect(w.text()).toContain('Add permission')
    })

    it('listing keys never rewrites an exact key; the operator must choose a scope', async () => {
      const w = make({ snapshot: emptySnap() })
      await click(w, 'Create policy')
      await pick(w, 'Storage')
      await nextStep(w)
      await pick(w, 'List keys')
      await nextStep(w)
      // A new permission starts on Prefix. Exact key stays selectable and is refused with a reason.
      expect(radioGroupChecked(w, 'Prefix')).toBe(true)
      await click(w, 'Exact key')
      await field(w, 'Key').setValue('state')
      await nextStep(w)
      expect(w.text()).toContain('Step 3 of 4: Resources')
      expect(w.text()).toContain('List keys needs a prefix or every key')
      expect(btn(w, 'Next').attributes('disabled')).toBeDefined()
      await click(w, 'Every key')
      expect(w.text()).toContain('Broad:')
      await nextStep(w)
      expect(w.text()).toContain('including keys added later')
    })

    it('adding List to an existing exact-key permission keeps the key, key scope and every other action', async () => {
      const w = make()
      await enterEdit(w)
      await w.find('button[aria-label="Edit Own storage, one key state"]').trigger('click')
      await flushPromises()
      await pick(w, 'List keys')
      await nextStep(w)
      expect(radioGroupChecked(w, 'Exact key')).toBe(true)
      expect(field(w, 'Key').element.value).toBe('state')
      await nextStep(w)
      expect(w.text()).toContain('Step 3 of 4: Resources')
      expect(w.text()).toContain('List keys needs a prefix or every key')
      expect(btn(w, 'Next').attributes('disabled')).toBeDefined()
      expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
      // Going back keeps all three ticks; nothing was dropped or converted.
      await click(w, 'Back')
      for (const a of ['Read a key', 'Write', 'List keys']) expect(radio(w, a).element.checked).toBe(true)
      await radio(w, 'List keys').setValue(false)
      await nextStep(w)
      expect(radioGroupChecked(w, 'Exact key')).toBe(true)
      await nextStep(w)
      await click(w, 'Update permission')
      // Unchanged allowance: no change is reported.
      expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    })

    it('a blank prefix is not every key; only the explicit Every key choice allows all keys', async () => {
      const w = make({ snapshot: emptySnap() })
      await click(w, 'Create policy')
      await pick(w, 'Storage')
      await nextStep(w)
      await pick(w, 'List keys')
      await nextStep(w)
      expect(field(w, 'Key prefix').element.value).toBe('')
      await nextStep(w)
      expect(w.text()).toContain('Step 3 of 4: Resources')
      expect(w.text()).toContain('Enter a key or prefix')
      expect(btn(w, 'Next').attributes('disabled')).toBeDefined()
      expect(has(w, 'Add permission')).toBe(false)
      // Spaces and NUL are a real literal: never trimmed, never treated as blank.
      const literal = ' \u0000 '
      await field(w, 'Key prefix').setValue(literal)
      await nextStep(w)
      expect(w.text()).toContain('Step 4 of 4: Confirm')
      await click(w, 'Add permission')
      await toReview(w)
      await click(w, 'Save permissions')
      expect(w.emitted('save')[0][0].policy.allow[0].resource).toEqual({ own_kv: { prefix: literal } })
    })

    it('a stored empty prefix round-trips as Every key, and a blank schedule prefix is refused the same way', async () => {
      const stored = makeSnapshot({
        policy: { schema_version: 1, allow: [{ actions: ['kv.get', 'kv.list'], resource: { own_kv: { prefix: '' } }, context: 'execution' }], inbound: [] },
      })
      const w = make({ snapshot: stored })
      await enterEdit(w)
      await w.find('button[aria-label^="Edit"]').trigger('click')
      await flushPromises()
      await nextStep(w)
      expect(radioGroupChecked(w, 'Every key')).toBe(true)
      await nextStep(w)
      await click(w, 'Update permission')
      expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)

      const s = make({ snapshot: emptySnap() })
      await click(s, 'Create policy')
      await pick(s, 'Schedules')
      await nextStep(s)
      await pick(s, 'Create or update a schedule')
      await nextStep(s)
      await click(s, 'Name prefix')
      await nextStep(s)
      expect(s.text()).toContain('Enter a schedule name prefix')
      expect(btn(s, 'Next').attributes('disabled')).toBeDefined()
      expect(s.text()).toContain('Step 3 of 4')
    })

    it('choosing startup keeps ticked writes, names them, and blocks Next until the operator unticks them', async () => {
      const w = make({ snapshot: emptySnap() })
      await click(w, 'Create policy')
      await pick(w, 'Storage')
      await nextStep(w)
      await pick(w, 'Read a key')
      await pick(w, 'Write')
      await pick(w, 'At startup')
      expect(radio(w, 'Write').element.checked).toBe(true)
      expect(radio(w, 'Write').element.disabled).toBe(false)
      expect(w.text()).toContain('Not allowed at startup')
      await nextStep(w)
      expect(w.text()).toContain('Step 2 of 4: Actions')
      expect(btn(w, 'Next').attributes('disabled')).toBeDefined()
      expect(w.text()).toContain('Untick Write, or choose While handling a request')
      await radio(w, 'Write').setValue(false)
      // With it unticked, no new write can be added while startup is chosen.
      expect(radio(w, 'Write').element.disabled).toBe(true)
      expect(radio(w, 'Delete').element.disabled).toBe(true)
      await nextStep(w)
      expect(w.text()).toContain('Step 3 of 4: Resources')
    })

    it('startup access is not offered for other services', async () => {
      const w = make({ snapshot: emptySnap() })
      await click(w, 'Create policy')
      await pick(w, 'Tracing')
      await nextStep(w)
      expect(w.text()).not.toContain('At startup')
      await pick(w, 'Emit spans')
      await nextStep(w)
      expect(w.text()).toContain('nothing to choose')
      expect(w.find('select').exists()).toBe(false)
    })

    const chooseOtherCaller = async (w) => {
      await w.find('#wizard-caller').trigger('click')
      await flushPromises()
      ;[...document.body.querySelectorAll('[role="menuitemradio"]')].find((e) => e.textContent.includes(OTHER_FN_ID)).click()
      await flushPromises()
      await nextStep(w)
    }

    it('another function cannot be accepted as a schedule caller; this function can', async () => {
      const w = make()
      await enterEdit(w)
      await click(w, 'Who can call it')
      await click(w, 'Add caller')
      await chooseOtherCaller(w)
      expect(radio(w, 'Schedule').element.disabled).toBe(true)
      expect(w.text()).toContain('only when the caller is this function itself')
      await radio(w, 'Schedule').setValue(true)
      await nextStep(w)
      expect(w.text()).toContain('Step 2 of 3: Triggers')
      expect(has(w, 'Add to policy')).toBe(false)
      await pick(w, 'Background job')
      await nextStep(w)
      await click(w, 'Add to policy')
      expect(w.find('[aria-label="Accepted callers"]').text()).toContain(OTHER_FN_ID)

      const own = make({ snapshot: makeSnapshot({ targets: [] }) })
      await enterEdit(own)
      await click(own, 'Who can call it')
      await click(own, 'Add caller')
      await field(own, 'Caller function ID').setValue(FN_ID)
      await nextStep(own)
      expect(radio(own, 'Schedule').element.disabled).toBe(false)
      await pick(own, 'Schedule')
      await nextStep(own)
      await click(own, 'Add to policy')
      await toReview(own)
      await click(own, 'Save permissions')
      expect(own.emitted('save')[0][0].policy.inbound.at(-1)).toEqual({ caller_id: FN_ID, triggers: ['cron'] })
    })

    it('a stored foreign schedule acceptance is shown as unsupported, never as working, and cannot be re-saved by editing', async () => {
      const s = makeSnapshot()
      s.policy.inbound = [{ caller_id: CALLER_ID, triggers: ['cron'] }]
      const w = make({ snapshot: s })
      expect(w.text()).toContain('Not supported: schedule acceptance works only when the caller is this function itself')
      expect(w.text()).not.toContain('may start this function by a schedule')
      await enterEdit(w)
      await click(w, 'Who can call it')
      await w.find(`button[aria-label="Edit accepted caller ${CALLER_ID}"]`).trigger('click')
      await flushPromises()
      expect(radio(w, 'Schedule').element.checked).toBe(true)
      await nextStep(w)
      expect(w.text()).toContain('Step 2 of 3: Triggers')
      expect(w.find('[role="alert"]').text()).toContain("Schedule acceptance works only for this function's own schedules")
    })

    it('an in-place caller edit changes valid triggers only and preserves every other entry', async () => {
      const s = makeSnapshot()
      s.policy.inbound.push({ caller_id: OTHER_FN_ID, triggers: ['job'] })
      const w = make({ snapshot: s })
      await enterEdit(w)
      await click(w, 'Who can call it')
      await w.find(`button[aria-label="Edit accepted caller ${CALLER_ID}"]`).trigger('click')
      await flushPromises()
      await radio(w, 'Background job').setValue(false)
      await nextStep(w)
      await click(w, 'Update caller')
      await toReview(w)
      await click(w, 'Save permissions')
      const saved = w.emitted('save')[0][0].policy
      expect(saved.allow).toEqual(s.policy.allow)
      expect(saved.inbound).toEqual([
        { caller_id: CALLER_ID, triggers: ['f2f'] },
        { caller_id: OTHER_FN_ID, triggers: ['job'] },
      ])
    })

    it('adding an already accepted caller with supported triggers does not merge or change it', async () => {
      const w = make({ snapshot: makeSnapshot({ targets: [] }) })
      await enterEdit(w)
      await click(w, 'Who can call it')
      await click(w, 'Add caller')
      await field(w, 'Caller function ID').setValue(CALLER_ID)
      await nextStep(w)
      await pick(w, 'Function call')
      await nextStep(w)
      await click(w, 'Add to policy')
      expect(w.find('[role="alert"]').text()).toContain('already accepted')
      expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
      expect(w.emitted('save')).toBeUndefined()
      await click(w, 'Cancel')
      await click(w, 'Show advanced')
      expect(JSON.parse(w.find('pre').text()).inbound).toEqual(policy().inbound)
    })

    it('Save is refused unless the review screen is showing, even if the bar emits it', async () => {
      const w = make()
      await removeFirst(w)
      expect(has(w, 'Save permissions')).toBe(false)
      w.findComponent(SaveBar).vm.$emit('save')
      await flushPromises()
      expect(w.emitted('save')).toBeUndefined()
      await toReview(w)
      await click(w, 'Save permissions')
      expect(w.emitted('save')).toHaveLength(1)
    })

    it('an open discard question stays visible but cannot discard once saving starts', async () => {
      const w = make()
      await removeFirst(w)
      await btn(w, 'Discard').trigger('click')
      await w.setProps({ saving: true })
      expect(btn(w, 'Discard changes').attributes('disabled')).toBeDefined()
      expect(btn(w, 'Keep editing').attributes('disabled')).toBeDefined()
      w.findComponent(SaveBar).vm.keepEditing()
      w.findComponent(SaveBar).vm.confirm()
      await flushPromises()
      expect(has(w, 'Discard changes')).toBe(true)
      await btn(w, 'Discard changes').trigger('click')
      w.findComponent(SaveBar).vm.$emit('discard')
      await flushPromises()
      expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
      expect(w.text()).toContain('1 unsaved change')
    })

    it('Done is absent while a step is half-filled and closeEditor cannot clear it', async () => {
      const w = make()
      await enterEdit(w)
      expect(has(w, 'Done')).toBe(true)
      await click(w, 'Add permission')
      await pick(w, 'Storage')
      await nextStep(w)
      await pick(w, 'Read a key')
      expect(has(w, 'Done')).toBe(false)
      w.vm.closeEditor()
      await flushPromises()
      expect(w.text()).toContain('Step 2 of 4: Actions')
      expect(radio(w, 'Read a key').element.checked).toBe(true)
      await click(w, 'Cancel')
      expect(has(w, 'Done')).toBe(true)
      await click(w, 'Done')
      expect(has(w, 'Edit permissions')).toBe(true)
    })

    it('Review changes waits for an open step, so reviewing never drops half-filled work', async () => {
      const w = make()
      await removeFirst(w)
      await click(w, 'Add permission')
      expect(btn(w, 'Review changes').attributes('disabled')).toBeDefined()
      w.findComponent(SaveBar).vm.$emit('review')
      await flushPromises()
      expect(w.text()).toContain('Step 1 of 4')
      expect(w.find('#permissions-change-list').exists()).toBe(false)
    })

    it('quarantine, review and enforcement warnings are still shown while editing', async () => {
      const q = make({ snapshot: snap({ quarantined: true }) })
      await enterEdit(q)
      expect(q.text()).toContain('Retained, not in force.')
      expect(q.text()).toContain('Quarantined')
      expect(q.text()).toContain('the stored policy is not enforced. Draft changes stay unsaved')
      expect(q.text()).not.toContain('what is listed below is stored')
      expect(q.text()).not.toContain('Below is the policy stored')
      await removeFirst(q)
      await toReview(q)
      expect(q.text()).toContain('Quarantined')
      expect(q.text()).toContain('the stored policy is not enforced. Draft changes stay unsaved')
      const w = make()
      await removeFirst(w)
      await w.setProps({ snapshot: snap({ enforcement: 'inactive' }) })
      await flushPromises()
      expect(w.text()).toMatch(/enforcement is not enabled/)
      expect(w.text()).toContain('1 unsaved change')
    })

    it('a function target uses a typed ID when none is known, and schedules run this function only', async () => {
      const w = make({ snapshot: emptySnap({ targets: [] }) })
      await click(w, 'Create policy')
      await pick(w, 'Functions')
      await nextStep(w)
      await pick(w, 'Call')
      await nextStep(w)
      await field(w, 'Function ID').setValue(OTHER_FN_ID.toUpperCase())
      await nextStep(w)
      expect(w.text()).toContain('Enter the function UUID in lowercase.')
      await field(w, 'Function ID').setValue(OTHER_FN_ID)
      await nextStep(w)
      expect(w.text()).toContain('A call works only if the other function also accepts this one')
      await click(w, 'Add permission')
      expect(w.findAll('li[title]').map((l) => l.attributes('title'))).toEqual(['functions.invoke'])

      await click(w, 'Add permission')
      await pick(w, 'Schedules')
      await nextStep(w)
      await pick(w, 'Create or update a schedule')
      await nextStep(w)
      expect(w.text()).toContain(FN_ID)
      expect(field(w, 'Schedule name')).toBeTruthy()
      await field(w, 'Schedule name').setValue('nightly')
      await nextStep(w)
      await click(w, 'Add permission')
      await toReview(w)
      expect(w.find('#permissions-change-list').text()).toContain('Own schedule name nightly: crons.upsert')
    })

    it('Edit changes one permission in place and keeps every other entry', async () => {
      const w = make()
      await enterEdit(w)
      const before = w.find('[aria-label="Allowed platform actions"]').findAll('li[title]').length
      await w.findAll('button').find((b) => b.attributes('aria-label') === 'Edit Own storage, one key state').trigger('click')
      await flushPromises()
      expect(w.text()).toContain('Step 2 of 4: Actions')
      expect(radio(w, 'Read a key').element.checked).toBe(true)
      await pick(w, 'Delete')
      await nextStep(w)
      await nextStep(w)
      await click(w, 'Update permission')
      const list = w.find('[aria-label="Allowed platform actions"]')
      expect(list.findAll(':scope > li')).toHaveLength(3)
      expect(list.findAll('li[title]').length).toBe(before + 1)
      expect(list.text()).toContain('config/')
      expect(list.text()).toContain(OTHER_FN_ID)
      await toReview(w)
      const changes = w.find('#permissions-change-list').text()
      expect(changes).toContain('Removed')
      expect(changes).toContain('Added')
      expect(changes).toContain('kv.get, kv.put, kv.delete')
    })

    it('a duplicate permission is refused with a reason and nothing is added', async () => {
      const w = make()
      await enterEdit(w)
      await click(w, 'Add permission')
      await pick(w, 'Storage')
      await nextStep(w)
      await pick(w, 'Read a key')
      await pick(w, 'Write')
      await nextStep(w)
      await click(w, 'Exact key')
      await field(w, 'Key').setValue('state')
      await nextStep(w)
      await click(w, 'Add permission')
      expect(w.find('[role="alert"]').text()).toContain('already in the policy')
      expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    })

    it('Advanced shows the read-only canonical JSON, for the draft while editing', async () => {
      const w = make()
      const stored = w.find('pre').text()
      expect(JSON.parse(stored)).toEqual(policy())
      expect(stored).not.toMatch(/revision|capab|artifact|enforcement/i)
      expect(has(w, 'Copy JSON')).toBe(true)
      await removeFirst(w)
      const draftJson = JSON.parse(w.find('pre').text())
      expect(draftJson.allow).toHaveLength(2)
      expect(w.find('textarea').exists()).toBe(false)
    })

    it('the stored view changes only when a fresh snapshot arrives, never on Save', async () => {
      const w = make()
      await dirtyReview(w)
      await click(w, 'Save permissions')
      await w.setProps({ saving: true })
      await w.setProps({ saving: false })
      // Still reviewing: nothing was applied, and nothing pretends it was.
      expect(w.find('#permissions-change-list').exists()).toBe(true)
      expect(w.text()).not.toContain('Revision 8')
      const next = makeSnapshot({ host: { revision: '8' } })
      next.policy.allow.shift()
      await w.setProps({ snapshot: next })
      await flushPromises()
      expect(w.find('#permissions-change-list').exists()).toBe(false)
      expect(w.text()).toContain('Revision 8')
      expect(w.find('[aria-label="Allowed platform actions"]')).toBeTruthy()
      expect(w.findAll('li[title]')).toHaveLength(3)
      expect(has(w, 'Edit permissions')).toBe(true)
    })

    it('Save is reachable only from the review screen, and review shows the both-sides warning', async () => {
      const w = make({ snapshot: emptySnap({ targets: [] }) })
      await click(w, 'Create policy')
      await pick(w, 'Functions')
      await nextStep(w)
      await pick(w, 'Call')
      await nextStep(w)
      await field(w, 'Function ID').setValue(OTHER_FN_ID)
      await nextStep(w)
      await click(w, 'Add permission')
      expect(has(w, 'Save permissions')).toBe(false)
      await toReview(w)
      expect(has(w, 'Save permissions')).toBe(true)
      expect(w.text()).toContain('Calls between functions need both sides')
      await click(w, 'Save permissions')
      const [payload] = w.emitted('save')[0]
      expect(Object.keys(payload).sort()).toEqual(['expected', 'policy'])
    })

    it('each step moves focus to its heading', async () => {
      const w = make({ snapshot: emptySnap() })
      await click(w, 'Create policy')
      expect(document.activeElement.textContent).toContain('Step 1 of 4')
      await pick(w, 'Tracing')
      await nextStep(w)
      expect(document.activeElement.textContent).toContain('Step 2 of 4')
      await click(w, 'Back')
      expect(document.activeElement.textContent).toContain('Step 1 of 4')
    })

    it('a known function is chosen from a searchable list that carries its ID', async () => {
      const w = make({ snapshot: emptySnap() })
      await click(w, 'Create policy')
      await pick(w, 'Functions')
      await nextStep(w)
      await pick(w, 'Queue a job')
      await nextStep(w)
      // Unchosen: Next stays disabled with a reason, nothing is guessed.
      expect(btn(w, 'Next').attributes('disabled')).toBeDefined()
      expect(w.text()).toContain('Choose a function')
      await w.find('#wizard-function').trigger('click')
      await flushPromises()
      const options = [...document.body.querySelectorAll('[role="menuitemradio"]')]
      expect(options.map((o) => o.textContent.trim())).toEqual([`preview-renderer \u00b7 ${OTHER_FN_ID}`, 'Enter a function ID\u2026'])
      options[0].click()
      await flushPromises()
      await nextStep(w)
      expect(w.text()).toContain('Can queue jobs for preview-renderer.')
      await click(w, 'Add permission')
      expect(w.find('[aria-label="Allowed platform actions"]').text()).toContain('preview-renderer')
    })

    it('while data is unconfirmed the summary does not say None', () => {
      const w = make({ snapshot: emptySnap(), loading: true })
      const summary = w.find('[aria-label="Current access by service"]').text()
      expect(summary).toContain('Not confirmed')
      expect(summary).not.toContain('None')
    })

    it('revoking manage while a wizard is open closes it, and a new one cannot start', async () => {
      const w = make()
      await enterEdit(w)
      await click(w, 'Add permission')
      expect(w.text()).toContain('Step 1 of 4')
      await w.setProps({ snapshot: snap({ capabilities: caps({ canManage: false }) }) })
      await flushPromises()
      expect(w.text()).not.toContain('Step 1 of 4')
      expect(has(w, 'Add permission')).toBe(false)
      expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(false)
    })
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
    await dirtyReview(w)
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
    await dirtyReview(w)
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
      await dirtyReview(w)
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
    await enterEdit(w)
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
    await dirtyReview(w)
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('older than revision 8')
    await saveBtn(w).trigger('click')
    expect(w.emitted('save')).toBeUndefined()
    await w.setProps({ snapshot: rev(9) })
    await flushPromises()
    await btn(w, 'Discard draft and reload').trigger('click')
    await dirtyReview(w)
    expect(saveBtn(w).attributes('disabled')).toBeUndefined()
    expect(w.text()).not.toContain('older than revision')
  })

  it('discarding against an obsolete response does not lower the high-water mark', async () => {
    const w = make()
    await dirtyReview(w)
    await w.setProps({ snapshot: rev(8) })
    await w.setProps({ snapshot: rev(7) })
    await btn(w, 'Discard draft and reload').trigger('click')
    await dirtyReview(w)
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
    await dirtyReview(w)
    expect(saveBtn(w).attributes('disabled')).toBeUndefined()
  })

  it('a matching snapshot mid-save is not an acknowledgement, and a delayed conflict keeps the base', async () => {
    const w = make()
    await dirtyReview(w)
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
    await dirtyReview(w)
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
    await dirtyReview(w)
    await saveBtn(w).trigger('click')
    await w.setProps({ snapshot: matching(8) })
    await flushPromises()
    expect(w.find('[aria-label="Unsaved permission changes"]').exists()).toBe(true)
    expect(saveBtn(w).attributes('disabled')).toBeDefined()
  })
})
