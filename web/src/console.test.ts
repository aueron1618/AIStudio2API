import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { api, openAdminEvents } from './api'
import type { AccountTask } from './types'

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
  vi.stubGlobal('window', {
    setTimeout,
    clearTimeout,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })
  vi.mocked(api.status).mockResolvedValue({ state: 'STOPPED', version: 'fixture' } as Awaited<
    ReturnType<typeof api.status>
  >)
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
  it('loads accepted tasks without mounting the Accounts panel and reloads after reconnect', async () => {
    const state = await start()
    expect(state.accountTasks.value).toEqual([running])
    vi.mocked(api.accountTasks).mockResolvedValue([{ ...running, id: 'accepted-after-abort' }])
    vi.mocked(openAdminEvents).mock.calls[0]![1]()
    await nextTick()
    expect(state.accountTasks.value[0]!.id).toBe('accepted-after-abort')
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
