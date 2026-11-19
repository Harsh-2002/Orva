import { describe, expect, it, vi } from 'vitest'
import { readManagedIAMCapability, readManagedIAMMode } from '@/api/managedPolicies.js'

describe('managed IAM capability', () => {
  it.each([[true, true], [false, false], ['true', false], [1, false], [undefined, false]])(
    'accepts only literal true (%s)',
    async (managed, allowed) => {
      const request = vi.fn().mockResolvedValue({ managed })
      expect(await readManagedIAMCapability(request)).toBe(allowed)
      expect(request).toHaveBeenCalledWith('/api/v1/iam/capabilities', expect.objectContaining({
        quietStatuses: expect.arrayContaining([404]),
      }))
    },
  )

  it.each([401, 403, 404, 500, undefined])('fails closed on a rejected request (%s)', async (status) => {
    const request = vi.fn().mockRejectedValue({ status })
    expect(await readManagedIAMCapability(request)).toBe(false)
  })

  it.each([[true, 'managed'], [false, 'legacy'], ['false', 'unknown'], [undefined, 'unknown']])(
    'distinguishes an explicit mode from an invalid response (%s)',
    async (managed, expected) => {
      expect(await readManagedIAMMode(async () => ({ managed }))).toBe(expected)
    },
  )

  it.each([[404, 'legacy'], [401, 'unknown'], [403, 'unknown'], [500, 'unknown'], [undefined, 'unknown']])(
    'does not infer legacy from probe failures (%s)',
    async (status, expected) => {
      expect(await readManagedIAMMode(vi.fn().mockRejectedValue({ status }))).toBe(expected)
    },
  )
})
