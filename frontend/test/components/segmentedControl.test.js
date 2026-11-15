import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import SegmentedControl from '@/components/common/SegmentedControl.vue'

const options = [
  { value: 'view', label: 'View' },
  { value: 'edit', label: 'Edit' },
  { value: 'diff', label: 'Diff', disabled: true },
]
const make = (modelValue = 'view') => mount(SegmentedControl, {
  props: { modelValue, options, ariaLabel: 'Mode' },
  attachTo: document.body,
})

describe('SegmentedControl', () => {
  it('is a labelled radio group with one checked option and real buttons', () => {
    const w = make('edit')
    expect(w.attributes('role')).toBe('radiogroup')
    expect(w.attributes('aria-label')).toBe('Mode')
    const radios = w.findAll('button[role="radio"]')
    expect(radios.map((r) => r.attributes('aria-checked'))).toEqual(['false', 'true', 'false'])
    expect(radios.every((r) => r.attributes('type') === 'button')).toBe(true)
    w.unmount()
  })

  it('only the selected option is in the tab order', () => {
    const w = make('edit')
    expect(w.findAll('button').map((b) => b.attributes('tabindex'))).toEqual(['-1', '0', '-1'])
    w.unmount()
  })

  it('emits the clicked value, and nothing for the current or a disabled option', async () => {
    const w = make('view')
    await w.findAll('button')[1].trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([['edit']])
    await w.findAll('button')[0].trigger('click')
    await w.findAll('button')[2].trigger('click')
    expect(w.emitted('update:modelValue')).toHaveLength(1)
    w.unmount()
  })

  it('arrow keys move the selection, skipping disabled options, and wrap', async () => {
    const w = make('view')
    await w.findAll('button')[0].trigger('keydown', { key: 'ArrowRight' })
    expect(w.emitted('update:modelValue')[0]).toEqual(['edit'])
    await w.setProps({ modelValue: 'edit' })
    await w.findAll('button')[1].trigger('keydown', { key: 'ArrowRight' }) // 'diff' is disabled -> wraps to 'view'
    expect(w.emitted('update:modelValue')[1]).toEqual(['view'])
    await w.findAll('button')[1].trigger('keydown', { key: 'Home' })
    expect(w.emitted('update:modelValue')[2]).toEqual(['view'])
    w.unmount()
  })
})
