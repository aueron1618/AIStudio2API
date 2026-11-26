import { createSSRApp, defineComponent, h, nextTick, reactive, ref, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { api, ApiError } from '@/api'
import { useConsole } from '@/console'
import { mountSetup } from '@/test-component'
import type { Account, Model, PathListing, ServiceConfig } from '@/types'
import { messages } from '@/locales/en'
import AccountsPanel from './AccountsPanel.vue'
import AccountEnvironmentFields from './AccountEnvironmentFields.vue'
import AccountOrderList from './AccountOrderList.vue'
import DurationInput from './DurationInput.vue'
import PairRows from './PairRows.vue'
import PathList from './PathList.vue'
import PathPicker from './PathPicker.vue'
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
vi.mock('@/api', async (original) => ({
  ApiError: (await original<typeof import('@/api')>()).ApiError,
  api: {
    firstEventLatency: vi.fn(async () => []),
    config: vi.fn(),
    saveConfig: vi.fn(),
    restartService: vi.fn(),
    paths: vi.fn(),
  },
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
    dispatchEvent: vi.fn(),
  })
})
afterEach(() => {
  cleanups.splice(0).forEach((cleanup) => cleanup())
  useConsole().status.value = null
  useConsole().config.value = null
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
  thinking_guard: false,
  user_message_rewrite: 'off',
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
  active_requests: 0,
  proxy: '',
  active_proxy: '',
  proxy_pool: [],
  init_timeout: '2m0s',
  request_timeout: '5m0s',
  first_event_timeout: '5m0s',
  warm_worker_limit: 0,
  max_active_workers: 1,
  warm_startup_concurrency: 1,
  per_account_concurrency: 1,
  max_account_switches: 3,
  max_quota_switches: 'off',
  account_switch_interval: 'off',
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
  it('includes only the two disabled-by-default roleplay settings in their own section', async () => {
    route.section = 'sillytavern'
    const instance = mountSetup<{
      form: ServiceConfig
      sections: { id: string; fields: (keyof ServiceConfig)[] }[]
      changedFields: Set<keyof ServiceConfig>
    }>(SettingsPanel, { config, accounts: [], models: [], loading: false, error: '' })
    cleanups.push(instance.unmount)
    expect(instance.state.sections.find((section) => section.id === 'sillytavern')!.fields).toEqual(
      ['thinking_guard', 'user_message_rewrite'],
    )
    expect(instance.state.form.thinking_guard).toBe(false)
    expect(instance.state.form.user_message_rewrite).toBe('off')
    expect(instance.state.changedFields.size).toBe(0)
    const html = await render(SettingsPanel, {
      config,
      accounts: [],
      models: [],
      loading: false,
      error: '',
    })
    expect(html).toContain('id="setting-thinking-guard"')
    expect(html).toContain('aria-checked="false"')
    expect(html).toContain('aria-labelledby="setting-user-message-rewrite-label"')
    expect(html).toContain(messages['settings.userMessageRewriteOff'])
  })

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

  it('unlocks only the env-injected local key, without disclosing saved or active secrets', async () => {
    route.section = 'service'
    const html = await render(SettingsPanel, {
      config: {
        ...config,
        proxy_api_key: 'Q7z!,fixture-configured-key',
        active_proxy_api_key: 'fixture-active-only',
        api_key_accounts: 'Q7z!:account-a',
        environment: ['PROXY_API_KEY', 'API_KEY_ACCOUNTS', 'LISTEN_ADDR'],
      },
      accounts: [{ id: 'account-a', label: 'Fixture account' }],
      models: [],
      loading: false,
      error: '',
    })
    const locked = html.match(/<fieldset disabled[\s\S]*?<\/fieldset>/g) ?? []
    expect(locked).toHaveLength(2)
    expect(locked[0]).toContain('id="setting-listen"')
    expect(locked[1]).toContain('aria-labelledby="setting-api-key-accounts-label"')
    expect(html).toContain('id="setting-api-key"')
    expect(html).not.toContain(
      messages['settings.environmentLocked'].replace('{key}', 'PROXY_API_KEY'),
    )
    for (const secret of ['Q7z!', 'fixture-configured-key', 'fixture-active-only']) {
      expect(html).not.toContain(secret)
    }
  })

  it.each([
    ['service', 'auto-start', 'launch'],
    ['service', 'preview-listen', 'management'],
    ['service', 'auth-path', 'service'],
    ['service', 'api-key', 'immediate'],
    ['runtime', 'waa-backend', 'service'],
    ['runtime', 'channel-build', 'immediate'],
    ['runtime', 'request-timeout', 'immediate'],
    ['models', 'strip-thought-signatures', 'immediate'],
    ['network', 'proxy', 'immediate'],
    ['security', 'admin-auth', 'immediate'],
    ['security', 'admin-password', 'immediate'],
  ] as const)('labels %s/%s as %s', async (section, id, effect) => {
    route.section = section
    const html = await render(SettingsPanel, {
      config,
      accounts: [],
      models: [],
      loading: false,
      error: '',
    })
    const label = `for="setting-${id}"`
    expect(html).toContain(label)
    const row = html.slice(html.indexOf(label)).split('</fieldset>')[0]!
    expect(row).toContain(messages[`settings.effect.${effect}`])
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

  function editor(overrides: Partial<ServiceConfig> = {}) {
    const props = reactive({
      config: { ...config, ...overrides },
      accounts,
      models: [],
      loading: false,
      error: '',
      onSaved: (saved: ServiceConfig) => {
        props.config = saved
      },
      onNotice: vi.fn(),
    })
    const instance = mountSetup<{
      form: ServiceConfig
      changedFields: Set<keyof ServiceConfig>
      restartDialog: boolean
      restartPending: boolean
      restartImpact: string
      setKeys: (keys: string) => void
      setKeyAccounts: (key: string, ids: string[]) => void
      saveConfig: () => Promise<void>
      restartService: () => Promise<void>
    }>(SettingsPanel, props)
    cleanups.push(instance.unmount)
    return { state: instance.state, props }
  }

  it('rotates and clears environment-provided keys and their editable bindings in one save', async () => {
    const { state, props } = editor({ environment: ['PROXY_API_KEY'] })
    vi.mocked(api.saveConfig).mockImplementation(async (value) => ({
      ...value,
      active_proxy_api_key: value.proxy_api_key,
    }))
    state.setKeys('second-key,replacement-key')
    state.setKeyAccounts('replacement-key', ['account-c', 'account-b'])
    await state.saveConfig()
    expect(api.saveConfig).toHaveBeenCalledTimes(1)
    expect(api.saveConfig).toHaveBeenCalledWith(
      expect.objectContaining({
        proxy_api_key: 'second-key,replacement-key',
        api_key_accounts: 'replacement-key:account-c|account-b',
      }),
    )
    expect(props.config.active_proxy_api_key).toBe('second-key,replacement-key')
    state.setKeys('')
    await state.saveConfig()
    expect(api.saveConfig).toHaveBeenCalledTimes(2)
    expect(api.saveConfig).toHaveBeenLastCalledWith(
      expect.objectContaining({ proxy_api_key: '', api_key_accounts: '' }),
    )
    expect(window.dispatchEvent).not.toHaveBeenCalled()
    expect(state.restartDialog).toBe(false)
    expect(props.onNotice).toHaveBeenLastCalledWith(messages['settings.saved'], 'success')
  })

  it('preserves environment-locked bindings when the key list changes', async () => {
    const bindings = 'first-key:account-a,next-key:account-b'
    const { state } = editor({
      api_key_accounts: bindings,
      environment: ['API_KEY_ACCOUNTS', 'PROXY_API_KEY'],
    })
    vi.mocked(api.saveConfig).mockImplementation(async (value) => value)
    state.setKeys('next-key')
    state.setKeyAccounts('next-key', [])
    expect([...state.changedFields]).toEqual(['proxy_api_key'])
    await state.saveConfig()
    expect(api.saveConfig).toHaveBeenCalledWith(
      expect.objectContaining({ proxy_api_key: 'next-key', api_key_accounts: bindings }),
    )
  })

  it('does not rewrite dormant bindings or prompt for an existing restart on an unrelated hot save', async () => {
    const bindings = 'dormant-key:account-b,first-key:account-a'
    const { state } = editor({ api_key_accounts: bindings, service_restart_required: true })
    vi.mocked(api.saveConfig).mockImplementation(async (value) => value)
    await nextTick()
    expect(state.changedFields.size).toBe(0)
    state.form.max_quota_switches = '0'
    state.form.account_switch_interval = '5'
    await state.saveConfig()
    expect(api.saveConfig).toHaveBeenCalledWith(
      expect.objectContaining({
        api_key_accounts: bindings,
        proxy_api_key: config.proxy_api_key,
        max_quota_switches: '0',
        account_switch_interval: '5',
      }),
    )
    expect(state.restartDialog).toBe(false)
    expect(state.restartPending).toBe(true)
    expect(window.dispatchEvent).not.toHaveBeenCalled()
  })

  it('shows restart impact for a saved service change and restarts only on confirmation', async () => {
    const { state, props } = editor()
    vi.mocked(api.saveConfig).mockImplementation(async (value) => ({
      ...value,
      active_requests: 2,
      service_restart_required: true,
    }))
    state.form.waa_backend = 'camoufox'
    await state.saveConfig()
    expect(state.restartDialog).toBe(true)
    expect(state.restartPending).toBe(true)
    expect(state.restartImpact).toBe(messages['settings.restartInterrupts'].replace('{count}', '2'))
    expect(api.restartService).not.toHaveBeenCalled()
    vi.mocked(api.restartService).mockResolvedValue({
      state: 'RUNNING',
      running: true,
      ready: true,
      version: 'fixture',
      active_requests: 0,
      accounts: { total: 0, ready: 0, busy: 0, cooldown: 0, auth_required: 0 },
      camoufox: { ready: false, preparing: false },
      chrome_import: false,
      time: new Date().toISOString(),
    })
    await state.restartService()
    expect(api.restartService).toHaveBeenCalledTimes(1)
    expect(state.restartDialog).toBe(false)
    expect(state.restartPending).toBe(false)
    expect(props.onNotice).toHaveBeenLastCalledWith(
      messages['settings.serviceRestarted'],
      'success',
    )
  })

  it('keeps edits after a conflict and refreshes the admin session only after a successful save', async () => {
    const { state, props } = editor()
    vi.mocked(api.saveConfig)
      .mockRejectedValueOnce(new ApiError('fixture conflict', 409, 'conflict'))
      .mockImplementationOnce(async (value) => ({ ...value, management_restart_required: true }))
    state.form.listen_addr = ':3050'
    state.form.admin_username = 'new-admin'
    await state.saveConfig()
    expect(props.onNotice).toHaveBeenLastCalledWith('ApiError: fixture conflict', 'error')
    expect(props.config.admin_username).toBe(config.admin_username)
    expect(state.changedFields.has('admin_username')).toBe(true)
    expect(api.config).not.toHaveBeenCalled()
    expect(window.dispatchEvent).not.toHaveBeenCalled()
    await state.saveConfig()
    expect(window.dispatchEvent).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ type: 'admin-session-changed' }),
    )
    expect(props.onNotice).toHaveBeenLastCalledWith(messages['settings.savedManagement'], 'success')
    expect(state.restartDialog).toBe(false)
  })

  it('reloads saved config and the session on a partial hot-apply failure without claiming success', async () => {
    const { state, props } = editor()
    const saved = {
      ...config,
      admin_username: 'new-admin',
      proxy_api_key: 'new-key',
      active_proxy_api_key: 'new-key',
      api_key_accounts: '',
    }
    vi.mocked(api.config).mockResolvedValueOnce(saved)
    vi.mocked(api.saveConfig).mockRejectedValueOnce(
      new ApiError('fixture proxy unavailable', 500, 'config_saved_apply_failed'),
    )
    state.form.admin_username = saved.admin_username
    state.setKeys(saved.proxy_api_key)
    await state.saveConfig()
    expect(api.config).toHaveBeenCalledTimes(1)
    expect(useConsole().config.value).toEqual(saved)
    expect(window.dispatchEvent).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ type: 'admin-session-changed' }),
    )
    expect(props.onNotice).toHaveBeenCalledExactlyOnceWith(
      expect.stringContaining('fixture proxy unavailable'),
      'error',
    )
  })

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
        failure: string
        load: (path: string) => Promise<void>
      }>(PathPicker, { open: false, start: '' })
      cleanups.push(instance.unmount)
      const first = instance.state.load('D:\\old')
      await instance.state.load(latest.path)
      if (fail) reject(new Error('old failure'))
      else resolve({ path: 'D:\\old', parent: '', entries: [] })
      await first
      expect(instance.state.listing).toEqual(latest)
      expect(instance.state.failure).toBe('')
      expect(change).not.toHaveBeenCalled()
      const editor = mountSetup<{ choose: (index: number, path: string) => void }>(PathList, {
        value: ' D:\\one, D:\\two ',
        inputId: 'path',
        inputClass: '',
        onChange: change,
      })
      cleanups.push(editor.unmount)
      editor.state.choose(1, latest.path)
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
        setTimeout,
        clearTimeout,
        setInterval,
        clearInterval,
      })
      const app = createSSRApp(AccountsPanel, {
        accounts: [],
        tasks: [],
        chromeImport: false,
        loading: false,
        error: '',
        camoufox: undefined,
        models: [],
        cooldowns: [],
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

  it('keeps all account fields blank and allows upstream exit-based region defaults', async () => {
    const html = await render(AccountEnvironmentFields, {
      modelValue: { proxy: '', locale: '', timezone: '' },
    })
    const fields = html.match(/<input[^>]*>/g) ?? []
    expect(fields).toHaveLength(3)
    expect(fields.every((field) => field.includes('value=""'))).toBe(true)
    expect(fields.every((field) => !field.includes('required'))).toBe(true)
    expect(fields.filter((field) => field.includes(messages['accounts.byExit']))).toHaveLength(2)
  })
})
