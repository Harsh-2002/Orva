import { mount } from '@vue/test-utils'
import { h, nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Popover from '@/components/common/Popover.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'

let wrapper
let mobile

beforeEach(() => {
  mobile = false
  vi.stubGlobal('matchMedia', vi.fn(() => ({
    matches: mobile,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })))
})

afterEach(() => {
  wrapper?.unmount()
  document.body.innerHTML = ''
  vi.unstubAllGlobals()
})

function make(content = () => h('button', { id: 'entry' }, 'Entry')) {
  wrapper = mount(Popover, {
    attachTo: document.body,
    slots: {
      trigger: ({ toggle, open }) => h('button', {
        id: 'trigger', 'aria-expanded': String(open), onClick: toggle,
      }, 'Open'),
      default: content,
    },
  })
  return wrapper.get('#trigger')
}

async function activate(trigger, detail = 0) {
  trigger.element.focus()
  // jsdom does not synthesize native button clicks from Enter/Space.
  trigger.element.dispatchEvent(new window.MouseEvent('click', { bubbles: true, detail }))
  await nextTick()
  await nextTick()
}

function escape(el) {
  const event = new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
  el.dispatchEvent(event)
  return event
}

describe('Popover keyboard focus', () => {
  it('enters the first enabled control on keyboard activation', async () => {
    const trigger = make(() => [
      h('button', { disabled: true }, 'Disabled'),
      h('input', { id: 'search', 'aria-label': 'Search' }),
      h('button', { id: 'entry' }, 'Entry'),
    ])
    await activate(trigger)
    expect(document.activeElement.id).toBe('search')
  })

  it('does not move focus on pointer activation', async () => {
    const trigger = make()
    await activate(trigger, 1)
    expect(document.querySelector('[role="menu"]')).not.toBeNull()
    expect(document.activeElement).toBe(trigger.element)
  })

  it.each(['trigger', 'entry'])('Escape from %s closes and restores trigger focus', async (id) => {
    const trigger = make()
    await activate(trigger)
    document.getElementById(id).focus()
    expect(escape(document.activeElement).defaultPrevented).toBe(true)
    await nextTick()
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(document.querySelector('[role="menu"]')).toBeNull()
    expect(document.activeElement).toBe(trigger.element)
  })

  it.each(['menuitem', 'menuitemradio', 'menuitemcheckbox'])('skips disabled and hidden entries before %s', async (role) => {
    const trigger = make(() => [
      h('input', { disabled: true }),
      h('button', { 'aria-disabled': 'true' }, 'Unavailable'),
      h('fieldset', { disabled: true }, [h('button', {}, 'Disabled by fieldset')]),
      h('div', { hidden: true }, [h('button', {}, 'Hidden')]),
      h('div', { inert: true }, [h('button', {}, 'Inert')]),
      h('div', { role, tabindex: -1, id: 'entry' }, 'Entry'),
    ])
    await activate(trigger)
    expect(document.activeElement.id).toBe('entry')
  })

  it('leaves focus on the trigger when no enabled entry exists', async () => {
    const trigger = make(() => h('button', { disabled: true }, 'Disabled'))
    await activate(trigger)
    expect(document.activeElement).toBe(trigger.element)
    escape(trigger.element)
    await nextTick()
    expect(trigger.attributes('aria-expanded')).toBe('false')
  })

  it('outside pointer dismissal never returns focus to the trigger', async () => {
    const trigger = make()
    await activate(trigger)
    const outside = document.createElement('button')
    document.body.append(outside)
    const focus = vi.spyOn(trigger.element, 'focus')
    outside.dispatchEvent(new window.Event('pointerdown', { bubbles: true }))
    // Native pointerdown's default action focuses the clicked control.
    outside.focus()
    await nextTick()
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(focus).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(outside)
  })

  it('does not enter a panel after an outside dismissal before the render tick', async () => {
    const trigger = make()
    trigger.element.focus()
    trigger.element.dispatchEvent(new window.MouseEvent('click', { bubbles: true, detail: 0 }))
    document.body.dispatchEvent(new window.Event('pointerdown', { bubbles: true }))
    await nextTick()
    expect(document.querySelector('[role="menu"]')).toBeNull()
    expect(document.activeElement).toBe(trigger.element)
  })

  it('leaves mobile focus and Escape handling to Drawer', async () => {
    mobile = true
    const trigger = make()
    await nextTick()
    await activate(trigger)
    const dialog = document.querySelector('[role="dialog"]')
    expect(document.querySelector('[role="menu"]')).toBeNull()
    expect(document.activeElement).toBe(dialog.querySelector('[aria-label="Close"]'))
    escape(document.activeElement)
    await nextTick()
    await nextTick()
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(trigger.element)
  })

  it.each([false, true])('FilterSelect keyboard entry works with search=%s', async (search) => {
    wrapper = mount(FilterSelect, {
      attachTo: document.body,
      props: {
        label: 'Status',
        options: Array.from({ length: search ? 10 : 3 }, (_, i) => ({
          value: i, label: `Option ${i}`, disabled: i === 0,
        })),
      },
    })
    const trigger = wrapper.get('button[aria-haspopup="menu"]')
    await activate(trigger)
    if (search) expect(document.activeElement.getAttribute('aria-label')).toBe('Filter status')
    else expect(document.activeElement.textContent).toContain('Option 1')
    escape(document.activeElement)
    await nextTick()
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(trigger.element)
  })
})
