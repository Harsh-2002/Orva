import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { readManagedIAMCapability } from '@/api/managedPolicies.js'
import client from '@/api/client.js'
import router from '@/router/index.js'
import { useAuthStore } from '@/stores/auth'

// Only the transport is stubbed: the real readManagedIAMCapability and
// listManagedIAMFunctions run, so paging and id validation are covered too.
vi.mock('@/api/client', async (importOriginal) => {
  const actual = await importOriginal()
  return { ...actual, default: { ...actual.default, request: vi.fn() } }
})

const PAYMENTS = { id: '11111111-1111-4111-8111-111111111111', name: 'payments' }
const LEDGER = { id: '22222222-2222-4222-8222-222222222222', name: 'ledger' }
const EXISTING = '33333333-3333-4333-8333-333333333333'

const respond = ({ managed = true, functions = [PAYMENTS, LEDGER] } = {}) => {
  client.request.mockImplementation(async ({ url }) => {
    if (url === '/api/v1/iam/capabilities') return { data: { managed } }
    if (url === '/api/v1/functions') return { data: { functions, total: functions.length } }
    throw new Error(`Unexpected request ${url}`)
  })
}

const at = async (path) => {
  await router.push(path)
  await flushPromises()
  return router.currentRoute.value
}

const lookups = () => client.request.mock.calls.filter(([config]) => config.url === '/api/v1/functions')

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  const auth = useAuthStore()
  auth.hasUser = true
  auth.isAuthenticated = true
})

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

describe('function permissions route', () => {
  it('resolves a function name to its id, replacing the URL', async () => {
    respond()

    const route = await at('/functions/payments/permissions?tab=access')

    expect(route.name).toBe('function-permissions')
    expect(route.params.functionId).toBe(PAYMENTS.id)
    expect(route.path).toBe(`/functions/${PAYMENTS.id}/permissions`)
    expect(route.query).toEqual({ tab: 'access' })
    // A shared link must not leave a second entry behind it: Back belongs to
    // wherever the operator came from, not to the name spelling of this page.
    expect(route.redirectedFrom.fullPath).toBe('/functions/payments/permissions?tab=access')
  })

  it('sends an unknown name to the not-found page', async () => {
    respond()
    const route = await at('/functions/ghost/permissions')
    expect(route.name).toBe('not-found')
    expect(route.path).toBe('/functions/ghost/permissions')
    expect(lookups()).not.toHaveLength(0)
  })

  it('leaves a canonical id alone and looks nothing up', async () => {
    respond()
    const route = await at(`/functions/${EXISTING}/permissions`)
    expect(route.name).toBe('function-permissions')
    expect(route.params.functionId).toBe(EXISTING)
    expect(route.redirectedFrom).toBeUndefined()
    expect(lookups()).toHaveLength(0)
  })

  it('sends an id-shaped path it cannot verify to not-found on lookup failure', async () => {
    client.request.mockImplementation(async ({ url }) => {
      if (url === '/api/v1/iam/capabilities') return { data: { managed: true } }
      throw { status: 500 }
    })
    const route = await at('/functions/ledger/permissions')
    expect(route.name).toBe('not-found')
  })

  it('refuses the route before any lookup when managed IAM is absent', async () => {
    respond({ managed: false })
    const route = await at('/functions/payments/permissions')
    expect(route.name).toBe('not-found')
    expect(lookups()).toHaveLength(0)
  })
})
