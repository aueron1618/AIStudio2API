import { nextTick, reactive, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { api } from '@/api'
import { messages } from '@/locales/en'
import { mountSetup } from '@/test-component'
import type { AccountTask, CookieBrowser } from '@/types'
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
  cookieText: string
  cookieMethod: 'browser' | 'http'
  cookieFiles: { name: string; text: string }[]
  cookieCandidates: Candidate[]
  selectedCookieUsers: string[]
  cookieImportError: string
  cookieDiscoveryError: string
  accountEnvironment: { proxy: string; locale: string; timezone: string }
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

function mount() {
  const taskEvents = vi.fn()
  const refresh = vi.fn()
  const props = reactive({
    accounts: [],
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
  })
  const instance = mountSetup<CookieState>(AccountsPanel, props)
  cleanups.push(instance.unmount)
  instance.state.showCookieImport = true
  return { ...instance, props, taskEvents, refresh }
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.stubGlobal('navigator', { userAgent: 'offline-fixture', language: 'must-not-prefill' })
  vi.stubGlobal('window', {
    location: new URL('https://console.example.test'),
    isSecureContext: true,
    setTimeout,
    clearTimeout,
  })
})
afterEach(() => {
  cleanups.splice(0).forEach((cleanup) => cleanup())
  vi.unstubAllGlobals()
})

describe('background Cookie account tasks', () => {
  it.each(['browser', 'http'] as const)(
    'retains %s inputs across acceptance and panel closure',
    async (method) => {
      const { state, props, taskEvents, refresh } = mount()
      expect(state.accountEnvironment).toEqual({ proxy: '', locale: '', timezone: '' })
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
