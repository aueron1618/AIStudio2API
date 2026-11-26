import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { api } from '@/api'
import { mountSetup } from '@/test-component'
import type { UpdateProgress, UpdateStatus } from '@/types'
import VersionPanel from './VersionPanel.vue'

vi.mock('@/api', () => ({ api: { updateStatus: vi.fn(), applyUpdate: vi.fn() } }))
vi.mock('@/i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
    locale: { value: 'en' },
    errorText: (error: Error) => error.message,
    logText: (text: string) => text,
  }),
}))

interface VersionState {
  install: () => Promise<void>
  phase: string
  progress: UpdateProgress | null
  percent: number | null
  summary: string
  checkMessage: string
  retryText: string
  error: string
}

const status: UpdateStatus = {
  current: 'fixture-current',
  latest: 'fixture-next',
  mode: 'binary',
  available: true,
  updating: false,
  log_dir: 'D:\\fixture\\logs',
}
let unmount: (() => void) | undefined

function mount(overrides: Partial<UpdateStatus> = {}) {
  const restarting = vi.fn()
  const instance = mountSetup<VersionState>(VersionPanel, {
    status: { ...status, ...overrides },
    activeRequests: 0,
    restartStalled: false,
    onRestarting: restarting,
  })
  unmount = instance.unmount
  return { state: instance.state, restarting }
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.useFakeTimers()
  vi.stubGlobal('window', { clearInterval, setInterval, clearTimeout, setTimeout })
  vi.stubGlobal('requestAnimationFrame', vi.fn().mockReturnValue(1))
  vi.stubGlobal('cancelAnimationFrame', vi.fn())
})
afterEach(() => {
  unmount?.()
  unmount = undefined
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('update progress integration', () => {
  it('renders normalized release notes as escaped text', async () => {
    const app = createSSRApp({
      render: () =>
        h(VersionPanel, {
          status: { ...status, notes: '<b>fixture</b>\n\nSecond paragraph' },
          activeRequests: 0,
          restartStalled: false,
        }),
    })
    app.directive('tooltip', {})
    const html = await renderToString(app)
    expect(html).toMatch(/&lt;b&gt;fixture&lt;\/b&gt;<\/p><p[^>]*>Second paragraph<\/p>/)
    expect(html).not.toContain('<b>fixture</b>')
  })

  it('shows the rate-limit summary and optional retry clock without fetching', async () => {
    const { state } = mount({
      error: 'fixture limit',
      error_kind: 'rate_limited',
      retry_at: new Date(Date.now() + 121_000).toISOString(),
    })
    expect(state.checkMessage).toBe('version.rateLimited')
    expect(state.retryText).toBe('version.retryAt')
    await vi.advanceTimersByTimeAsync(121_000)
    expect(state.retryText).toBe('')
    expect(api.updateStatus).not.toHaveBeenCalled()
  })

  it('shows download, verification and installation progress, then stops polling on restart', async () => {
    let complete!: (value: UpdateStatus) => void
    vi.mocked(api.applyUpdate).mockReturnValue(
      new Promise((resolve) => {
        complete = resolve
      }),
    )
    const { state, restarting } = mount()
    const installing = state.install()
    expect(state.percent).toBeNull()
    for (const [stage, summary, percent] of [
      ['download', 'version.downloading', 25],
      ['verify', 'version.verifying', 100],
      ['install', 'version.installing', 100],
      ['restart', 'version.restarting', 100],
    ] as const) {
      vi.mocked(api.updateStatus).mockResolvedValue({
        ...status,
        updating: true,
        progress: { stage, downloaded: 25, total: 100 },
      })
      await vi.advanceTimersByTimeAsync(250)
      expect(state.summary).toBe(summary)
      expect(state.percent).toBe(percent)
    }
    complete({ ...status, updating: true })
    await installing
    expect(state.phase).toBe('restarting')
    expect(restarting).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.updateStatus).toHaveBeenCalledTimes(4)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('tolerates failed polls, avoids overlapping polls and ignores late progress after installation failure', async () => {
    let fail!: (reason: Error) => void
    let progress!: (value: UpdateStatus) => void
    vi.mocked(api.applyUpdate).mockReturnValue(
      new Promise((_resolve, reject) => {
        fail = reject
      }),
    )
    vi.mocked(api.updateStatus)
      .mockRejectedValueOnce(new Error('offline progress'))
      .mockReturnValueOnce(
        new Promise((resolve) => {
          progress = resolve
        }),
      )
    const { state, restarting } = mount()
    const installing = state.install()
    await vi.advanceTimersByTimeAsync(250)
    expect(state.phase).toBe('installing')
    expect(state.error).toBe('')
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.updateStatus).toHaveBeenCalledTimes(2)
    fail(new Error('installation failed'))
    await installing
    progress({ ...status, progress: { stage: 'download', downloaded: 50, total: 100 } })
    await vi.advanceTimersByTimeAsync(250)
    expect(state.phase).toBe('idle')
    expect(state.error).toBe('installation failed')
    expect(state.progress).toBeNull()
    expect(restarting).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('releases the progress timer when the panel is unmounted', async () => {
    let complete!: (value: UpdateStatus) => void
    vi.mocked(api.applyUpdate).mockReturnValue(
      new Promise((resolve) => {
        complete = resolve
      }),
    )
    const { state } = mount()
    const installing = state.install()
    unmount!()
    unmount = undefined
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.updateStatus).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
    complete({ ...status, updating: true })
    await installing
  })
})
