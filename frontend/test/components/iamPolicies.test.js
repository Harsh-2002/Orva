import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IAMPolicies from '@/views/IAMPolicies.vue'
import IAMPolicyDetail from '@/views/IAMPolicyDetail.vue'
import Button from '@/components/common/Button.vue'

const wrappers = []
afterEach(() => { while (wrappers.length) wrappers.pop().unmount() })

const mountView = (component, props) => {
  const wrapper = mount(component, { props })
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
    await view.findAll('button').find((b) => b.text() === 'Review attachment').trigger('click')
    await flushPromises()
    expect(view.text()).toContain('Enter a valid existing version number.')
    expect(mutate).not.toHaveBeenCalled()
  })
})
