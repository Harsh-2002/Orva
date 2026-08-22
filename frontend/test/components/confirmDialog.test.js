import { describe, expect, it, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { readFileSync } from 'node:fs'
import { nextTick } from 'vue'
import ConfirmDialog from '../../src/components/common/ConfirmDialog.vue'
import { useConfirmStore } from '../../src/stores/confirm.js'

// Regression guard for a confirmed data-loss bug.
//
// ConfirmDialog used to bind Enter on `window` and call settle(true) whenever
// the dialog was open and not in prompt mode. The focus trap puts initial focus
// on the first focusable descendant, which is Cancel, so pressing Enter on
// Cancel bubbled a keydown to window, settled true, and ran the destructive
// action. Cancel's own settle(false) then did nothing, because the store nulls
// its resolver on the first settle.
//
// It was reproduced in a real browser against a live instance: opening the
// delete confirm on a function, pressing Enter with focus on Cancel, and
// watching the function disappear. Every destructive path in the product routes
// through this one component, so the blast radius was delete function, bulk
// delete, revoke API key, delete firewall rule, revoke session, revoke
// connected app, truncate an AI conversation, and restore-from-backup.
//
// Enter needs no handler: it already activates whichever button holds focus.

describe('ConfirmDialog keyboard handling', () => {
  let wrapper

  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    wrapper?.unmount()
  })

  const openDialog = async (opts = {}) => {
    wrapper = mount(ConfirmDialog, { attachTo: document.body })
    const confirm = useConfirmStore()
    const promise = confirm.ask({
      title: 'Delete "payments-webhook-handler-1"?',
      message: 'This removes the function and its deployments.',
      confirmLabel: 'Delete',
      danger: true,
      ...opts,
    })
    await nextTick()
    await nextTick()
    return { confirm, promise }
  }

  it('does not confirm when Enter is pressed while Cancel holds focus', async () => {
    const { promise } = await openDialog()

    // Exactly what the browser does: a keydown that bubbles up to window.
    window.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    await nextTick()

    // Observe settlement with a flag rather than Promise.race against an
    // already-resolved sentinel, which always wins the race and made an earlier
    // version of this test pass against the defect it was written to catch.
    let settledWith = 'pending'
    promise.then((v) => { settledWith = v })
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    expect(settledWith, 'Enter on Cancel must not confirm').toBe('pending')
  })

  it('still cancels on Escape', async () => {
    const { promise } = await openDialog()
    window.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await expect(promise).resolves.toBe(false)
  })

  it('still confirms when the confirm button is actually activated', async () => {
    const { promise } = await openDialog()
    // The dialog teleports to <body>, so it is not inside the wrapper.
    const buttons = [...document.body.querySelectorAll('button')]
    const confirmBtn = buttons.find((b) => b.textContent.trim() === 'Delete')
    expect(confirmBtn, 'confirm button should render').toBeTruthy()
    confirmBtn.click()
    await expect(promise).resolves.toBe(true)
  })

  it('has no window-level Enter branch in its key handler', () => {
    // Belt and braces against the exact shape of the original defect, so a
    // refactor cannot reintroduce it behind a different control flow and still
    // pass the behavioural tests above (for instance by settling on keyup).
    // vitest runs with the frontend package root as cwd, and import.meta.url
    // is not a file: URL under the jsdom transform.
    const src = readFileSync('src/components/common/ConfirmDialog.vue', 'utf8')
    const handler = src.slice(src.indexOf('const onKey'), src.indexOf('onMounted('))
    expect(handler).not.toMatch(/e\.key === 'Enter'[\s\S]{0,80}settle\(true\)/)
  })
})
