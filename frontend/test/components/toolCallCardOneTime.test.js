import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ToolCallCard from '@/components/ai/ToolCallCard.vue'

const SECRET = 'live-secret-0123456789abcdef'
const PLACEHOLDER = '[redacted: shown once to the operator]'
const base = { id: 't1', name: 'create_api_key', group: 'keys', args: { name: 'ci' }, status: 'succeeded' }

describe('one-time credential in a tool result card', () => {
  it('shows the live value open with a copy-now note and keeps it open after success', async () => {
    const view = mount(ToolCallCard, { props: { tool: { ...base, status: 'running', result: null } } })
    await view.setProps({ tool: { ...base, result: { key: SECRET }, oneTime: true } })
    expect(view.text()).toContain(SECRET)
    const note = view.get('[data-testid="one-time-note"]')
    expect(note.text()).toMatch(/shown only once/)
    expect(note.text()).toMatch(/not stored/)
    expect(view.find('[data-testid="redacted-note"]').exists()).toBe(false)
    expect(view.get('button[aria-expanded]').attributes('aria-expanded')).toBe('true')
  })

  it('shows only the placeholder and a redacted note after a reload', async () => {
    const view = mount(ToolCallCard, { props: { tool: { ...base, result: { key: PLACEHOLDER } } } })
    await view.get('button[aria-expanded]').trigger('click')
    expect(view.text()).not.toContain(SECRET)
    expect(view.text()).toContain(PLACEHOLDER)
    expect(view.get('[data-testid="redacted-note"]').text()).toMatch(/not stored/)
    expect(view.find('[data-testid="one-time-note"]').exists()).toBe(false)
  })

  it('adds no note to an ordinary result', async () => {
    const view = mount(ToolCallCard, { props: { tool: { ...base, name: 'list_functions', result: { items: [] } } } })
    await view.get('button[aria-expanded]').trigger('click')
    expect(view.find('[data-testid="one-time-note"]').exists()).toBe(false)
    expect(view.find('[data-testid="redacted-note"]').exists()).toBe(false)
  })
})
