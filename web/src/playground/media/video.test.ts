import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { api } from '@/api'
import { createOpenAIVideo, createVideo, waitOpenAIVideo, waitVideo } from './api'
import { defaultSettings, useVideo, type VideoData } from './video'
import type { Model } from '@/types'

vi.mock('@/desktop', () => ({ keepPage: () => {} }))
vi.mock('@/i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api', () => ({ api: { savePrompt: vi.fn(), deletePrompt: vi.fn() } }))
vi.mock('../session', () => ({ newTurnID: () => crypto.randomUUID() }))
vi.mock('../title', () => ({ requestTitle: vi.fn().mockResolvedValue('') }))
vi.mock('./api', () => ({
  createOpenAIVideo: vi.fn(),
  waitOpenAIVideo: vi.fn(),
  createVideo: vi.fn(),
  waitVideo: vi.fn(),
}))
vi.mock('@/console', async () => {
  const { ref } = await import('vue')
  return {
    useConsole: () => ({
      models: ref([
        {
          id: 'video-build',
          name: 'Video',
          channels: ['build'],
          methods: ['predictLongRunning'],
          capabilities: { capability_code_20: true },
          capability_options: {
            video_aspect_ratios: ['16:9', '9:16'],
            video_output_resolutions: ['1080p', '720p'],
            video_durations_seconds: ['4', '8'],
            video_inputs: ['frames', 'reference_images'],
          },
        },
      ]),
      showNotice: vi.fn(),
    }),
  }
})

const video = useVideo()

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.savePrompt).mockResolvedValue({
    id: 'saved-video',
    title: '',
    model: 'video-build',
    description: '',
    created_at: '',
    updated_at: '',
  })
  vi.mocked(api.deletePrompt).mockResolvedValue(undefined)
  vi.stubGlobal('window', { location: new URL('https://example.test') })
  video.newSession()
  video.ensureModel()
  video.reset()
})
afterEach(() => {
  video.newSession()
  vi.unstubAllGlobals()
})

describe('retained video sessions', () => {
  it('retains model defaults, frame constraints and both video protocols', () => {
    const defaults = defaultSettings(video.currentModel.value)
    expect(defaults).toMatchObject({ protocol: 'gemini', resolution: '720p', durationSeconds: 8 })
    video.state.draft = 'fixture'
    video.state.settings.durationSeconds = 4
    video.setLast({ name: 'last.png', mimeType: 'image/png', data: 'eA==' })
    expect(video.state.settings.durationSeconds).toBe(8)
    expect(video.canRun.value).toBe(true)
    video.state.settings.protocol = 'openai-videos'
    expect(video.openAIFrames.value).toBe(true)
    expect(video.canRun.value).toBe(false)
    video.setLast(undefined)
    expect(video.canRun.value).toBe(true)
  })

  it('saves resumable Gemini jobs and the final same-origin download path', async () => {
    vi.mocked(createVideo).mockResolvedValue({ name: 'operations/video' })
    vi.mocked(waitVideo).mockResolvedValue([
      'https://example.test/v1beta/files/video:download?alt=media',
    ])
    video.state.draft = 'fixture video'
    video.state.start = { name: 'start.png', mimeType: 'image/png', data: 'eA==' }
    await video.run()
    expect(createVideo).toHaveBeenCalledWith(
      'video-build',
      expect.objectContaining({
        instances: [
          { prompt: 'fixture video', image: { bytesBase64Encoded: 'eA==', mimeType: 'image/png' } },
        ],
      }),
      expect.any(AbortSignal),
    )
    expect(api.savePrompt).toHaveBeenCalledWith(
      expect.any(String),
      expect.objectContaining({ turns: 2, data: expect.objectContaining({ kind: 'video' }) }),
    )
    expect(video.state.turns.at(-1)).toMatchObject({
      videos: ['/v1beta/files/video:download?alt=media'],
      task: { protocol: 'gemini', id: 'operations/video' },
    })
    expect(video.state.running).toBe(false)
  })

  it('persists the OpenAI job model and explicit request channel', async () => {
    vi.mocked(createOpenAIVideo).mockResolvedValue({
      id: 'job',
      model: 'video-build',
      status: 'queued',
      base: '/build/v1',
    })
    vi.mocked(waitOpenAIVideo).mockResolvedValue('/build/v1/videos/job/content')
    video.state.settings.protocol = 'openai-videos'
    video.state.draft = 'fixture video'
    await video.run()
    const form = vi.mocked(createOpenAIVideo).mock.calls[0]![0]
    expect(form.get('model')).toBe('video-build')
    expect(form.get('size')).toBe('1280x720')
    expect(video.state.turns.at(-1)).toMatchObject({
      task: { protocol: 'openai-videos', id: 'job', model: 'video-build', base: '/build/v1' },
      videos: ['/build/v1/videos/job/content'],
    })
  })

  it('resumes saved jobs on their original channel instead of current settings', async () => {
    vi.mocked(waitOpenAIVideo).mockResolvedValue('/build/v1/videos/job/content')
    const data: VideoData = {
      kind: 'video',
      settings: defaultSettings({
        id: 'different-model',
        name: 'Other',
        methods: [],
      } satisfies Model),
      turns: [
        {
          id: 'turn',
          role: 'model',
          created: Date.now(),
          task: { protocol: 'openai-videos', id: 'job', model: 'video-build', base: '/build/v1' },
        },
      ],
    }
    video.load('saved-video', { title: 'Saved', description: '', data })
    expect(waitOpenAIVideo).toHaveBeenCalledWith(
      { id: 'job', model: 'video-build', status: 'queued', base: '/build/v1' },
      expect.any(AbortSignal),
    )
    await vi.waitFor(() => expect(video.state.running).toBe(false))
    expect(video.state.turns.at(-1)?.videos).toEqual(['/build/v1/videos/job/content'])
    expect(api.deletePrompt).not.toHaveBeenCalled()
  })
})
