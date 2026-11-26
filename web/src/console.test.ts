import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { api, openAdminEvents } from './api'
import type { AccountTask } from './types'
import { readTab, useTabParam } from './url'
import { mountSetup } from './test-component'
import { readPreference, writePreference } from './preference'

vi.mock('@/api', () => ({
  api: {
    status: vi.fn(),
    models: vi.fn(),
    accounts: vi.fn(),
    requests: vi.fn(),
    cooldowns: vi.fn(),
    config: vi.fn(),
    updateStatus: vi.fn(),
    accountTasks: vi.fn(),
  },
  openAdminEvents: vi.fn(),
}))
vi.mock('@/i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, errorText: (error: Error) => error.message }),
}))

const running: AccountTask = {
  id: 'fixture-task',
  kind: 'cookies',
  state: 'running',
  items: [
    { label: '', state: 'pending' },
    { label: '', state: 'pending' },
  ],
  started_at: '2026-11-04T00:00:00Z',
}
let stop: (() => void) | undefined

beforeEach(() => {
  vi.resetModules()
  vi.resetAllMocks()
  const storage = new Map<string, string>()
  vi.stubGlobal('window', {
    location: new URL('https://console.example.test/'),
    localStorage: {
      getItem: vi.fn((key: string) => storage.get(key) ?? null),
      setItem: vi.fn((key: string, value: string) => storage.set(key, value)),
    },
    setTimeout,
    clearTimeout,
    setInterval: vi.fn(() => 1),
    clearInterval: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })
  vi.mocked(api.status).mockResolvedValue({
    state: 'STOPPED',
    running: false,
    ready: false,
    version: 'fixture',
    active_requests: 0,
    accounts: { total: 0, ready: 0, busy: 0, cooldown: 0, auth_required: 0 },
    camoufox: { ready: false, preparing: false },
    chrome_import: false,
    time: new Date().toISOString(),
  })
  vi.mocked(api.accounts).mockResolvedValue([])
  vi.mocked(api.models).mockResolvedValue([])
  vi.mocked(api.requests).mockResolvedValue([])
  vi.mocked(api.cooldowns).mockResolvedValue([])
  vi.mocked(api.config).mockResolvedValue({} as Awaited<ReturnType<typeof api.config>>)
  vi.mocked(api.updateStatus).mockResolvedValue({
    current: 'fixture',
    mode: 'development',
    available: false,
    updating: false,
    log_dir: 'D:\\fixture\\logs',
  })
  vi.mocked(api.accountTasks).mockResolvedValue([running])
  vi.mocked(openAdminEvents).mockReturnValue({ close: vi.fn() } as unknown as EventSource)
})
afterEach(() => {
  stop?.()
  stop = undefined
  vi.unstubAllGlobals()
})

async function start() {
  const state = (await import('./console')).useConsole()
  stop = state.stopConsole
  await state.startConsole()
  return state
}

describe('shared account task recovery', () => {
  it('does not open SSE when initialization finishes after leaving the console', async () => {
    let finish!: (tasks: AccountTask[]) => void
    vi.mocked(api.accountTasks).mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve
      }),
    )
    const state = (await import('./console')).useConsole()
    stop = state.stopConsole
    const starting = state.startConsole()
    state.stopConsole()
    finish([running])
    await starting
    expect(openAdminEvents).not.toHaveBeenCalled()
    expect(window.setInterval).not.toHaveBeenCalled()
    expect(state.config.value).toBeNull()
    expect(state.loading.config).toBe(true)
  })

  it('ignores events and reconnect callbacks from a closed console generation', async () => {
    const state = await start()
    const [oldReceive, oldReconnect] = vi.mocked(openAdminEvents).mock.calls[0]!
    state.stopConsole()
    expect(state.config.value).toBeNull()
    expect(state.status.value).toBeNull()
    expect(state.accountTasks.value).toEqual([])
    await state.startConsole()
    const reads = vi.mocked(api.accountTasks).mock.calls.length
    oldReceive({
      type: 'account_tasks',
      data: {
        tasks: [
          {
            ...running,
            state: 'failed',
            items: [{ label: '', state: 'failed', code: 'account_task_canceled' }],
          },
        ],
      },
    })
    oldReconnect()
    expect(state.accountTasks.value).toEqual([running])
    expect(api.accountTasks).toHaveBeenCalledTimes(reads)
    expect(openAdminEvents).toHaveBeenCalledTimes(2)
  })

  it('polls Camoufox preparation only while the console is active', async () => {
    vi.mocked(api.accountTasks).mockResolvedValue([])
    const state = await start()
    expect(window.setInterval).not.toHaveBeenCalled()
    state.status.value = {
      ...state.status.value!,
      camoufox: { ready: false, preparing: true, download: { received: 1, total: 10 } },
    }
    expect(window.setInterval).toHaveBeenCalledTimes(1)
    state.stopConsole()
    expect(window.clearInterval).toHaveBeenLastCalledWith(1)
    state.status.value = { ...state.status.value!, camoufox: { ready: true, preparing: false } }
    state.status.value = { ...state.status.value!, camoufox: { ready: false, preparing: true } }
    expect(window.setInterval).toHaveBeenCalledTimes(1)
    await state.startConsole()
    expect(window.setInterval).toHaveBeenCalledTimes(2)
    expect(window.clearInterval).toHaveBeenLastCalledWith(1)
  })

  it('loads accepted tasks without mounting the Accounts panel and reloads after reconnect', async () => {
    const state = await start()
    expect(state.accountTasks.value).toEqual([running])
    vi.mocked(api.accountTasks).mockResolvedValue([{ ...running, id: 'accepted-after-abort' }])
    vi.mocked(openAdminEvents).mock.calls[0]![1]()
    await nextTick()
    expect(state.accountTasks.value[0]!.id).toBe('accepted-after-abort')
  })

  it('shares one active-only poller for account tasks and Camoufox preparation', async () => {
    const state = await start()
    expect(window.setInterval).toHaveBeenCalledTimes(1)
    state.status.value = { ...state.status.value!, camoufox: { ready: false, preparing: true } }
    expect(window.setInterval).toHaveBeenCalledTimes(1)
    state.accountTasks.value = []
    expect(window.setInterval).toHaveBeenCalledTimes(1)
    state.status.value = { ...state.status.value!, camoufox: { ready: true, preparing: false } }
    expect(window.clearInterval).toHaveBeenLastCalledWith(1)
    state.stopConsole()
    state.accountTasks.value = [running]
    expect(window.setInterval).toHaveBeenCalledTimes(1)
  })

  it('tracks a 202 task immediately but never overwrites a newer SSE terminal result', async () => {
    const state = await start()
    const next = { ...running, id: 'second-task' }
    state.rememberAccountTask(next)
    expect(state.accountTasks.value.some((task) => task.id === next.id)).toBe(true)
    const terminal: AccountTask = {
      ...next,
      state: 'failed',
      items: [{ label: '', state: 'failed', code: 'account_task_canceled' }],
    }
    vi.mocked(openAdminEvents).mock.calls[0]![0]({
      type: 'account_tasks',
      data: { tasks: [terminal] },
    })
    state.rememberAccountTask(next)
    expect(state.accountTasks.value).toEqual([terminal])
  })

  it('ignores an older GET snapshot that finishes after an SSE update', async () => {
    const state = await start()
    let resolve!: (tasks: AccountTask[]) => void
    vi.mocked(api.accountTasks).mockReturnValueOnce(
      new Promise((done) => {
        resolve = done
      }),
    )
    const request = state.loadAccountTasks()
    const done: AccountTask = {
      ...running,
      state: 'done',
      items: [{ label: 'verified@example.test', state: 'done' }],
    }
    vi.mocked(openAdminEvents).mock.calls[0]![0]({ type: 'account_tasks', data: { tasks: [done] } })
    resolve([running])
    await request
    expect(state.accountTasks.value).toEqual([done])
  })

  it('refreshes partial successes even while another item is running or later fails', async () => {
    await start()
    vi.mocked(api.accounts).mockClear()
    const partial: AccountTask = {
      ...running,
      items: [
        { label: 'verified@example.test', state: 'done' },
        { label: '', state: 'running' },
      ],
    }
    const receive = vi.mocked(openAdminEvents).mock.calls[0]![0]
    receive({ type: 'account_tasks', data: { tasks: [partial] } })
    await nextTick()
    expect(api.accounts).toHaveBeenCalledTimes(1)
    receive({
      type: 'account_tasks',
      data: {
        tasks: [
          {
            ...partial,
            state: 'failed',
            items: [partial.items[0]!, { label: '', state: 'failed' }],
          },
        ],
      },
    })
    await nextTick()
    expect(api.accounts).toHaveBeenCalledTimes(2)
  })
})

describe('portal tab preferences', () => {
  const tabs = ['logs', 'accounts', 'usage', 'version', 'access'] as const

  it('preserves account and channel when switching a remembered management tab', () => {
    const pushState = vi.fn()
    vi.stubGlobal('window', {
      localStorage: window.localStorage,
      addEventListener: window.addEventListener,
      removeEventListener: window.removeEventListener,
      location: new URL(
        'https://example.test/console?tab=accounts&account=fixture&channel=build&state=running&model=hidden&state=failed#summary',
      ),
      history: { pushState },
    })
    const instance = mountSetup<{ tab: string; select: (value: string) => void }>(
      { setup: () => useTabParam(['logs', 'accounts', 'usage'] as const, 'logs', 'app.tab') },
      {},
    )
    stop = instance.unmount
    expect(instance.state.tab).toBe('accounts')
    instance.state.select('usage')
    const url = new URL(pushState.mock.calls[0]![2] as string)
    expect(url.searchParams.get('tab')).toBe('usage')
    expect(url.searchParams.get('account')).toBe('fixture')
    expect(url.searchParams.get('channel')).toBe('build')
    expect(url.searchParams.has('state')).toBe(false)
    expect(url.searchParams.has('model')).toBe(false)
    expect(url.hash).toBe('#summary')
  })

  it('restores the last valid tab on a new portal visit', () => {
    expect(readTab(tabs, 'logs', 'app.tab')).toBe('logs')
    writePreference('app.tab', 'usage')
    expect(readTab(tabs, 'logs', 'app.tab')).toBe('usage')
  })

  it('lets an explicit tab link override the saved selection', () => {
    writePreference('app.tab', 'usage')
    window.location.search = '?tab=accounts'
    expect(readTab(tabs, 'logs', 'app.tab')).toBe('accounts')
    window.location.search = '?tab=invalid'
    expect(readTab(tabs, 'logs', 'app.tab')).toBe('logs')
  })

  it('rejects corrupt or no longer valid saved tabs', () => {
    writePreference('app.tab', 'removed-tab')
    expect(readTab(tabs, 'logs', 'app.tab')).toBe('logs')
    window.localStorage.setItem('app.tab', '{invalid-json')
    expect(readTab(tabs, 'logs', 'app.tab')).toBe('logs')
  })

  it('keeps the portal usable when browser storage is unavailable', () => {
    vi.spyOn(window.localStorage, 'getItem').mockImplementation(() => {
      throw new Error('storage disabled')
    })
    vi.spyOn(window.localStorage, 'setItem').mockImplementation(() => {
      throw new Error('storage disabled')
    })
    expect(readTab(tabs, 'logs', 'app.tab')).toBe('logs')
    expect(readPreference('app.sidebarCollapsed', false, () => true)).toBe(false)
    expect(() => writePreference('app.tab', 'usage')).not.toThrow()
  })
})
