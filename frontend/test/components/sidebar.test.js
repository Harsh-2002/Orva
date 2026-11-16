import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import Sidebar from '@/components/layout/Sidebar.vue'

const sections = [
  ['/', 'Overview'], ['/ai', 'Chat'], ['/functions', 'Functions'],
  ['/cron', 'Schedules'], ['/jobs', 'Jobs'], ['/activity', 'Activity'],
  ['/invocations', 'Invocations'], ['/traces', 'Traces'], ['/api-keys', 'Keys'],
  ['/channels', 'Channels'], ['/webhooks', 'Webhooks'], ['/firewall', 'Egress'],
  ['/settings', 'Settings'], ['/docs', 'Docs'],
]
const mounted = []
const originalWidth = window.innerWidth
afterEach(() => {
  while (mounted.length) mounted.pop().unmount()
  window.innerWidth = originalWidth
})

async function make(path = '/') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: sections.map(([url]) => ({ path: url, component: { render: () => null } })),
  })
  await router.push(path)
  await router.isReady()
  const view = mount(Sidebar, { global: { plugins: [router] }, attachTo: document.body })
  mounted.push(view)
  return { view, router }
}

describe('flat sidebar navigation', () => {
  it('lists every existing section directly and in order without group controls', async () => {
    const { view } = await make()
    const links = view.findAll('nav > a')
    expect(links.map(link => [link.attributes('href'), link.text()])).toEqual(sections)
    expect(view.findAll('nav button')).toHaveLength(0)
    expect(view.findAll('[id^="nav-group-"]')).toHaveLength(0)
    for (const label of ['Automation', 'Observe', 'Connect']) expect(view.text()).not.toContain(label)
    expect(view.find('nav').classes()).toContain('overflow-y-auto')
    expect(view.find('nav').classes()).toContain('min-h-0')
  })

  it('preserves active routes and closes the mobile drawer when a section is chosen', async () => {
    const { view, router } = await make('/jobs')
    expect(view.find('a[href="/jobs"]').attributes('aria-current')).toBe('page')
    const toggle = view.find('button[aria-controls="primary-navigation"]')
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    await view.find('a[href="/traces"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/traces')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(view.find('a[href="/traces"]').attributes('aria-current')).toBe('page')
  })

  it('closes with Escape and restores the mobile menu toggle', async () => {
    window.innerWidth = 390
    const { view } = await make()
    const toggle = view.find('button[aria-controls="primary-navigation"]')
    await toggle.trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(view.find('nav a').element)
    document.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(toggle.element)
  })

  it('keeps keyboard focus in the open mobile drawer without trapping desktop navigation', async () => {
    window.innerWidth = 390
    const { view } = await make()
    const toggle = view.find('button[aria-controls="primary-navigation"]')
    await toggle.trigger('click')
    await flushPromises()
    const last = view.find('a[href="/docs"]')
    last.element.focus()
    const tab = new window.KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    document.dispatchEvent(tab)
    expect(tab.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(toggle.element)
    document.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(last.element)
    window.innerWidth = 1440
    const desktopTab = new window.KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    document.dispatchEvent(desktopTab)
    expect(desktopTab.defaultPrevented).toBe(false)
  })
})
