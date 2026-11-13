import { createSSRApp, defineComponent, h, nextTick, reactive, ref, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { api } from '@/api'
import { mountSetup } from '@/test-component'
import type { Account, Model, PathListing, ServiceConfig } from '@/types'
import { messages } from '@/locales/en'
import AccountsPanel from './AccountsPanel.vue'
import AccountEnvironmentFields from './AccountEnvironmentFields.vue'
import AccountOrderList from './AccountOrderList.vue'
import DurationInput from './DurationInput.vue'
import PairRows from './PairRows.vue'
import PathList from './PathList.vue'
import ModelsTable from './ModelsTable.vue'
import SettingRow from './SettingRow.vue'
import SettingsPanel from './SettingsPanel.vue'
import UiSelect from './UiSelect.vue'

const route = vi.hoisted(() => ({ section: 'models' }))

vi.mock('@/desktop', () => ({
  keepPage: () => () => {},
  desktopPlatform: '',
  sendDesktop: vi.fn(),
}))
vi.mock('@/api', () => ({
  api: { firstEventLatency: vi.fn(async () => []), saveConfig: vi.fn(), paths: vi.fn() },
}))
vi.mock('@/url', () => ({ useQueryParam: () => ref(route.section), useQueryText: () => ref('') }))
vi.mock('vue-router', () => ({ useRouter: () => ({ beforeEach: () => () => {} }) }))
vi.mock('@/i18n', () => ({
  channelLabelKey: (channel: string) => `channel.${channel}`,
  useI18n: () => ({
    t: (key: keyof typeof messages) => messages[key],
    locale: { value: 'en' },
    errorText: String,
    logText: String,
  }),
}))

// Reka portals mount only in a browser; exercise the parent-provided help content in SSR.
vi.mock('./HelpHint.vue', () => ({
  default: defineComponent({
    setup(_props, { slots }) {
      return () => h('div', { 'data-help-hint': '' }, slots.default?.())
    },
  }),
}))

const cleanups: (() => void)[] = []
beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('window', {
    location: new URL('https://console.example.test'),
    isSecureContext: true,
    setTimeout,
    clearTimeout,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })
})
afterEach(() => {
  cleanups.splice(0).forEach((cleanup) => cleanup())
  vi.unstubAllGlobals()
})

const config: ServiceConfig = {
  auto_start: false,
  hide_dock_icon: false,
  request_body_log: false,
  log_file: false,
  log_max_total_mb: 100,
  admin_auth_enabled: true,
  admin_username: 'admin',
  admin_password_set: true,
  build_native_nonstream: false,
  nonstream_keepalive: false,
  fake_stream: false,
  youtube_links: false,
  search_model_aliases: false,
  discover_hidden_models: false,
  google_search: false,
  anti_truncation: false,
  auto_continuation: true,
  continuation_rewrite: false,
  continuation_prompt: '',
  first_event_hedge: 'off',
  first_event_hedge_models: 'gemini-test',
  auth_states: '',
  listen_addr: ':2048',
  preview_listen_addr: ':2049',
  proxy_api_key: 'first-key,second-key',
  active_listen_addr: ':2048',
  reserved_listen_addr: '',
  active_preview_listen_addr: ':2049',
  active_proxy_api_key: 'first-key,second-key',
  management_restart_required: false,
  service_restart_required: false,
  proxy: '',
  active_proxy: '',
  proxy_pool: [],
  init_timeout: '2m0s',
  request_timeout: '5m0s',
  warm_worker_limit: 0,
  max_active_workers: 1,
  warm_startup_concurrency: 1,
  per_account_concurrency: 1,
  max_account_switches: 3,
  routing_strategy: 'round-robin',
  account_order: '',
  upstream_channels: ['playground', 'build'],
  model_mapping: '',
  model_channels: '',
  model_accounts: '',
  api_key_accounts: 'first-key:account-a',
  waa_backend: 'spidermonkey',
  strip_thought_signatures: false,
  media_output: 'inline',
  media_base_url: '',
  environment: [],
  invalid: [],
}

async function render(component: Component, props: Record<string, unknown>): Promise<string> {
  const app = createSSRApp({ render: () => h(component, props) })
  app.directive('tooltip', {})
  return renderToString(app)
}

describe('merged settings controls', () => {
  it.each([
    [false, false],
    [false, true],
    [true, false],
    [true, true],
  ])('renders anti=%s and auto=%s as independent immediate switches', async (anti, auto) => {
    route.section = 'models'
    const html = await render(SettingsPanel, {
      config: { ...config, anti_truncation: anti, auto_continuation: auto },
      accounts: [],
      models: [],
      loading: false,
      error: '',
    })
    for (const [id, checked] of [
      ['anti-truncation', anti],
      ['auto-continuation', auto],
    ]) {
      expect(html).toMatch(new RegExp(`id="setting-${id}"[^>]*aria-checked="${checked}"`))
    }
  })

  it('locks each local switch using its own environment key', async () => {
    route.section = 'models'
    const html = await render(SettingsPanel, {
      config: { ...config, environment: ['ANTI_TRUNCATION', 'AUTO_CONTINUATION'] },
      accounts: [],
      models: [],
      loading: false,
      error: '',
    })
    const locked = html.match(/<fieldset disabled[\s\S]*?<\/fieldset>/g) ?? []
    expect(locked).toHaveLength(2)
    expect(locked[0]).toContain('id="setting-anti-truncation"')
    expect(locked[1]).toContain('id="setting-auto-continuation"')
    expect(html).toContain(
      messages['settings.environmentLocked'].replace('{key}', 'ANTI_TRUNCATION'),
    )
    expect(html).toContain(
      messages['settings.environmentLocked'].replace('{key}', 'AUTO_CONTINUATION'),
    )
  })

  it('keeps hidden-model discovery and account-switch limits alongside local settings', async () => {
    route.section = 'models'
    const models = await render(SettingsPanel, {
      config: { ...config, discover_hidden_models: true, environment: ['DISCOVER_HIDDEN_MODELS'] },
      accounts: [],
      models: [],
      loading: false,
      error: '',
    })
    expect(models).toMatch(/id="setting-discover-hidden-models"[^>]*aria-checked="true"/)
    expect(models).toContain('id="setting-anti-truncation"')
    expect(models).toContain('id="setting-auto-continuation"')
    const locked = models.match(/<fieldset disabled[\s\S]*?<\/fieldset>/g) ?? []
    expect(locked).toHaveLength(1)
    expect(locked[0]).toContain('id="setting-discover-hidden-models"')
    route.section = 'capacity'
    const capacity = await render(SettingsPanel, {
      config: { ...config, max_account_switches: 0 },
      accounts: [],
      models: [],
      loading: false,
      error: '',
    })
    expect(capacity).toMatch(/id="setting-max-account-switches"[^>]*value="0"[^>]*min="0"/)
    expect(capacity).toMatch(/id="setting-warm_worker_limit"[^>]*value="0"[^>]*min="0"/)
  })

  it.each(['camoufox', 'spidermonkey'] as const)(
    'preserves zero-warm-pool defaults for %s',
    async (backend) => {
      route.section = 'capacity'
      const html = await render(SettingsPanel, {
        config: { ...config, waa_backend: backend },
        accounts: [],
        models: [],
        loading: false,
        error: '',
      })
      expect(html).toMatch(/id="setting-warm_worker_limit"[^>]*value="0"[^>]*min="0"/)
      for (const field of [
        'max_active_workers',
        'warm_startup_concurrency',
        'per_account_concurrency',
      ]) {
        expect(html).toMatch(new RegExp(`id="setting-${field}"[^>]*value="1"[^>]*min="1"`))
      }
      expect(html).toContain('aria-valuemin="0"')
      expect(html).toContain(messages['settings.warmWorkerLimitHelp'])
      expect(html).toContain('0/1/1/1')
    },
  )

  it.each([false, true])(
    'preserves Build-native nonstream=%s and its own environment lock',
    async (enabled) => {
      route.section = 'runtime'
      const html = await render(SettingsPanel, {
        config: {
          ...config,
          build_native_nonstream: enabled,
          environment: ['BUILD_NATIVE_NONSTREAM'],
        },
        accounts: [],
        models: [],
        loading: false,
        error: '',
      })
      const locked = html.match(/<fieldset disabled[\s\S]*?<\/fieldset>/g) ?? []
      expect(locked).toHaveLength(1)
      expect(locked[0]).toMatch(
        new RegExp(`id="setting-build-native"[^>]*aria-checked="${enabled}"`),
      )
      expect(html).toContain(messages['settings.buildNativeHelp'])
      expect(html).toContain(messages['settings.fixedChannelHelp'])
      expect(html).not.toContain(messages['settings.upstreamChannelsHelp'])
    },
  )

  it('locks strip_thought_signatures and no longer offers a temporary-chat toggle', async () => {
    route.section = 'models'
    const html = await render(SettingsPanel, {
      config: { ...config, environment: ['STRIP_THOUGHT_SIGNATURES'] },
      accounts: [],
      models: [],
      loading: false,
      error: '',
    })
    const locked = html.match(/<fieldset disabled[\s\S]*?<\/fieldset>/g) ?? []
    expect(locked).toHaveLength(1)
    expect(locked[0]).toContain('id="setting-strip-thought-signatures"')
    expect(html).not.toContain('id="setting-temporary-chat"')
    expect(html).toContain('id="setting-auto-continuation"')
    expect(html).toContain('id="setting-continuation-rewrite"')
  })

  it('renders the new account binding selector without exposing full keys', async () => {
    route.section = 'service'
    const html = await render(SettingsPanel, {
      config,
      accounts: [{ id: 'account-a', label: 'Fixture account' }],
      models: [],
      loading: false,
      error: '',
    })
    const bindings = html.slice(html.indexOf('aria-labelledby="setting-api-key-accounts-label"'))
    expect(bindings).toContain('Fixture account')
    expect(bindings).toContain(messages['settings.keyAccountsAll'])
    expect(bindings).not.toContain('first-key')
    expect(bindings).not.toContain('second-key')
  })

  it('keeps environment locking and full-width controls until the local xl layout', async () => {
    const html = await render(SettingRow, {
      label: 'Fixture setting',
      effect: 'immediate',
      env: 'ANTI_TRUNCATION',
    })
    expect(html).toContain('<fieldset disabled')
    expect(html).toContain('xl:w-80')
    expect(html).not.toContain('md:w-80')
    expect(html).toContain(messages['settings.effect.immediate'])
  })

  it.each([
    ['2m0s', '2', 'minutes'],
    ['1h30m', '90', 'minutes'],
    ['90s', '90', 'seconds'],
    ['500ms', '0.5', 'seconds'],
  ])('displays stored duration %s without rewriting it', async (value, amount, unit) => {
    const update = vi.fn()
    const html = await render(DurationInput, {
      id: 'duration',
      inputClass: 'fixture',
      modelValue: value,
      'onUpdate:modelValue': update,
    })
    expect(html).toContain(`value="${amount}"`)
    expect(html).toContain(unit)
    expect(update).not.toHaveBeenCalled()
  })

  it('retains mapped model suffixes absent from the current catalog', async () => {
    const target = '\u6297\u622a\u65ad-model-build(high)-search'
    const update = vi.fn()
    const html = await render(PairRows, {
      value: `custom:${target}`,
      leftPlaceholder: 'Alias',
      rightOptions: [{ value: 'normal', label: 'Normal model' }],
      rightPlaceholder: 'Model',
      addLabel: 'Add',
      onChange: update,
    })
    expect(html).toContain('value="custom"')
    expect(html).toContain(target)
    expect(update).not.toHaveBeenCalled()
  })

  it('keeps numeric selections and a disabled selector trigger', async () => {
    const update = vi.fn()
    const app = createSSRApp({
      render: () =>
        h(
          UiSelect,
          { modelValue: 2, disabled: true, 'onUpdate:modelValue': update },
          {
            default: () => [
              h('option', { value: '', disabled: true }, 'Choose'),
              h('option', { value: 1, disabled: '' }, 'Disabled'),
              h('option', { value: 2, disabled: false }, 'Selected'),
            ],
          },
        ),
    })
    app.directive('tooltip', {})
    const html = await renderToString(app)
    expect(html).toContain('>Selected</span>')
    expect(html).toMatch(/<button[^>]* disabled[ >]/)
    expect(update).not.toHaveBeenCalled()
  })
})

describe('merged configuration editors', () => {
  const accounts: Account[] = ['account-a', 'account-b', 'account-c'].map((id) => ({
    id,
    label: id,
    enabled: true,
    state: 'ready',
    proxy: '',
    egress: '',
    locale: '',
    timezone: '',
    models: [],
    benefit_tier: 'Unknown',
    message: '',
    active_requests: 0,
  }))

  it('keeps a zero warm pool while enforcing a positive slider peak', () => {
    const instance = mountSetup<{
      form: ServiceConfig
      changedFields: Set<keyof ServiceConfig>
      setWorkerRange: (range: number[]) => void
    }>(SettingsPanel, { config, accounts, models: [], loading: false, error: '' })
    cleanups.push(instance.unmount)
    expect(instance.state.changedFields.size).toBe(0)
    instance.state.setWorkerRange([0, 0])
    expect(instance.state.form.warm_worker_limit).toBe(0)
    expect(instance.state.form.max_active_workers).toBe(1)
    expect(instance.state.changedFields.size).toBe(0)
    instance.state.setWorkerRange([2, 4])
    expect(instance.state.form.warm_worker_limit).toBe(2)
    expect(instance.state.form.max_active_workers).toBe(4)
  })

  it('locks capacity fields individually instead of disabling the whole group', async () => {
    route.section = 'capacity'
    const html = await render(SettingsPanel, {
      config: { ...config, environment: ['WARM_WORKER_LIMIT'] },
      accounts,
      models: [],
      loading: false,
      error: '',
    })
    expect(html).toMatch(/id="setting-warm_worker_limit"[^>]*disabled/)
    expect(html).not.toMatch(/id="setting-max_active_workers"[^>]*disabled(?:[ >])/)
    expect(html).not.toContain('<fieldset disabled')
  })

  it('preserves selected account priority and blocks edits to an environment-locked order', async () => {
    const change = vi.fn()
    const props = reactive({
      accounts,
      value: 'account-b,account-a',
      disabled: false,
      onChange: change,
    })
    const instance = mountSetup<{
      priority: string[]
      move: (from: number, to: number) => void
      select: (ids: string[]) => void
    }>(AccountOrderList, props)
    cleanups.push(instance.unmount)
    expect(instance.state.priority).toEqual(['account-b', 'account-a'])
    expect(change).not.toHaveBeenCalled()
    instance.state.move(1, 0)
    expect(change).toHaveBeenLastCalledWith('account-a,account-b')
    instance.state.select(['account-c', 'account-a'])
    expect(change).toHaveBeenLastCalledWith('account-a,account-c')
    props.disabled = true
    await nextTick()
    change.mockClear()
    instance.state.move(1, 0)
    instance.state.select(['account-c'])
    expect(change).not.toHaveBeenCalled()
    const html = await render(AccountOrderList, props)
    expect(html).toContain('draggable="false"')
  })

  it('saves model account bindings without dropping local settings or other model bindings', async () => {
    vi.mocked(api.saveConfig).mockImplementation(async (value) => value)
    const props = reactive({
      config: { ...config, model_accounts: 'models/model-a:account-a,model-b:account-b' },
      accounts,
      models: [],
      loading: false,
      error: '',
      onSaved: vi.fn(),
    })
    const instance = mountSetup<{
      setModelAccounts: (model: Model, ids: string[]) => Promise<void>
      setModelChannel: (model: Model, channel: string) => Promise<void>
    }>(ModelsTable, props)
    cleanups.push(instance.unmount)
    const model: Model = { id: 'model-a', name: 'Fixture', methods: ['generateContent'] }
    await instance.state.setModelAccounts(model, ['account-a', 'account-c'])
    expect(api.saveConfig).toHaveBeenCalledWith({
      ...props.config,
      model_accounts: 'model-a:account-a|account-c,model-b:account-b',
    })
    expect(props.onSaved).toHaveBeenCalled()
    vi.mocked(api.saveConfig).mockClear()
    props.config.environment = ['MODEL_ACCOUNTS', 'MODEL_CHANNELS']
    await nextTick()
    await instance.state.setModelAccounts(model, [])
    await instance.state.setModelChannel(model, 'build')
    expect(api.saveConfig).not.toHaveBeenCalled()
  })

  it.each([false, true])(
    'ignores stale path listing responses, including errors=%s',
    async (fail) => {
      let resolve!: (value: PathListing) => void
      let reject!: (error: Error) => void
      const old = new Promise<PathListing>((done, error) => {
        resolve = done
        reject = error
      })
      const latest: PathListing = { path: 'D:\\fixture', parent: 'D:\\', entries: [] }
      vi.mocked(api.paths).mockReturnValueOnce(old).mockResolvedValueOnce(latest)
      const change = vi.fn()
      const instance = mountSetup<{
        listing: PathListing
        pickError: string
        picking: number
        rows: string[]
        load: (path: string) => Promise<void>
        choose: (path: string) => void
      }>(PathList, {
        value: ' D:\\one, D:\\two ',
        inputId: 'path',
        inputClass: '',
        onChange: change,
      })
      cleanups.push(instance.unmount)
      const first = instance.state.load('D:\\old')
      await instance.state.load(latest.path)
      if (fail) reject(new Error('old failure'))
      else resolve({ path: 'D:\\old', parent: '', entries: [] })
      await first
      expect(instance.state.listing).toEqual(latest)
      expect(instance.state.pickError).toBe('')
      expect(change).not.toHaveBeenCalled()
      instance.state.picking = 1
      instance.state.choose(latest.path)
      expect(change).toHaveBeenCalledWith('D:\\one,D:\\fixture')
    },
  )
})

describe('Cookie import form', () => {
  it.each([false, true])(
    'keeps optional blank Cookie region fields visible with help expanded=%s',
    async (expanded) => {
      vi.stubGlobal('navigator', { userAgent: 'offline-fixture' })
      vi.stubGlobal('window', {
        location: new URL('https://console.example.test'),
        isSecureContext: true,
      })
      const app = createSSRApp(AccountsPanel, {
        accounts: [],
        tasks: [],
        chromeImport: false,
        loading: false,
        error: '',
      })
      app.directive('tooltip', {})
      app.mixin({
        created() {
          const { setupState } = this.$ as unknown as { setupState: Record<string, unknown> }
          if (Object.hasOwn(setupState, 'showCookieImport')) {
            setupState.showCookieImport = true
            setupState.cookieHelpOpen = expanded
          }
        },
      })
      const context = { teleports: {} as Record<string, string> }
      await renderToString(app, context)
      const form = context.teleports.body!
      const fields = form.match(/<input(?![^>]*type="file")[^>]*>/g) ?? []
      expect(fields).toHaveLength(3)
      expect(fields.every((field) => field.includes('value=""'))).toBe(true)
      expect(fields.every((field) => !field.includes('required'))).toBe(true)
      if (expanded) expect(form).toContain(messages['accounts.cookieTaskHelp'])
      else expect(form).not.toContain(messages['accounts.cookieTaskHelp'])
      expect(form).toMatch(/<textarea[^>]*required/)
      expect(form).not.toMatch(/<form[^>]*novalidate/)
    },
  )

  it('keeps the required region fields for other account forms without prefilling them', async () => {
    const html = await render(AccountEnvironmentFields, {
      modelValue: { proxy: '', locale: '', timezone: '' },
    })
    const fields = html.match(/<input[^>]*required[^>]*>/g) ?? []
    expect(fields).toHaveLength(2)
    expect(fields.every((field) => field.includes('value=""'))).toBe(true)
  })
})
