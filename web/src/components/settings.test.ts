import { createSSRApp, h, ref, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { afterEach, describe, expect, it, vi } from 'vite-plus/test'
import type { ServiceConfig } from '@/types'
import { messages } from '@/locales/en'
import AccountsPanel from './AccountsPanel.vue'
import AccountEnvironmentFields from './AccountEnvironmentFields.vue'
import DurationInput from './DurationInput.vue'
import PairRows from './PairRows.vue'
import SettingRow from './SettingRow.vue'
import SettingsPanel from './SettingsPanel.vue'
import UiSelect from './UiSelect.vue'

const route = vi.hoisted(() => ({ section: 'upstream' }))

vi.mock('@/desktop', () => ({
  keepPage: () => () => {},
  desktopPlatform: '',
  sendDesktop: vi.fn(),
}))
vi.mock('@/api', () => ({ api: {} }))
vi.mock('@/url', () => ({ useQueryParam: () => ref(route.section) }))
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

afterEach(() => vi.unstubAllGlobals())

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
  routing_strategy: 'round-robin',
  upstream_channels: ['playground', 'build'],
  model_mapping: '',
  model_channels: '',
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
    route.section = 'upstream'
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
    expect(html).toContain(messages['settings.fixedChannelHelp'])
    expect(html).not.toContain(messages['settings.upstreamChannelsHelp'])
  })

  it('locks each local switch using its own environment key', async () => {
    route.section = 'upstream'
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

  it('preserves the zero-warm-pool value and minimum', async () => {
    route.section = 'capacity'
    const html = await render(SettingsPanel, {
      config,
      accounts: [],
      models: [],
      loading: false,
      error: '',
    })
    expect(html).toMatch(/id="setting-warm"[^>]*value="0"[^>]*min="0"/)
  })

  it.each([false, true])(
    'preserves Build-native nonstream=%s and its own environment lock',
    async (enabled) => {
      route.section = 'upstream'
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
    },
  )

  it('locks strip_thought_signatures and no longer offers a temporary-chat toggle', async () => {
    route.section = 'upstream'
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
