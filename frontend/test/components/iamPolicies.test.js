import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IAMPolicies from '@/views/IAMPolicies.vue'
import IAMPolicyDetail from '@/views/IAMPolicyDetail.vue'
import Button from '@/components/common/Button.vue'
import { copyText } from '@/utils/clipboard'

vi.mock('@/utils/clipboard', () => ({ copyText: vi.fn(async () => true) }))

const wrappers = []
afterEach(() => { while (wrappers.length) wrappers.pop().unmount() })

const mountView = (component, props) => {
  const wrapper = mount(component, {
    props,
    global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } },
  })
  wrappers.push(wrapper)
  return wrapper
}

const listApi = (items, overrides = {}) => ({
  list: vi.fn(async () => ({ items, can_manage: true })),
  get: vi.fn(async () => ({ policy: { name: items[0]?.name ?? 'reader', latest_version: 2, revoked: false, definition_revision: '10', membership_revision: '4' }, version: { policy: { allow: [] } }, attachments: [], can_manage: true })),
  ...overrides,
})

const detailResponse = (latest = 2) => ({
  policy: { name: 'reader', latest_version: latest, revoked: false, definition_revision: '10', membership_revision: '4' },
  version: { policy: { allow: [] } },
  attachments: [],
  can_manage: true,
})

describe('managed policy list rows', () => {
  it('names the entry control New policy and never reuses that text for saving', async () => {
    const view = mountView(IAMPolicies, { policyId: '', api: listApi([{ id: 'p1', name: 'reader-node', latest_version: 2, revoked: false }]), functions: [] })
    await flushPromises()
    const labels = view.findAll('button').map((b) => b.text())
    expect(labels).toContain('New policy')
    expect(labels).not.toContain('Create reusable policy')
  })

  it('aligns policy names with the row padding instead of a padded ghost button', async () => {
    const view = mountView(IAMPolicies, { policyId: '', api: listApi([{ id: 'p1', name: 'reader-node', latest_version: 2, revoked: false }]), functions: [] })
    await flushPromises()
    const row = view.find('li')
    expect(row.classes()).toContain('px-4')
    expect(row.classes()).toContain('py-3')
    expect(row.classes()).not.toContain('p-4')
    const name = row.find('button')
    expect(name.exists()).toBe(true)
    expect(name.classes()).toContain('text-foreground-strong')
    expect(row.findComponent(Button).exists()).toBe(false)
  })

  it('navigates from the whole row, and a click on the name selects it exactly once', async () => {
    const view = mountView(IAMPolicies, { policyId: '', api: listApi([{ id: 'p1', name: 'reader-node', latest_version: 2, revoked: false }]), functions: [] })
    await flushPromises()
    await view.find('li').trigger('click')
    expect(view.emitted('select')).toEqual([['p1']])
    await view.find('li button').trigger('click')
    expect(view.emitted('select')).toEqual([['p1'], ['p1']])
  })
})

describe('policy detail header', () => {
  it('keeps Refresh policy beside the title instead of floating above it', async () => {
    const view = mountView(IAMPolicyDetail, { policyId: 'p1', api: listApi([], { get: vi.fn(async () => detailResponse()) }), functions: [] })
    await flushPromises()
    const header = view.find('header')
    expect(header.classes()).toContain('justify-between')
    const children = header.element.children
    expect(children[0].tagName).toBe('DIV')
    expect(children[0].querySelector('h2').textContent.trim()).toBe('reader')
    expect(children[children.length - 1].textContent.trim()).toBe('Refresh policy')
    const refresh = header.findAll('button').find((b) => b.text() === 'Refresh policy')
    expect(refresh.exists()).toBe(true)
  })
})

describe('policy identity and versions', () => {
  const detail = () => mountView(IAMPolicyDetail, { policyId: 'p1', api: listApi([], { get: vi.fn(async () => detailResponse()) }), functions: [] })

  it('keeps the raw policy id behind a copy control instead of under the title', async () => {
    const view = detail()
    await flushPromises()
    expect(view.find('header').text()).not.toContain('p1')
    const identity = view.findAll('details').find((d) => d.find('summary').text() === 'Policy ID')
    expect(identity.exists()).toBe(true)
    expect(identity.text()).toContain('p1')
    await identity.find('button').trigger('click')
    await flushPromises()
    expect(copyText).toHaveBeenCalledWith('p1')
    expect(identity.find('button').text()).toBe('Copied')
  })

  it('collapses the pin and rollout explanation into a Versions detail', async () => {
    const view = detail()
    await flushPromises()
    expect(view.find('header').text()).not.toContain('Latest verified version')
    const versions = view.findAll('details').find((d) => d.find('summary').text() === 'Versions')
    expect(versions.exists()).toBe(true)
    expect(versions.text()).toContain('Latest verified version 2')
    expect(versions.text()).toContain('keep their reviewed version until explicit rollout')
  })
})

describe('attach-version group', () => {
  it('draws the section rule above its heading without a legend eating the border', async () => {
    const view = mountView(IAMPolicyDetail, { policyId: 'p1', api: listApi([], { get: vi.fn(async () => detailResponse()) }), functions: [{ id: 'f1', name: 'fn' }] })
    await flushPromises()
    expect(view.find('legend').exists()).toBe(false)
    const heading = view.findAll('h3').find((h) => h.text() === 'Attach or roll out a version')
    expect(heading.exists()).toBe(true)
    expect(heading.element.parentElement.className).toContain('border-t')
    const group = view.find('fieldset[aria-label="Attach or roll out a version"]')
    expect(group.exists()).toBe(true)
  })
})

describe('policy detail action groups', () => {
  it('separates the destructive actions from Attach with a rule', async () => {
    const view = mountView(IAMPolicyDetail, { policyId: 'p1', api: listApi([], { get: vi.fn(async () => detailResponse()) }), functions: [{ id: 'f1', name: 'fn' }] })
    await flushPromises()
    const attach = view.findAll('button').find((b) => b.text() === 'Attach…')
    const revoke = view.findAll('button').find((b) => b.text() === 'Revoke everywhere…')
    const group = revoke.element.closest('div')
    expect(group.className).toContain('border-t')
    expect(group.contains(attach.element)).toBe(false)
    expect(group.textContent).toContain('Delete…')
  })
})

const FN_A = '11111111-1111-4111-8111-111111111111'
const FN_B = '22222222-2222-4222-8222-222222222222'
const FUNCTIONS = [{ id: FN_A, name: 'hello-node' }, { id: FN_B, name: 'hello-python' }]
const attachApi = (attachments = []) => ({
  ...listApi([{ id: 'p1', name: 'reader-node', latest_version: 2, revoked: false }]),
  get: vi.fn(async () => ({ ...detailResponse(), attachments })),
  permissions: vi.fn(async (id) => ({ function: { id }, host: { revision: '7' } })),
  mutate: vi.fn(async () => ({ ok: true })),
})
const attachedLine = (view) => view.findAll('p')
  .map((p) => p.text().replace(/\s+/g, ' ').trim())
  .find((text) => text.startsWith('Attached to'))

describe('attach feedback and row status', () => {
  it('shows why Attach… is unavailable until a function is selected', async () => {
    const view = mountView(IAMPolicyDetail, { policyId: 'p1', api: attachApi(), functions: FUNCTIONS })
    await flushPromises()
    const attach = () => view.findAll('button').find((b) => b.text() === 'Attach…')
    expect(attach().attributes('disabled')).toBeDefined()
    expect(view.text()).toContain('Select at least one function.')
    await view.findAll('input[type="checkbox"]')[0].setValue(true)
    expect(view.text()).not.toContain('Select at least one function.')
    expect(attach().attributes('disabled')).toBeUndefined()
  })

  it('links the attached function to its Permissions page and clears the selection', async () => {
    const view = mountView(IAMPolicyDetail, { policyId: 'p1', api: attachApi(), functions: FUNCTIONS })
    await flushPromises()
    await view.findAll('input[type="checkbox"]')[0].setValue(true)
    await view.findAll('button').find((b) => b.text() === 'Attach…').trigger('click')
    await flushPromises()
    await view.findAll('button').find((b) => b.text() === 'Attach reviewed version').trigger('click')
    await flushPromises()
    expect(attachedLine(view)).toBe('Attached to hello-node. Approve it on its Permissions page to make it active.')
    expect(view.findAll('a').find((a) => a.text() === 'hello-node').attributes('href')).toBe(`/functions/${FN_A}/permissions`)
    expect(view.find('input[type="checkbox"]').element.checked).toBe(false)
  })

  it('names and links every function when more than one is attached', async () => {
    const view = mountView(IAMPolicyDetail, { policyId: 'p1', api: attachApi(), functions: FUNCTIONS })
    await flushPromises()
    for (const box of view.findAll('input[type="checkbox"]')) await box.setValue(true)
    await view.findAll('button').find((b) => b.text() === 'Attach…').trigger('click')
    await flushPromises()
    await view.findAll('button').find((b) => b.text() === 'Attach reviewed version').trigger('click')
    await flushPromises()
    expect(attachedLine(view)).toBe('Attached to hello-node, hello-python. Approve each on its Permissions page to make them active.')
    const hrefs = view.findAll('a').map((a) => a.attributes('href'))
    expect(hrefs).toEqual([`/functions/${FN_A}/permissions`, `/functions/${FN_B}/permissions`])
  })

  it('replaces the stale row status with the verified attachment count', async () => {
    const view = mountView(IAMPolicies, { policyId: 'p1', api: attachApi([{ owner_id: FN_A, version: '2', enabled: true }]), functions: FUNCTIONS })
    await flushPromises()
    const row = view.findAll('ul')[0]
    expect(row.text()).toContain('Attached to 1 function')
    expect(row.text()).not.toContain('Available to attach')
  })

  it('counts every attached function on the row', async () => {
    const view = mountView(IAMPolicies, { policyId: 'p1', api: attachApi([{ owner_id: FN_A, version: '2', enabled: true }, { owner_id: FN_B, version: '2', enabled: true }]), functions: FUNCTIONS })
    await flushPromises()
    expect(view.findAll('ul')[0].text()).toContain('Attached to 2 functions')
  })
})

describe('pinned version prefill', () => {
  const detail = (get) => mountView(IAMPolicyDetail, { policyId: 'p1', api: listApi([], { get }), functions: [{ id: 'f1', name: 'fn' }] })
  const pinnedInput = (view) => view.findAll('input').find((i) => i.element.type !== 'checkbox')

  it('prefills the latest verified version and follows it until the operator edits the field', async () => {
    let latest = 2
    const view = detail(vi.fn(async () => detailResponse(latest)))
    await flushPromises()
    expect(pinnedInput(view).element.value).toBe('2')
    latest = 3
    const refresh = view.findAll('button').find((b) => b.text() === 'Refresh policy')
    await refresh.trigger('click')
    await flushPromises()
    expect(pinnedInput(view).element.value).toBe('3')
    await pinnedInput(view).setValue('5')
    latest = 4
    await refresh.trigger('click')
    await flushPromises()
    expect(pinnedInput(view).element.value).toBe('5')
  })

  it('still refuses a pinned version that is not an existing version number', async () => {
    const mutate = vi.fn()
    const view = detail(vi.fn(async () => detailResponse()))
    await flushPromises()
    await pinnedInput(view).setValue('abc')
    await view.find('input[type="checkbox"]').setValue(true)
    await view.findAll('button').find((b) => b.text() === 'Attach…').trigger('click')
    await flushPromises()
    expect(view.text()).toContain('Enter a valid existing version number.')
    expect(mutate).not.toHaveBeenCalled()
  })
})
