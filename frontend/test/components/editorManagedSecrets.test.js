import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

// Managed mode replaces the legacy secrets CRUD with the CAS-checked
// configuration service, so the editor must write there with tokens read
// fresh for each write. Legacy mode stays byte-identical.

window.matchMedia = window.matchMedia || ((q) => ({
  matches: false, media: q, onchange: null, dispatchEvent: () => false,
  addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
}))

const FN = {
  id: 'fn-a', name: 'alpha', description: '', runtime: 'python', entrypoint: 'handler.py',
  memory_mb: 64, cpus: 0.5, code_hash: 'ha', timeout_ms: 5000, network_mode: 'none', sdk: false,
  max_concurrency: 0, concurrency_policy: 'queue', auth_mode: 'none', rate_limit_per_min: 0,
  env_vars: { EXISTING: 'keep' },
}
const ARTIFACT = 'a'.repeat(64)

const h = vi.hoisted(() => ({
  managed: false,
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  configuration: null,
}))
vi.mock('@/api/managedPolicies.js', () => ({
  readManagedIAMCapability: async () => h.managed,
}))
vi.mock('@/api/client', () => ({
  default: { get: h.get, post: h.post, put: h.put, delete: h.del },
  getApiKey: () => 'k',
}))
vi.mock('@/api/endpoints', () => ({
  __esModule: true,
  listFunctions: vi.fn(async () => ({ data: { functions: [FN], total: 1 } })),
  listDeployments: vi.fn(async () => ({ data: { deployments: [] } })),
  rollbackFunction: vi.fn(async () => ({ data: {} })),
  listFixtures: vi.fn(async () => ({ data: { fixtures: [] } })),
  updateFixture: vi.fn(async () => ({ data: {} })),
  deleteFixture: vi.fn(async () => ({ data: {} })),
  invokeFunctionFull: vi.fn(async () => ({ data: {} })),
  getInvocationLogs: vi.fn(async () => ({ data: { stderr: '', log_entries: [] } })),
  listRoutes: vi.fn(async () => ({ data: { routes: [] } })),
  setRoute: vi.fn(async () => ({ data: {} })),
  deleteRoute: vi.fn(async () => ({ data: {} })),
}))
vi.mock('@/components/common/CodeEditor.vue', async () => {
  const { h: render } = await import('vue')
  return {
    __esModule: true,
    default: {
      name: 'CodeEditorStub',
      props: {
        modelValue: { type: String, default: '' },
        language: { type: String, default: '' },
        readOnly: { type: Boolean, default: false },
      },
      emits: ['update:modelValue'],
      setup(props) { return () => render('pre', { 'data-code': '' }, props.modelValue) },
    },
  }
})

const settle = async (n = 10) => { for (let i = 0; i < n; i++) { await nextTick(); await new Promise((r) => setTimeout(r, 0)) } }
const body = () => document.body
const byText = (text) => [...body().querySelectorAll('button')].find((b) => b.textContent.includes(text))

const mounted = []
const boot = async ({ managed }) => {
  h.managed = managed
  const Editor = (await import('@/views/Editor.vue')).default
  const editorProps = (r) => ({ ...r.params, mode: r.query.mode === 'edit' ? 'edit' : '' })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/functions/new', name: 'function-new', component: Editor, props: editorProps },
      { path: '/functions/:name', name: 'function-detail', component: Editor, props: editorProps },
      { path: '/functions/:name/test', name: 'function-test', component: { render: () => null } },
      { path: '/:rest(.*)*', component: { render: () => null } },
    ],
  })
  router.push('/functions/alpha?mode=edit')
  await router.isReady()
  const pinia = createPinia()
  setActivePinia(pinia)
  const { useAuthStore } = await import('@/stores/auth')
  useAuthStore(pinia).isAuthenticated = true
  const wrapper = mount({ template: '<router-view />' }, { global: { plugins: [router, pinia] }, attachTo: document.body })
  mounted.push(wrapper)
  await settle()
  const { useConfirmStore } = await import('@/stores/confirm')
  const confirm = useConfirmStore(pinia)
  confirm.ask = vi.fn(async () => true)
  confirm.notify = vi.fn()
  return { wrapper, confirm }
}

const openSecrets = async () => {
  byText('Config').click()
  await settle()
  byText('Secrets').click()
  await settle()
}

const saveSecret = async (name, value) => {
  const nameInput = body().querySelector('input[aria-label="Secret name"]')
  const valueInput = body().querySelector('input[aria-label="Secret value"]')
  nameInput.value = name
  nameInput.dispatchEvent(new window.Event('input'))
  valueInput.value = value
  valueInput.dispatchEvent(new window.Event('input'))
  await settle()
  byText('Save secret').click()
  await settle()
}

const deployInlineCalls = () => h.post.mock.calls.filter(([url]) => url.endsWith('/deploy-inline'))
const postCalls = (url) => h.post.mock.calls.filter(([call]) => call === url)
const putCalls = (url) => h.put.mock.calls.filter(([call]) => call === url)

const deploy = async () => {
  byText('Deploy').click()
  await settle(14)
}

const setValue = (element, value) => {
  element.value = value
  element.dispatchEvent(new window.Event('input'))
}

const setEnvironment = async (key, value) => {
  byText('Config').click()
  await settle()
  byText('Env').click()
  await settle()
  if (key) setValue(body().querySelector('input[placeholder="KEY"]'), key)
  setValue(body().querySelector('input[placeholder="VALUE"]'), value)
  await settle()
}

const configurationCalls = () => h.get.mock.calls.filter(([url]) => url.endsWith('/configuration'))
const settings = (revision) => ({
  data: {
    expected: {
      function_version: '1', artifact_revision: '4', artifact_epoch: '2', generation: 'gen-1',
      policy_revision: String(revision), authority_epoch: '3', approved_artifact: ARTIFACT,
    },
    current_artifact: ARTIFACT,
    review_required: true,
    environment_entries: 0,
    secret_entries: 1,
  },
})

beforeEach(() => {
  h.managed = false
  h.get.mockReset()
  h.post.mockReset()
  h.put.mockReset()
  h.del.mockReset()
  let revision = 5
  h.configuration = () => settings(revision++)
  h.get.mockImplementation(async (url) => {
    if (url === '/functions') return { data: { functions: [FN] } }
    if (url.endsWith('/source')) return { data: { code: 'print(1)', dependencies: '' } }
    if (url.includes('/deployments')) return { data: { deployments: [] } }
    if (url.endsWith('/secrets')) return { data: { secrets: ['API_TOKEN'] } }
    if (url.endsWith('/configuration')) return h.configuration()
    return { data: {} }
  })
  h.post.mockResolvedValue({ data: {} })
  h.put.mockResolvedValue({ data: {} })
  h.del.mockResolvedValue({ data: {} })
})

afterEach(() => {
  for (const wrapper of mounted.splice(0)) wrapper.unmount()
  document.body.innerHTML = ''
})

describe('editor secrets in managed mode', () => {
  it('lists names through the read route and writes through the configuration service', async () => {
    await boot({ managed: true })
    expect(h.get.mock.calls.some(([url]) => url === '/functions/fn-a/secrets')).toBe(true)
    await openSecrets()
    expect(body().textContent).toContain('API_TOKEN')

    await saveSecret('STRIPE_KEY', 'sk_live_1')
    expect(h.post).not.toHaveBeenCalled()
    expect(h.put).toHaveBeenCalledTimes(1)
    expect(h.put.mock.calls[0]).toEqual([
      '/functions/fn-a/configuration/secrets',
      { expected: settings(5).data.expected, current_artifact: ARTIFACT, secret: { key: 'STRIPE_KEY', value: 'sk_live_1' } },
    ])
  })

  it('reads fresh tokens before every write and never reuses one', async () => {
    await boot({ managed: true })
    await openSecrets()
    await saveSecret('FIRST', 'v1')
    await saveSecret('SECOND', 'v2')

    expect(configurationCalls()).toHaveLength(2)
    expect(h.put).toHaveBeenCalledTimes(2)
    const revisions = h.put.mock.calls.map(([, payload]) => payload.expected.policy_revision)
    expect(revisions).toEqual(['5', '6'])
  })

  it('deletes through the configuration service with the key only', async () => {
    await boot({ managed: true })
    await openSecrets()
    body().querySelector('[aria-label="Delete secret API_TOKEN"]').click()
    await settle()

    expect(h.post).not.toHaveBeenCalled()
    expect(h.del).toHaveBeenCalledTimes(1)
    expect(h.del.mock.calls[0]).toEqual([
      '/functions/fn-a/configuration/secrets',
      { data: { expected: settings(5).data.expected, current_artifact: ARTIFACT, secret: { key: 'API_TOKEN' } } },
    ])
  })

  it('stops with a stale message on 409 and does not retry', async () => {
    const { confirm } = await boot({ managed: true })
    await openSecrets()
    h.put.mockRejectedValueOnce({ response: { status: 409, data: { error: { message: 'conflict' } } } })

    await saveSecret('STRIPE_KEY', 'sk_live_1')

    expect(confirm.notify).toHaveBeenCalledTimes(1)
    expect(confirm.notify.mock.calls[0][0].message).toBe('Configuration changed elsewhere; reload to continue.')
    expect(h.put).toHaveBeenCalledTimes(1)
    expect(configurationCalls()).toHaveLength(1)
  })

  it('reports that an administrator must change secrets on 403', async () => {
    const { confirm } = await boot({ managed: true })
    await openSecrets()
    h.del.mockRejectedValueOnce({ response: { status: 403, data: { error: { message: 'denied' } } } })

    body().querySelector('[aria-label="Delete secret API_TOKEN"]').click()
    await settle()

    expect(confirm.notify.mock.calls[0][0].message).toBe('An administrator must change secrets.')
    expect(h.del).toHaveBeenCalledTimes(1)
  })
})

describe('editor secrets in legacy mode', () => {
  it('keeps the legacy secrets CRUD calls unchanged', async () => {
    const { confirm } = await boot({ managed: false })
    await openSecrets()
    await saveSecret('STRIPE_KEY', 'sk_live_1')
    body().querySelector('[aria-label="Delete secret API_TOKEN"]').click()
    await settle()

    expect(h.post).toHaveBeenCalledWith('/functions/fn-a/secrets', { key: 'STRIPE_KEY', value: 'sk_live_1' })
    expect(h.del.mock.calls.map(([url]) => url)).toEqual(['/functions/fn-a/secrets/API_TOKEN'])
    expect(h.put).not.toHaveBeenCalled()
    expect(configurationCalls()).toHaveLength(0)
    expect(confirm.notify).not.toHaveBeenCalled()
  })
})

describe('editor deploy in managed mode', () => {
  it('does not publish a configuration an unchanged redeploy did not change', async () => {
    await boot({ managed: true })
    await deploy()

    expect(configurationCalls()).toHaveLength(0)
    expect(h.put).not.toHaveBeenCalled()
    expect(postCalls('/functions/fn-a')).toHaveLength(0)
    expect(deployInlineCalls()).toHaveLength(1)
  })

  it('publishes the exact settings body before the build when configuration changed', async () => {
    await boot({ managed: true })
    await setEnvironment('', 'changed')
    await deploy()

    expect(putCalls('/functions/fn-a')).toHaveLength(0)
    expect(h.put).toHaveBeenCalledTimes(1)
    const [url, payload] = h.put.mock.calls[0]
    expect(url).toBe('/functions/fn-a/configuration/settings')
    expect(Object.keys(payload).sort()).toEqual(['current_artifact', 'environment', 'expected', 'settings'])
    expect(payload.current_artifact).toBe(ARTIFACT)
    expect(payload.expected).toEqual(settings(5).data.expected)
    expect(payload.environment).toEqual({ EXISTING: 'changed' })
    expect(payload.settings).toEqual({
      timeout_ms: 5000,
      memory_mb: 64,
      cpus: 0.5,
      network_mode: 'none',
      sdk: false,
      max_concurrency: 0,
      concurrency_policy: 'queue',
      auth_mode: 'none',
      rate_limit_per_min: 0,
    })
    expect(deployInlineCalls()).toHaveLength(1)
    expect(h.put.mock.invocationCallOrder[0]).toBeLessThan(h.post.mock.invocationCallOrder.find((_, i) => h.post.mock.calls[i][0].endsWith('/deploy-inline')))
  })

  it('stops before the build when the configuration moved elsewhere', async () => {
    await boot({ managed: true })
    await setEnvironment('', 'changed')
    h.put.mockRejectedValueOnce({ response: { status: 409, data: { error: { message: 'conflict' } } } })

    await deploy()

    expect(deployInlineCalls()).toHaveLength(0)
    expect(body().textContent).toContain('Configuration changed elsewhere; reload to continue.')
  })
})

describe('editor deploy in legacy mode', () => {
  it('keeps the legacy configuration update unchanged', async () => {
    await boot({ managed: false })
    await deploy()

    expect(configurationCalls()).toHaveLength(0)
    expect(h.put).toHaveBeenCalledTimes(1)
    const [url, payload] = h.put.mock.calls[0]
    expect(url).toBe('/functions/fn-a')
    expect(payload).toMatchObject({
      description: '',
      memory_mb: 64,
      cpus: 0.5,
      env_vars: { EXISTING: 'keep' },
      network_mode: 'none',
      sdk: false,
      max_concurrency: 0,
      concurrency_policy: 'queue',
      auth_mode: 'none',
      rate_limit_per_min: 0,
    })
    expect(deployInlineCalls()).toHaveLength(1)
  })
})
