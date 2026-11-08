import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { api } from '@/api'
import { mountSetup } from '@/test-component'
import type { UpdateProgress, UpdateStatus } from '@/types'
import VersionPanel from './VersionPanel.vue'

vi.mock('@/api', () => ({ api: { updateStatus: vi.fn(), applyUpdate: vi.fn() } }))
vi.mock('@/markdown', () => ({ markdown: { render: (text: string) => text } }))
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
  error: string
}

const status: UpdateStatus = {
  current: 'fixture-current',
  latest: 'fixture-next',
  mode: 'binary',
  available: true,
  updating: false,
}
let unmount: (() => void) | undefined

function mount() {
  const restarting = vi.fn()
  const instance = mountSetup<VersionState>(VersionPanel, {
    status,
    activeRequests: 0,
    onRestarting: restarting,
  })
  unmount = instance.unmount
  return { state: instance.state, restarting }
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.useFakeTimers()
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
    expect(api.updateStatus).toHaveBeenCalledTimes(3)
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
