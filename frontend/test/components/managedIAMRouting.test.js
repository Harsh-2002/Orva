import { describe, expect, it, vi } from 'vitest'
import { readManagedIAMCapability } from '@/api/managedPolicies.js'

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
})
