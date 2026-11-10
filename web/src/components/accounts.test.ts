import { createSSRApp, nextTick, reactive, ref } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { api } from '@/api'
import { messages } from '@/locales/en'
import { mountSetup } from '@/test-component'
import type {
  Account,
  AccountDraft,
  AccountLoginInput,
  AccountTask,
  ChromeImportProfile,
  CookieBrowser,
} from '@/types'
import AccountsPanel from './AccountsPanel.vue'

vi.mock('@/desktop', () => ({ keepPage: () => () => {} }))
vi.mock('@/api', async (original) => {
  const actual = await original<typeof import('@/api')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      discoverCookieAccounts: vi.fn(),
      importCookieAccounts: vi.fn(),
      dismissAccountTask: vi.fn(),
      chromeImportProfiles: vi.fn(),
      importChromeAccounts: vi.fn(),
      refreshAccount: vi.fn(),
    },
  }
})
vi.mock('@/i18n', () => ({
  useI18n: () => ({
    t: (key: keyof typeof messages) => messages[key],
    locale: { value: 'en' },
    errorText: (error: Error) => error.message,
    logText: (text: string) => text,
  }),
}))
vi.mock('./AnchoredMenu.vue', () => ({
  default: {},
  useAnchoredMenu: () => ({ open: ref(false), target: ref(null), toggle: () => {} }),
}))

type Candidate = CookieBrowser & { source: number }
interface CookieState {
  showCookieImport: boolean
  cookieHelpOpen: boolean
  cookieText: string
  cookieMethod: 'browser' | 'http'
  cookieFiles: { name: string; text: string }[]
  cookieCandidates: Candidate[]
  selectedCookieUsers: string[]
  toggleAllCookieCandidates: () => void
  allCookieCandidatesSelected: boolean
  cookieImportError: string
  cookieDiscoveryError: string
  cookieEnvironment: AccountLoginInput
  accountEnvironment: AccountLoginInput
  draft: AccountDraft
  showBrowserImport: boolean
  cookieBrowsers: CookieBrowser[]
  selectedBrowsers: string[]
  importBrowserAccounts: () => Promise<void>
  showChromeImport: boolean
  chromeProfiles: ChromeImportProfile[]
  selectedChromeProfiles: string[]
  openChromeImport: () => Promise<void>
  importChromeAccounts: () => Promise<void>
  toggleAllChromeProfiles: () => void
  transitionText: (account: Account) => string
  refreshAccounts: (accounts: Account[]) => Promise<void>
  toggleAccountSelected: (id: string) => void
  bulkDelete: () => void
  importCookieAccounts: (direct?: boolean) => Promise<void>
  discoverCookieAccounts: () => Promise<void>
  closeCookieImport: () => void
  clearCookieInput: () => void
  readCookieFiles: (event: Event) => Promise<void>
  dismissTask: (task: AccountTask) => Promise<void>
  viewerTaskID: string
  viewerUnavailable: () => void
  cancelViewerTask: () => void
}

const candidate = (source: number, imported = false): Candidate => ({
  source,
  address: '',
  browser: '',
  email: `fixture-${source}@example.test`,
  auth_user: 0,
  locale: '',
  timezone: '',
  imported,
  signed_out: false,
  device_bound: false,
  error: '',
})
const queued = (count = 1): AccountTask => ({
  id: 'fixture-task',
  kind: 'cookies',
  state: 'running',
  items: Array.from({ length: count }, () => ({ label: '', state: 'pending' })),
  started_at: '2026-11-04T00:00:00Z',
})
const cleanups: (() => void)[] = []
const preferences = new Map<string, string>()
const storage = {
  getItem: vi.fn<(key: string) => string | null>(),
  setItem: vi.fn<(key: string, value: string) => void>(),
}

function mount() {
  const taskEvents = vi.fn()
  const refresh = vi.fn()
  const props = reactive({
    accounts: [] as Account[],
    tasks: [] as AccountTask[],
    loading: false,
    error: '',
    chromeImport: false,
    onTask: (task: AccountTask) => {
      taskEvents(task)
      props.tasks = [task]
    },
    onRefreshTasks: refresh,
    onRefresh: vi.fn(),
    onNotice: vi.fn(),
    onDelete: vi.fn(),
  })
  const instance = mountSetup<CookieState>(AccountsPanel, props)
  cleanups.push(instance.unmount)
  instance.state.showCookieImport = true
  return { ...instance, props, taskEvents, refresh }
}

async function renderImport(
  browser = false,
  help = false,
  method: CookieState['cookieMethod'] = 'browser',
): Promise<string> {
  const app = createSSRApp(AccountsPanel, {
    accounts: [],
    tasks: [],
    loading: false,
    error: '',
    chromeImport: false,
  })
  app.directive('tooltip', {})
  app.mixin({
    created() {
      const { setupState } = this.$ as unknown as { setupState: CookieState }
      if (!Object.hasOwn(setupState, 'showCookieImport')) return
      if (browser) setupState.showBrowserImport = true
      else {
        setupState.showCookieImport = true
        setupState.cookieHelpOpen = help
        setupState.cookieMethod = method
      }
    },
  })
  const context = { teleports: {} as Record<string, string> }
  await renderToString(app, context)
  return context.teleports.body!
}

beforeEach(() => {
  vi.resetAllMocks()
  preferences.clear()
  storage.getItem.mockImplementation((key) => preferences.get(key) ?? null)
  storage.setItem.mockImplementation((key, value) => {
    preferences.set(key, value)
  })
  vi.stubGlobal('navigator', { userAgent: 'offline-fixture', language: 'must-not-prefill' })
  vi.stubGlobal('window', {
    location: new URL('https://console.example.test'),
    isSecureContext: true,
    setTimeout,
    clearTimeout,
    localStorage: storage,
  })
})
afterEach(() => {
  cleanups.splice(0).forEach((cleanup) => cleanup())
  vi.unstubAllGlobals()
})

describe('Cookie import preferences and layout', () => {
  it('starts blank without preferences and does not remember untouched defaults', () => {
    const { state } = mount()
    expect(state.cookieEnvironment).toEqual({ proxy: '', locale: '', timezone: '' })
    expect(storage.getItem).toHaveBeenCalledWith('accounts.cookie.locale')
    expect(storage.getItem).toHaveBeenCalledWith('accounts.cookie.timezone')
    expect(storage.setItem).not.toHaveBeenCalled()
  })

  it('remembers only edited language and timezone across reopening and remounting', () => {
    const { state } = mount()
    state.cookieEnvironment.locale = 'fr-CA'
    expect([...preferences]).toEqual([['accounts.cookie.locale', '"fr-CA"']])
    state.cookieEnvironment.timezone = 'America/Toronto'
    state.cookieEnvironment.proxy = 'socks5://fixture-user:fixture-secret@proxy.example.test:1080'
    state.cookieText = 'fixture-cookie=offline-secret'
    state.cookieFiles = [{ name: 'fixture-private.txt', text: 'fixture-file=offline-secret' }]
    state.cookieCandidates = [candidate(0)]
    state.selectedCookieUsers = ['0:0']
    expect([...preferences]).toEqual([
      ['accounts.cookie.locale', '"fr-CA"'],
      ['accounts.cookie.timezone', '"America/Toronto"'],
    ])
    expect(storage.setItem).toHaveBeenCalledTimes(2)
    state.closeCookieImport()
    state.cookieEnvironment.locale = ''
    state.cookieEnvironment.timezone = ''
    state.showCookieImport = true
    expect(state.cookieEnvironment.locale).toBe('fr-CA')
    expect(state.cookieEnvironment.timezone).toBe('America/Toronto')
    expect(storage.setItem).toHaveBeenCalledTimes(2)
    state.clearCookieInput()
    expect(preferences.size).toBe(2)
    cleanups.pop()!()
    const reopened = mount().state
    expect(reopened.cookieEnvironment).toEqual({
      proxy: '',
      locale: 'fr-CA',
      timezone: 'America/Toronto',
    })
    expect(reopened.cookieText).toBe('')
    expect(reopened.cookieFiles).toEqual([])
    expect(reopened.cookieCandidates).toEqual([])
    expect(storage.setItem).toHaveBeenCalledTimes(2)
  })

  it('remembers an explicit field clear without overwriting other import or edit settings', () => {
    preferences.set('accounts.cookie.locale', '"fr-CA"')
    preferences.set('accounts.cookie.timezone', '"America/Toronto"')
    const { state } = mount()
    state.accountEnvironment.locale = 'de-DE'
    state.accountEnvironment.timezone = 'Europe/Berlin'
    state.draft.locale = 'ko-KR'
    state.draft.timezone = 'Asia/Seoul'
    state.cookieEnvironment.locale = ''
    state.closeCookieImport()
    state.accountEnvironment.locale = 'ja-JP'
    state.showCookieImport = true
    expect(state.cookieEnvironment).toEqual({ proxy: '', locale: '', timezone: 'America/Toronto' })
    expect(state.accountEnvironment).toEqual({
      proxy: '',
      locale: 'ja-JP',
      timezone: 'Europe/Berlin',
    })
    expect(state.draft.locale).toBe('ko-KR')
    expect(state.draft.timezone).toBe('Asia/Seoul')
    expect([...preferences]).toEqual([
      ['accounts.cookie.locale', '""'],
      ['accounts.cookie.timezone', '"America/Toronto"'],
    ])
  })

  it('ignores invalid preferences and tolerates unavailable browser storage', () => {
    preferences.set('accounts.cookie.locale', 'false')
    preferences.set('accounts.cookie.timezone', '{invalid-json')
    const { state } = mount()
    expect(state.cookieEnvironment).toEqual({ proxy: '', locale: '', timezone: '' })
    state.closeCookieImport()
    storage.getItem.mockImplementation(() => {
      throw new Error('Storage unavailable')
    })
    state.showCookieImport = true
    storage.setItem.mockImplementation(() => {
      throw new Error('Storage unavailable')
    })
    expect(() => (state.cookieEnvironment.locale = 'en-US')).not.toThrow()
    expect(state.cookieEnvironment.locale).toBe('en-US')
  })

  it.each(['browser', 'http'] as const)(
    'uses remembered region only for the %s Cookie flow',
    async (method) => {
      preferences.set('accounts.cookie.locale', '"fr-CA"')
      preferences.set('accounts.cookie.timezone', '"America/Toronto"')
      const { state } = mount()
      expect(state.accountEnvironment).toEqual({ proxy: '', locale: '', timezone: '' })
      state.cookieText = 'fixture-cookie=offline'
      state.cookieMethod = method
      vi.mocked(api.importCookieAccounts).mockResolvedValue(queued())
      await state.importCookieAccounts(true)
      expect(api.importCookieAccounts).toHaveBeenCalledWith({
        items: [{ email: '', cookies: 'fixture-cookie=offline', cdp: '', auth_user: 0, method }],
        proxy: '',
        locale: 'fr-CA',
        timezone: 'America/Toronto',
      })
      expect(storage.setItem).not.toHaveBeenCalled()
    },
  )

  it('keeps browser-discovery imports HTTP-only and does not apply Cookie region preferences', async () => {
    preferences.set('accounts.cookie.locale', '"fr-CA"')
    preferences.set('accounts.cookie.timezone', '"America/Toronto"')
    const { state } = mount()
    state.closeCookieImport()
    state.showBrowserImport = true
    state.cookieBrowsers = [{ ...candidate(0), address: 'http://browser.example.test:9222' }]
    state.selectedBrowsers = ['http://browser.example.test:9222#0']
    vi.mocked(api.importCookieAccounts).mockResolvedValue(queued())
    await state.importBrowserAccounts()
    expect(api.importCookieAccounts).toHaveBeenCalledWith({
      items: [
        {
          email: 'fixture-0@example.test',
          cookies: '',
          cdp: 'http://browser.example.test:9222',
          auth_user: 0,
          method: 'http',
        },
      ],
      proxy: '',
      locale: '',
      timezone: '',
    })
    expect(storage.setItem).not.toHaveBeenCalled()
  })

  it.each([false, true])('keeps settings visible while help is expanded=%s', async (help) => {
    const form = await renderImport(false, help, 'http')
    const fields = form.match(/<input(?![^>]*type="file")[^>]*>/g) ?? []
    expect(fields).toHaveLength(3)
    expect(fields.every((field) => field.includes('value=""') && !field.includes('required'))).toBe(
      true,
    )
    expect(form).toContain('id="cookie-settings-title"')
    expect(form).toContain('<div id="cookie-import-help">')
    expect(form).toContain('grid-cols-1 gap-4 md:grid-cols-2')
    expect(form).toMatch(
      new RegExp(
        `<button[^>]*type="button"[^>]*aria-expanded="${help}"[^>]*aria-controls="cookie-import-help"`,
      ),
    )
    expect(form).toMatch(/<textarea[^>]*autocomplete="off"[^>]*required/)
    for (const key of [
      'accounts.cookieSecurity',
      'accounts.cookieTaskHelp',
      'accounts.cookieEnvironmentHelp',
      'accounts.cookieMethodHttpHelp',
    ] as const) {
      if (help) expect(form).toContain(messages[key])
      else expect(form).not.toContain(messages[key])
    }
  })

  it('keeps browser import proxy settings visible without a disclosure', async () => {
    const form = await renderImport(true)
    const fields = form.match(/<input[^>]*>/g) ?? []
    expect(fields).toHaveLength(1)
    expect(form).toMatch(/<h4[^>]*>More settings<\/h4>/)
    expect(form).not.toContain('ui-collapse')
  })
})

describe('background Cookie account tasks', () => {
  it.each(['browser', 'http'] as const)(
    'retains %s inputs across acceptance and panel closure',
    async (method) => {
      const { state, props, taskEvents, refresh } = mount()
      expect(state.accountEnvironment).toEqual({ proxy: '', locale: '', timezone: '' })
      expect(state.cookieEnvironment).toEqual({ proxy: '', locale: '', timezone: '' })
      expect(state.cookieMethod).toBe('browser')
      state.cookieMethod = method
      state.cookieText = 'fixture-cookie=offline'
      let accept!: (task: AccountTask) => void
      vi.mocked(api.importCookieAccounts).mockReturnValue(
        new Promise((resolve) => {
          accept = resolve
        }),
      )
      const posting = state.importCookieAccounts(true)
      state.closeCookieImport()
      expect(state.cookieText).toBe('fixture-cookie=offline')
      expect(api.dismissAccountTask).not.toHaveBeenCalled()
      accept(queued())
      await posting
      await nextTick()
      expect(taskEvents).toHaveBeenCalledWith(queued())
      expect(refresh).toHaveBeenCalled()
      expect(props.tasks[0]!.state).toBe('running')
      expect(state.cookieText).toBe('fixture-cookie=offline')
      expect(api.importCookieAccounts).toHaveBeenCalledWith({
        items: [{ email: '', cookies: 'fixture-cookie=offline', cdp: '', auth_user: 0, method }],
        proxy: '',
        locale: '',
        timezone: '',
      })
      props.tasks = [
        { ...queued(), state: 'done', items: [{ label: 'verified@example.test', state: 'done' }] },
      ]
      await nextTick()
      expect(state.cookieText).toBe('')
      expect(api.dismissAccountTask).not.toHaveBeenCalled()
    },
  )

  it('keeps file source identity and imported markers during discovery', async () => {
    const { state } = mount()
    state.cookieFiles = [
      { name: 'first.txt', text: 'first=offline' },
      { name: 'second.txt', text: 'second=offline' },
    ]
    vi.mocked(api.discoverCookieAccounts)
      .mockResolvedValueOnce([candidate(0, true)])
      .mockResolvedValueOnce([candidate(1)])
    await state.discoverCookieAccounts()
    expect(state.cookieCandidates.map((item) => `${item.source}:${item.auth_user}`)).toEqual([
      '0:0',
      '1:0',
    ])
    expect(state.selectedCookieUsers).toEqual(['1:0'])
    state.cookieMethod = 'http'
    vi.mocked(api.importCookieAccounts).mockResolvedValue(queued())
    await state.importCookieAccounts()
    expect(api.importCookieAccounts).toHaveBeenCalledWith({
      items: [
        {
          email: 'fixture-1@example.test',
          cookies: 'second=offline',
          cdp: '',
          auth_user: 0,
          method: 'http',
        },
      ],
      proxy: '',
      locale: '',
      timezone: '',
    })
  })

  it('never selects or submits existing identities from raw Cookie text or files', async () => {
    const { state } = mount()
    state.cookieFiles = [{ name: 'state.json', text: '{"cookies":[]}' }]
    vi.mocked(api.discoverCookieAccounts).mockResolvedValue([candidate(0, true)])
    await state.discoverCookieAccounts()
    expect(state.selectedCookieUsers).toEqual([])
    state.toggleAllCookieCandidates()
    expect(state.selectedCookieUsers).toEqual([])
    expect(state.allCookieCandidatesSelected).toBe(false)
    state.selectedCookieUsers = ['0:0']
    await state.importCookieAccounts()
    expect(api.importCookieAccounts).not.toHaveBeenCalled()
    expect(state.cookieFiles).toHaveLength(1)
  })

  it('keeps CDP imports from overwriting existing identities', async () => {
    const { state } = mount()
    state.cookieBrowsers = [{ ...candidate(0, true), address: 'http://browser.example.test:9222' }]
    state.selectedBrowsers = ['http://browser.example.test:9222#0']
    await state.importBrowserAccounts()
    expect(api.importCookieAccounts).not.toHaveBeenCalled()
  })

  it('allows Chrome reimport selection while preserving source ID and task acceptance', async () => {
    const { state, taskEvents, refresh } = mount()
    const profile: ChromeImportProfile = {
      id: 'existing-source',
      email: 'existing@example.test',
      profile: 'Default',
      display_name: 'Existing account',
      locale: 'en',
    }
    vi.mocked(api.chromeImportProfiles).mockResolvedValue([profile])
    vi.mocked(api.importChromeAccounts).mockResolvedValue({ ...queued(), kind: 'chrome' })
    await state.openChromeImport()
    state.toggleAllChromeProfiles()
    expect(state.selectedChromeProfiles).toEqual([profile.id])
    await state.importChromeAccounts()
    expect(api.importChromeAccounts).toHaveBeenCalledWith({
      accounts: [{ id: profile.id, email: profile.email }],
      proxy: '',
      locale: '',
      timezone: '',
    })
    expect(state.showChromeImport).toBe(false)
    expect(taskEvents).toHaveBeenCalledWith({ ...queued(), kind: 'chrome' })
    expect(refresh).toHaveBeenCalled()
  })

  it('shows asynchronous transitions and keeps batch deletion separate from refreshing', async () => {
    const { state, props } = mount()
    const account: Account = {
      id: 'fixture-account',
      label: 'Fixture',
      enabled: true,
      state: 'ready',
      proxy: '',
      egress: '',
      locale: '',
      timezone: '',
      models: [],
      benefit_tier: 'Unknown',
      message: '',
      active_requests: 2,
    }
    const draining: Account = { ...account, id: 'draining', transition: 'draining' }
    const syncing: Account = { ...account, id: 'syncing', transition: 'syncing' }
    expect(state.transitionText(draining)).toBe(
      messages['accounts.drainingRequests'].replace('{count}', '2'),
    )
    expect(state.transitionText({ ...draining, active_requests: 0 })).toBe(
      messages['accounts.draining'],
    )
    expect(state.transitionText(syncing)).toBe(messages['accounts.syncing'])
    expect(state.transitionText(account)).toBe('')
    await state.refreshAccounts([draining, syncing])
    expect(api.refreshAccount).not.toHaveBeenCalled()
    props.accounts = [account, draining]
    await nextTick()
    state.toggleAccountSelected(account.id)
    state.toggleAccountSelected(draining.id)
    state.bulkDelete()
    expect(props.onDelete).toHaveBeenCalledWith([account, draining])
  })

  it('retains failed input for a method switch and marks only confirmed successes', async () => {
    const { state, props } = mount()
    state.cookieFiles = [
      { name: 'first.txt', text: 'first=offline' },
      { name: 'second.txt', text: 'second=offline' },
    ]
    state.cookieCandidates = [candidate(0), candidate(1)]
    state.selectedCookieUsers = ['0:0', '1:0']
    state.cookieMethod = 'http'
    vi.mocked(api.importCookieAccounts).mockResolvedValue(queued(2))
    await state.importCookieAccounts()
    await nextTick()
    expect(state.cookieCandidates.every((item) => !item.imported)).toBe(true)
    props.tasks = [
      {
        ...queued(2),
        state: 'failed',
        items: [
          { label: 'verified@example.test', state: 'done' },
          { label: '', state: 'failed', code: 'fixture_failure', error: 'redacted fixture reason' },
        ],
      },
    ]
    await nextTick()
    expect(state.cookieCandidates.map((item) => item.imported)).toEqual([true, false])
    expect(state.selectedCookieUsers).toEqual(['1:0'])
    expect(state.cookieFiles).toHaveLength(2)
    expect(state.cookieImportError).toBe('redacted fixture reason')
    state.cookieMethod = 'browser'
    await nextTick()
    expect(state.cookieImportError).toBe('')
    expect(state.selectedCookieUsers).toEqual(['1:0'])
  })

  it('aborts obsolete discovery without issuing DELETE or losing source input', async () => {
    const { state } = mount()
    state.cookieText = 'fixture-cookie=offline'
    vi.mocked(api.discoverCookieAccounts).mockImplementation(
      (_input, signal) =>
        new Promise((_resolve, reject) => {
          signal!.addEventListener('abort', () => reject(signal!.reason))
        }),
    )
    const finding = state.discoverCookieAccounts()
    state.closeCookieImport()
    await finding
    expect(state.cookieDiscoveryError).toBe('')
    expect(state.cookieText).toBe('fixture-cookie=offline')
    expect(api.dismissAccountTask).not.toHaveBeenCalled()
  })

  it('refreshes accepted tasks after a failed HTTP response without implicit cancellation', async () => {
    const { state, props, refresh } = mount()
    state.cookieText = 'fixture-cookie=offline'
    vi.mocked(api.importCookieAccounts).mockRejectedValue(
      new DOMException('Aborted response', 'AbortError'),
    )
    await state.importCookieAccounts(true)
    expect(refresh).toHaveBeenCalled()
    expect(state.cookieText).toBe('fixture-cookie=offline')
    props.tasks = [queued()]
    await nextTick()
    expect(props.tasks[0]!.state).toBe('running')
    expect(api.dismissAccountTask).not.toHaveBeenCalled()
  })

  it('uses DELETE for explicit cancellation and refreshes partial results', async () => {
    const { state, props, refresh } = mount()
    vi.mocked(api.dismissAccountTask).mockResolvedValue()
    await state.dismissTask(queued())
    expect(api.dismissAccountTask).toHaveBeenCalledWith('fixture-task')
    expect(refresh).toHaveBeenCalled()
    expect(props.onRefresh).toHaveBeenCalled()
  })

  it('keeps accepted login tasks visible after a viewer connection failure', async () => {
    const { state, props } = mount()
    const task: AccountTask = { ...queued(), kind: 'login', view: 'fixture-view' }
    props.tasks = [task]
    state.viewerTaskID = task.id
    await nextTick()
    state.viewerUnavailable()
    expect(state.viewerTaskID).toBe('')
    expect(props.tasks).toEqual([task])
    expect(props.onNotice).toHaveBeenCalled()
    expect(api.dismissAccountTask).not.toHaveBeenCalled()
    state.cancelViewerTask()
    expect(api.dismissAccountTask).not.toHaveBeenCalled()
    vi.mocked(api.dismissAccountTask).mockResolvedValue()
    await state.dismissTask(task)
    expect(api.dismissAccountTask).toHaveBeenCalledWith(task.id)
  })

  it('keeps cancellation pending until DELETE releases the task and suppresses duplicate clicks', async () => {
    const { state, refresh } = mount()
    let released!: () => void
    vi.mocked(api.dismissAccountTask).mockReturnValue(
      new Promise((resolve) => {
        released = resolve
      }),
    )
    const first = state.dismissTask(queued())
    const duplicate = state.dismissTask(queued())
    expect(api.dismissAccountTask).toHaveBeenCalledTimes(1)
    expect(refresh).not.toHaveBeenCalled()
    released()
    await Promise.all([first, duplicate])
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('rejects over-limit UTF-8 input and files before any task or discovery request', async () => {
    const { state } = mount()
    state.cookieText = '\u4e2d'.repeat(350000)
    await state.importCookieAccounts(true)
    expect(state.cookieImportError).toBe(messages['accounts.cookieTooLarge'])
    await state.readCookieFiles({
      target: { files: [new File(['x'.repeat((1 << 20) + 1)], 'large.txt')], value: 'fixture' },
    } as unknown as Event)
    expect(state.cookieImportError).toContain('large.txt')
    expect(api.importCookieAccounts).not.toHaveBeenCalled()
    expect(api.discoverCookieAccounts).not.toHaveBeenCalled()
  })
})
