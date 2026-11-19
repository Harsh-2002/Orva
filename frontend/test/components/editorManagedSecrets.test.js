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
  version: 1,
  memory_mb: 64, cpus: 0.5, code_hash: 'ha', timeout_ms: 5000, network_mode: 'none', sdk: false,
  max_concurrency: 0, concurrency_policy: 'queue', auth_mode: 'none', rate_limit_per_min: 0,
  env_vars: { EXISTING: 'keep' },
}
const FN_B = { ...FN, id: 'fn-b', name: 'beta', code_hash: 'hb' }
const ARTIFACT = 'a'.repeat(64)
const BUILD = '12345678-1234-7234-8234-123456789abc'
const ROLLBACK = '12345678-1234-7234-8234-123456789abd'
const FN_ROLLBACK = { ...FN, id: '12345678-1234-7234-8234-123456789abe', name: 'rollback-demo', active_deployment_id: BUILD }
const ROLLBACK_TARGET = { deployment_id: ROLLBACK, version: 2, code_hash: 'ha', short_hash: 'ha' }
const rollbackData = {
  id: ROLLBACK, deployment_id: ROLLBACK, function_id: FN_ROLLBACK.id, version: 2,
  code_hash: 'ha', status: 'succeeded', phase: 'review_required', function_status: 'inactive',
}
let buildStream

class ManagedBuildStream {
  static CLOSED = 2
  listeners = new Map()
  readyState = 1

  constructor(url) {
    this.url = url
    buildStream = this
  }

  addEventListener(name, listener) { this.listeners.set(name, listener) }
  close() { this.readyState = ManagedBuildStream.CLOSED }
  succeed(owner) {
    this.listeners.get('succeeded')({ data: JSON.stringify({ id: BUILD, function_id: owner, version: 3, status: 'succeeded' }) })
  }
}

const h = vi.hoisted(() => ({
  mode: vi.fn(),
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  rollback: vi.fn(),
  configuration: null,
}))
vi.mock('@/api/managedPolicies.js', () => ({
  readManagedIAMMode: h.mode,
}))
vi.mock('@/api/client', () => ({
  default: { get: h.get, post: h.post, put: h.put, delete: h.del },
  getApiKey: () => 'k',
}))
vi.mock('@/api/endpoints', () => ({
  __esModule: true,
  listFunctions: vi.fn(async () => ({ data: { functions: [FN, FN_B], total: 2 } })),
  listDeployments: vi.fn(async () => ({ data: { deployments: [] } })),
  rollbackFunction: h.rollback,
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
const boot = async ({ managed, capability, path = '/functions/alpha?mode=edit', autoBuild = true } = {}) => {
  if (capability) h.mode.mockImplementation(capability)
  else h.mode.mockResolvedValue(managed ? 'managed' : 'legacy')
  if (managed) {
    vi.stubGlobal('EventSource', ManagedBuildStream)
    h.post.mockImplementation(async (url) => {
      const owner = /^\/functions\/([^/]+)\/deploy-inline$/.exec(url)?.[1]
      if (!owner) return { data: {} }
      if (autoBuild) setTimeout(() => buildStream?.succeed(owner), 0)
      return { data: { deployment_id: BUILD } }
    })
  }
  const Editor = (await import('@/views/Editor.vue')).default
  const editorProps = (r) => ({ ...r.params, mode: r.query.mode === 'edit' ? 'edit' : '' })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/functions/new', name: 'function-new', component: Editor, props: editorProps },
      { path: '/functions/:name', name: 'function-detail', component: Editor, props: editorProps },
      { path: '/functions/:name/test', name: 'function-test', component: { render: () => null } },
      { path: '/functions/:functionId/permissions', name: 'function-permissions', component: { render: () => null } },
      { path: '/:rest(.*)*', component: { render: () => null } },
    ],
  })
  router.push(path)
  await router.isReady()
  const pinia = createPinia()
  setActivePinia(pinia)
  const { useAuthStore } = await import('@/stores/auth')
  useAuthStore(pinia).isAuthenticated = true
  const wrapper = mount({
    template: '<router-view v-slot="{ Component }"><keep-alive><component :is="Component" /></keep-alive></router-view>',
  }, { global: { plugins: [router, pinia] }, attachTo: document.body })
  mounted.push(wrapper)
  await settle()
  if (!capability && path !== '/functions/new') {
    const { useManagedIAMStore } = await import('@/stores/managedIAM.js')
    expect(useManagedIAMStore(pinia).mode).toBe(managed ? 'managed' : 'legacy')
  }
  const { useConfirmStore } = await import('@/stores/confirm')
  const confirm = useConfirmStore(pinia)
  confirm.ask = vi.fn(async () => true)
  confirm.notify = vi.fn()
  return { wrapper, confirm, router, editor: wrapper.findComponent(Editor).vm }
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

const deferred = () => {
  let resolve
  let reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

const noMutations = () => {
  expect(h.post).not.toHaveBeenCalled()
  expect(h.put).not.toHaveBeenCalled()
  expect(h.del).not.toHaveBeenCalled()
}

const configurationCalls = () => h.get.mock.calls.filter(([url]) => url.endsWith('/configuration'))
const settings = (revision, functionVersion = '1') => ({
  data: {
    expected: {
      function_version: functionVersion, artifact_revision: '4', artifact_epoch: '2', generation: 'gen-1',
      policy_revision: String(revision), authority_epoch: '3', approved_artifact: ARTIFACT,
    },
    current_artifact: ARTIFACT,
    review_required: true,
    environment_entries: 0,
    secret_entries: 1,
  },
})
const settingsAck = { data: { function_version: '2' } }

beforeEach(() => {
  h.mode.mockReset()
  h.get.mockReset()
  h.post.mockReset()
  h.put.mockReset()
  h.del.mockReset()
  h.rollback.mockReset()
  let revision = 5
  h.configuration = () => settings(revision++)
  h.get.mockImplementation(async (url) => {
    if (url === '/functions') return { data: { functions: [FN, FN_B, FN_ROLLBACK] } }
    if (url.endsWith('/source')) return { data: { code: url.includes('fn-b') ? 'print(2)' : 'print(1)', dependencies: '' } }
    if (url.startsWith(`/functions/${FN_ROLLBACK.id}/deployments`)) {
      return { data: { deployments: [
        { id: BUILD, status: 'succeeded', version: 3, code_hash: 'ha' },
        { id: ROLLBACK, status: 'succeeded', version: 2, code_hash: 'ha' },
      ] } }
    }
    if (url.includes('/deployments')) return { data: { deployments: [] } }
    if (url.endsWith('/secrets')) return { data: { secrets: [url.includes('fn-b') ? 'BETA_TOKEN' : 'API_TOKEN'] } }
    if (url.endsWith('/configuration')) return h.configuration()
    return { data: {} }
  })
  h.post.mockResolvedValue({ data: {} })
  h.put.mockImplementation(async (url) => url.endsWith('/configuration/settings') ? settingsAck : { data: {} })
  h.del.mockResolvedValue({ data: {} })
})

afterEach(() => {
  for (const wrapper of mounted.splice(0)) wrapper.unmount()
  vi.unstubAllGlobals()
  buildStream = undefined
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
      { expected: settings(6).data.expected, current_artifact: ARTIFACT, secret: { key: 'STRIPE_KEY', value: 'sk_live_1' } },
    ])
  })

  it('reads fresh tokens before every write and never reuses one', async () => {
    await boot({ managed: true })
    await openSecrets()
    await saveSecret('FIRST', 'v1')
    await saveSecret('SECOND', 'v2')

    expect(configurationCalls()).toHaveLength(3)
    expect(h.put).toHaveBeenCalledTimes(2)
    const revisions = h.put.mock.calls.map(([, payload]) => payload.expected.policy_revision)
    expect(revisions).toEqual(['6', '7'])
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
      { data: { expected: settings(6).data.expected, current_artifact: ARTIFACT, secret: { key: 'API_TOKEN' } } },
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
    expect(configurationCalls()).toHaveLength(2)
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
  it('shows a separate install action for the exact built deployment, then offers permissions review', async () => {
    const { editor } = await boot({ managed: true, path: '/functions/new', autoBuild: false })
    editor.form.name = 'new-function'
    h.post.mockResolvedValueOnce({ data: { id: 'fn-new' } })
    await deploy()
    expect(buildStream.url).toBe(`/api/v1/deployments/${BUILD}/stream`)
    buildStream.succeed('fn-new')
    await settle()

    expect(body().textContent).toContain('Build succeeded. Install required.')
    expect(body().textContent).not.toContain('v3 live')
    expect(h.post.mock.calls.map(([url]) => url)).toEqual(['/functions', '/functions/fn-new/deploy-inline'])
    expect(body().querySelector('a[href="/functions/fn-new/permissions"]')).toBeNull()
    h.post.mockResolvedValueOnce({ data: { review_required: true, artifact: ARTIFACT } })
    byText('Install build').click()
    await settle()
    expect(h.post.mock.calls.at(-1)).toEqual([`/deployments/${BUILD}/install`])
    expect(body().textContent).toContain('Installed. Permissions review required.')
    expect(body().querySelector('a[href="/functions/fn-new/permissions"]').textContent).toContain('Review permissions')
  })

  it('does not show an install acknowledgement on another function', async () => {
    const first = deferred()
    const { router } = await boot({ managed: true })
    await deploy()
    expect(body().textContent).toContain('Build succeeded. Install required.')
    h.post.mockReturnValueOnce(first.promise)
    byText('Install build').click()
    await settle()
    expect(h.post.mock.calls.at(-1)).toEqual([`/deployments/${BUILD}/install`])

    await router.push('/functions/beta?mode=edit')
    await settle()
    first.resolve({ data: { review_required: true, artifact: ARTIFACT } })
    await settle()
    expect(body().textContent).not.toContain('Installed. Permissions review required.')
    expect(body().querySelector('a[href="/functions/fn-b/permissions"]')).toBeNull()
  })

  it('does not offer installation for a mismatched build result', async () => {
    await boot({ managed: true, autoBuild: false })
    await deploy()
    buildStream.succeed('fn-b')
    await settle()
    expect(body().textContent).toContain('Build result could not be verified')
    expect(byText('Install build')).toBeUndefined()
    expect(h.post.mock.calls.map(([url]) => url)).toEqual(['/functions/fn-a/deploy-inline'])
  })

  it('creates a managed owner with only the name, runtime and exact build entrypoint', async () => {
    const { editor } = await boot({ managed: true, path: '/functions/new' })
    editor.form.name = 'new-function'
    editor.form.runtime = 'node'
    editor.templateEntrypoint = 'handler.ts'
    h.post.mockResolvedValueOnce({ data: { id: 'fn-new' } })

    await deploy()

    expect(h.post.mock.calls[0]).toEqual([
      '/functions', { name: 'new-function', runtime: 'node', entrypoint: 'handler.ts' },
    ])
    expect(deployInlineCalls()).toEqual([
      ['/functions/fn-new/deploy-inline', expect.objectContaining({ filename: 'handler.ts' })],
    ])
    expect(h.put).not.toHaveBeenCalled()
  })

  it('does not resolve or deploy to a same-name owner after a managed create conflict', async () => {
    const { editor } = await boot({ managed: true, path: '/functions/new' })
    editor.form.name = FN.name
    const reads = h.get.mock.calls.length
    h.post.mockRejectedValueOnce({ response: { status: 409, data: { error: { message: 'Function name already exists' } } } })

    await deploy()

    expect(h.post.mock.calls.map(([url]) => url)).toEqual(['/functions'])
    expect(h.get.mock.calls).toHaveLength(reads)
    expect(h.put).not.toHaveBeenCalled()
    expect(editor.fnId).toBe('')
    expect(body().textContent).toContain('Function name already exists')
  })

  it('keeps queued secrets and unsupported settings without writing or creating an owner', async () => {
    const { editor } = await boot({ managed: true, path: '/functions/new' })
    editor.form.name = 'new-function'
    editor.form.memory_mb = 128
    editor.envVars = [{ key: 'DRAFT', value: 'kept' }]
    editor.secretForm = { name: 'PENDING', value: 'local-only' }
    await editor.saveSecret()

    await deploy()

    noMutations()
    expect(editor.pendingSecrets).toEqual([{ name: 'PENDING', value: 'local-only' }])
    expect(editor.form.memory_mb).toBe(128)
    expect(editor.envVars).toEqual([{ key: 'DRAFT', value: 'kept' }])
    expect(body().textContent).toContain('install before configuring')
  })

  it.each([
    { name: 'Uppercase' },
    { runtime: 'invalid' },
    { entrypoint: '../handler.py' },
  ])('rejects invalid managed creation input %j before any write', async (invalid) => {
    const { editor } = await boot({ managed: true, path: '/functions/new' })
    editor.form.name = invalid.name ?? 'new-function'
    if (invalid.runtime) editor.form.runtime = invalid.runtime
    if (invalid.entrypoint) editor.templateEntrypoint = invalid.entrypoint

    await deploy()

    noMutations()
    expect(editor.buildError).not.toBe('')
  })

  it('can deploy code without settings CAS when the configuration is unchanged', async () => {
    h.configuration = () => ({ data: { ...settings(5).data, current_artifact: '' } })
    await boot({ managed: true })
    await deploy()

    expect(configurationCalls()).toHaveLength(1)
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

  it.each(['during load', 'after load'])('never pairs stale environment with settings expectations refreshed %s', async (when) => {
    const latest = settings(6, '2')
    const observation = deferred()
    if (when === 'during load') h.configuration = () => observation.promise
    const { editor } = await boot({ managed: true })
    h.configuration = () => latest
    editor.form.memory_mb = 128
    // The server now has a different environment at version 2.
    h.put.mockImplementation(async (_url, { expected }) => {
      if (expected.function_version === '2') return { data: { function_version: '3' } }
      throw { response: { status: 409 } }
    })

    const building = editor.runDeploy()
    if (when === 'during load') {
      await settle()
      noMutations()
      observation.resolve(latest)
    }
    await building

    expect(configurationCalls()).toHaveLength(1)
    expect(deployInlineCalls()).toHaveLength(0)
    if (when === 'during load') {
      expect(h.put).not.toHaveBeenCalled()
      expect(editor.buildError).toContain('reload')
    } else {
      expect(h.put).toHaveBeenCalledTimes(1)
      expect(h.put.mock.calls[0][1]).toMatchObject({ expected: settings(5).data.expected, environment: FN.env_vars })
      expect(editor.buildError).toBe('Configuration changed elsewhere; reload to continue.')
    }
  })

  it('does not publish acknowledged settings again when retrying a failed code upload', async () => {
    const { editor } = await boot({ managed: true })
    editor.envVars = [{ key: 'EXISTING', value: 'changed' }]
    editor.form.memory_mb = 128
    h.post.mockRejectedValueOnce(new Error('build submission failed'))

    await deploy()
    expect(editor.buildError).toBe('build submission failed')
    await deploy()

    expect(putCalls('/functions/fn-a/configuration/settings')).toHaveLength(1)
    expect(deployInlineCalls()).toHaveLength(2)
    expect(configurationCalls()).toHaveLength(1)
    expect(editor.buildError).toBe('')

    editor.form.memory_mb = 256
    await deploy()
    expect(putCalls('/functions/fn-a/configuration/settings')).toHaveLength(1)
    expect(deployInlineCalls()).toHaveLength(2)
    expect(editor.buildError).toContain('reload')
  })
})

describe('editor rollback in managed mode', () => {
  const open = () => boot({ managed: true, path: '/functions/rollback-demo?mode=edit' })

  it('keeps same-hash deployments distinct and restores the selected ID without installing it', async () => {
    h.rollback.mockResolvedValue({ data: rollbackData })
    const { editor, confirm } = await open()
    expect(editor.versions.map(({ deployment_id, is_active }) => [deployment_id, is_active]))
      .toEqual([[BUILD, true], [ROLLBACK, false]])
    await editor.rollbackToVersion(ROLLBACK_TARGET)
    await settle()
    expect(confirm.ask.mock.calls[0][0].message).toContain('Review permissions separately')
    expect(h.rollback.mock.calls).toEqual([[FN_ROLLBACK.id, { deployment_id: ROLLBACK }]])
    expect(body().textContent).toContain('Version restored. Install required.')
    expect(byText('Install restored version')).toBeTruthy()
    expect(h.post).not.toHaveBeenCalled()
  })

  it('refuses to show an install control for a different deployment response', async () => {
    h.rollback.mockResolvedValue({ data: { ...rollbackData, id: BUILD, deployment_id: BUILD } })
    const { editor, confirm } = await open()
    await editor.rollbackToVersion(ROLLBACK_TARGET)
    expect(confirm.notify.mock.calls[0][0].message).toContain('Rollback result could not be verified')
    expect(editor.lastBuild).toBeNull()
    expect(h.post).not.toHaveBeenCalled()
  })

  it.each(['confirmation', 'request'])('does not update a new owner after navigation during the rollback %s', async (stage) => {
    const gate = deferred()
    const { editor, router, confirm } = await open()
    if (stage === 'confirmation') confirm.ask.mockReturnValueOnce(gate.promise)
    else h.rollback.mockReturnValueOnce(gate.promise)
    const pending = editor.rollbackToVersion(ROLLBACK_TARGET)
    await settle()
    expect(confirm.ask).toHaveBeenCalledTimes(1)
    expect(h.rollback).toHaveBeenCalledTimes(stage === 'confirmation' ? 0 : 1)

    await router.push('/functions/beta?mode=edit')
    await settle()
    gate.resolve(stage === 'confirmation' ? true : { data: rollbackData })
    await pending
    await settle()

    expect(h.rollback).toHaveBeenCalledTimes(stage === 'confirmation' ? 0 : 1)
    expect(editor.fnId).toBe('fn-b')
    expect(editor.lastBuild).toBeNull()
    expect(confirm.notify).not.toHaveBeenCalled()
    expect(byText('Install restored version')).toBeUndefined()
  })
})

describe('editor deploy in legacy mode', () => {
  it('preserves the full create body and flushes legacy secrets before the first build', async () => {
    const { editor } = await boot({ managed: false, path: '/functions/new' })
    editor.form.name = 'new-function'
    editor.form.description = 'description'
    editor.form.memory_mb = 128
    editor.envVars = [{ key: 'DRAFT', value: 'env' }]
    editor.secretForm = { name: 'PENDING', value: 'secret' }
    await editor.saveSecret()
    h.post.mockResolvedValueOnce({ data: { id: 'fn-new' } })

    await deploy()

    expect(h.post.mock.calls).toEqual([
      ['/functions', {
        name: 'new-function', description: 'description', runtime: 'python', memory_mb: 128, cpus: 0.5,
        env_vars: { DRAFT: 'env' }, network_mode: 'none', sdk: false, max_concurrency: 0,
        concurrency_policy: 'queue', auth_mode: 'none', rate_limit_per_min: 0,
      }],
      ['/functions/fn-new/secrets', { key: 'PENDING', value: 'secret' }],
      ['/functions/fn-new/deploy-inline', expect.objectContaining({ filename: 'handler.py' })],
    ])
    expect(editor.pendingSecrets).toEqual([])
  })

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

describe('editor mutation ownership', () => {
  it('waits for capability and never falls through to legacy writes on error', async () => {
    const capability = deferred()
    const { confirm, editor } = await boot({ capability: () => capability.promise })
    const building = editor.runDeploy()
    editor.secretForm = { name: 'TOKEN', value: 'draft' }
    const saving = editor.saveSecret()
    const deleting = editor.removeSecret('API_TOKEN')
    await settle()

    expect(h.mode).toHaveBeenCalledTimes(1)
    noMutations()
    capability.reject(new Error('no capability response'))
    await Promise.all([building, saving, deleting])
    await settle()

    noMutations()
    expect(configurationCalls()).toHaveLength(0)
    expect(body().textContent).toContain('Could not determine the server mode')
    expect(confirm.notify.mock.calls.map(([notice]) => notice.title)).toEqual(['Save failed', 'Delete failed'])
    expect(editor.secretForm).toEqual({ name: 'TOKEN', value: 'draft' })
  })

  it.each(['route change', 'KeepAlive departure and return'])('does not continue an in-flight settings PUT after %s', async (transition) => {
    const put = deferred()
    const { editor, router, wrapper } = await boot({ managed: true })
    h.put.mockReturnValueOnce(put.promise)
    editor.envVars = [{ key: 'EXISTING', value: 'changed' }]
    const building = editor.runDeploy()
    await settle()
    expect(putCalls('/functions/fn-a/configuration/settings')).toHaveLength(1)

    if (transition === 'route change') {
      await router.push('/functions/beta?mode=edit')
    } else {
      await router.push('/elsewhere')
      await settle()
      await router.push('/functions/alpha?mode=edit')
    }
    await settle()
    put.resolve(settingsAck)
    await building
    await settle()

    expect(deployInlineCalls()).toHaveLength(0)
    expect(wrapper.find('[data-code]').text()).toBe(transition === 'route change' ? 'print(2)' : 'print(1)')
    expect(editor.deploying).toBe(false)
    expect(editor.buildLogs).not.toContain('Updated function configuration')
    expect(editor.buildModalOpen).toBe(false)
  })

  it('deploys the original draft even when code and settings change while the PUT waits', async () => {
    const put = deferred()
    const { editor } = await boot({ managed: true })
    h.put.mockReturnValueOnce(put.promise)
    editor.envVars = [{ key: 'EXISTING', value: 'original' }]
    const building = editor.runDeploy()
    await settle()
    editor.envVars[0].value = 'later'
    editor.code = 'print("later")'
    editor.dependencyText = 'later'
    editor.templateEntrypoint = 'later.py'
    editor.templateExtras = { 'later.txt': 'later' }
    put.resolve(settingsAck)
    await building

    expect(h.put.mock.calls[0][1].environment).toEqual({ EXISTING: 'original' })
    expect(deployInlineCalls()).toEqual([
      ['/functions/fn-a/deploy-inline', { code: 'print(1)', filename: 'handler.py', dependencies: '' }],
    ])
    expect(editor.code).toBe('print("later")')
    expect(editor.isDirty).toBe(true)
  })

  it.each([true, false])('does not delete after navigating during confirmation (managed=%s)', async (managed) => {
    const answer = deferred()
    const { confirm, router, editor } = await boot({ managed })
    confirm.ask.mockReturnValueOnce(answer.promise)
    const deleting = editor.removeSecret('API_TOKEN')
    expect(confirm.ask).toHaveBeenCalledWith(expect.objectContaining({ title: 'Delete secret?' }))
    await router.push('/functions/beta?mode=edit')
    await settle()
    answer.resolve(true)
    await deleting

    noMutations()
    expect(configurationCalls()).toHaveLength(managed ? 2 : 0)
    expect(confirm.notify).not.toHaveBeenCalled()
  })

  it('ignores a stale secret list after opening another function', async () => {
    const first = deferred()
    const get = h.get.getMockImplementation()
    h.get.mockImplementation((url) => url === '/functions/fn-a/secrets' ? first.promise : get(url))
    const { router, editor } = await boot({ managed: true })
    expect(h.get).toHaveBeenCalledWith('/functions/fn-a/secrets')
    await router.push('/functions/beta?mode=edit')
    await settle()
    first.resolve({ data: { secrets: ['ALPHA_TOKEN'] } })
    await settle()

    expect(editor.secrets).toEqual([{ id: 'BETA_TOKEN', name: 'BETA_TOKEN' }])
  })

  it('does not send a managed secret write after leaving while its CAS read waits', async () => {
    const read = deferred()
    const { editor, router } = await boot({ managed: true })
    h.configuration = () => read.promise
    editor.secretForm = { name: 'A_SECRET', value: 'a draft' }
    const saving = editor.saveSecret()
    await settle()
    expect(configurationCalls().map(([url]) => url)).toEqual([
      '/functions/fn-a/configuration', '/functions/fn-a/configuration',
    ])
    await router.push('/functions/beta?mode=edit')
    await settle()
    editor.secretForm = { name: 'B_SECRET', value: 'b draft' }
    read.resolve(settings(5))
    await saving

    noMutations()
    expect(editor.secretForm).toEqual({ name: 'B_SECRET', value: 'b draft' })
  })
})
