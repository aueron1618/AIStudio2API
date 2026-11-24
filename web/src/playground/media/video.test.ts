import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { api } from '@/api'
import { createOpenAIVideo, createVideo, waitOpenAIVideo, waitVideo } from './api'
import { defaultSettings, frameURL, useVideo, type VideoData, type VideoFrame } from './video'
import { localAttachment } from '../media-store'
import type { Model } from '@/types'

vi.mock('@/desktop', () => ({ keepPage: () => {} }))
vi.mock('@/i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api', () => ({
  api: { savePrompt: vi.fn(), deletePrompt: vi.fn(), uploadPromptMedia: vi.fn() },
}))
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

function loadFrame(frame: VideoFrame, id = 'saved-video'): void {
  video.load(id, {
    title: 'Original',
    description: 'Fixture description',
    data: {
      kind: 'video',
      settings: { ...video.state.settings },
      turns: [{ id: 'input', role: 'user', created: 0, prompt: 'Fixture', start: frame }],
    },
  })
}

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
  vi.mocked(api.uploadPromptMedia).mockResolvedValue('frame-media')
  vi.stubGlobal('window', { location: new URL('https://example.test') })
  vi.stubGlobal(
    'FileReader',
    class {
      result = ''
      onload: (() => void) | null = null
      abort = vi.fn()
      readAsDataURL(blob: Blob): void {
        void blob.arrayBuffer().then((bytes) => {
          this.result = `data:${blob.type};base64,${Buffer.from(bytes).toString('base64')}`
          this.onload?.()
        })
      }
    },
  )
  video.newSession()
  video.ensureModel()
  video.reset()
})
afterEach(() => {
  video.newSession()
  vi.restoreAllMocks()
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

  it.each(['name', 'mimeType'] as const)(
    'coalesces saves and preserves live frame %s edits during an upload',
    async (field) => {
      const original = { name: 'start.png', mimeType: 'image/png', data: 'eA==' }
      loadFrame(original)
      let finish!: (id: string) => void
      vi.mocked(api.uploadPromptMedia).mockReturnValueOnce(
        new Promise((resolve) => {
          finish = resolve
        }),
      )
      const first = video.save()
      await vi.waitFor(() => expect(api.uploadPromptMedia).toHaveBeenCalledTimes(1))
      video.state.title = 'Intermediate'
      const second = video.save()
      const edited = field === 'name' ? 'renamed.png' : 'image/jpeg'
      video.state.turns[0]!.start![field] = edited
      video.state.title = 'Latest'
      const latest = video.save()
      expect(api.savePrompt).not.toHaveBeenCalled()
      finish('first-media')
      await Promise.all([first, second, latest])
      expect(api.savePrompt).toHaveBeenCalledTimes(2)
      const snapshots = vi.mocked(api.savePrompt).mock.calls.map(([, prompt]) => prompt)
      expect(snapshots.map((prompt) => prompt.title)).toEqual(['Original', 'Latest'])
      const stored = (snapshots[1]!.data as VideoData).turns[0]!.start!
      expect(stored[field]).toBe(edited)
      expect(stored.data).toBe('')
      expect(stored.mediaId).toBe(field === 'name' ? 'first-media' : 'frame-media')
      expect(video.state.turns[0]!.start).toMatchObject(stored)
      expect(api.uploadPromptMedia).toHaveBeenCalledTimes(field === 'name' ? 1 : 2)
      expect(original).toEqual({ name: 'start.png', mimeType: 'image/png', data: 'eA==' })
      expect(video.state.description).toBe('Fixture description')
    },
  )

  it('waits for an in-flight media save and drops queued writes before deleting a video', async () => {
    const frame = Object.assign(
      localAttachment(new Blob(['x'], { type: 'image/png' })).inlineData!,
      {
        name: 'start.png',
      },
    )
    loadFrame(frame, 'delete-video')
    let finish!: (id: string) => void
    vi.mocked(api.uploadPromptMedia).mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve
      }),
    )
    const saving = video.save()
    await vi.waitFor(() => expect(api.uploadPromptMedia).toHaveBeenCalledTimes(1))
    video.state.title = 'Queued'
    const queued = video.save()
    const deleting = video.remove()
    expect(api.deletePrompt).not.toHaveBeenCalled()
    finish('delete-frame')
    await Promise.all([saving, queued, deleting])
    expect(api.savePrompt).toHaveBeenCalledTimes(1)
    expect(api.deletePrompt).toHaveBeenCalledWith('delete-video')
    expect(vi.mocked(api.deletePrompt).mock.invocationCallOrder[0]).toBeGreaterThan(
      vi.mocked(api.savePrompt).mock.invocationCallOrder[0]!,
    )
    expect(video.state.id).toBeNull()
    expect(video.state.turns).toEqual([])
  })

  it('hydrates a stored frame with its declared MIME while keeping the submitted input snapshot', async () => {
    const frame = { name: 'stored.png', mimeType: 'image/png', data: '', mediaId: 'stored/frame' }
    expect(frameURL(frame)).toBe('/api/prompts/media/stored%2Fframe')
    video.setStart(frame)
    video.state.draft = 'Submitted prompt'
    let finish!: (response: Response) => void
    const fetchMock = vi.fn().mockReturnValue(
      new Promise<Response>((resolve) => {
        finish = resolve
      }),
    )
    vi.stubGlobal('fetch', fetchMock)
    vi.mocked(createVideo).mockResolvedValue({ name: 'operations/stored' })
    vi.mocked(waitVideo).mockResolvedValue(['/v1beta/files/stored:download?alt=media'])
    const running = video.run()
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    video.state.draft = 'Later input'
    video.state.settings.durationSeconds = 4
    finish(new Response(new Blob(['real-image'], { type: 'application/octet-stream' })))
    await running
    expect(createVideo).toHaveBeenCalledWith(
      'video-build',
      {
        instances: [
          {
            prompt: 'Submitted prompt',
            image: { bytesBase64Encoded: 'cmVhbC1pbWFnZQ==', mimeType: 'image/png' },
          },
        ],
        parameters: { sampleCount: 1, aspectRatio: '16:9', durationSeconds: 8, resolution: '720p' },
      },
      expect.any(AbortSignal),
    )
    expect(video.state.turns[0]!.start).toMatchObject({ data: '', mediaId: 'stored/frame' })
    expect(api.uploadPromptMedia).not.toHaveBeenCalled()
  })

  it('recycles local frame previews without converting them to inline history', () => {
    const create = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:frame')
    const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined)
    const frame = Object.assign(
      localAttachment(new Blob(['x'], { type: 'image/png' })).inlineData!,
      {
        name: 'local.png',
      },
    )
    video.setStart(frame)
    expect(frameURL(frame)).toBe('blob:frame')
    expect(frameURL(frame)).toBe('blob:frame')
    expect(create).toHaveBeenCalledTimes(1)
    expect(frame.data).toBe('')
    video.setStart(undefined)
    expect(revoke).toHaveBeenCalledWith('blob:frame')
  })
})
