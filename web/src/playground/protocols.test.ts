import { afterEach, describe, expect, it, vi } from 'vite-plus/test'
import type { Model } from '@/types'
import { generateCode } from './code'
import { createOpenAIVideo, waitOpenAIVideo } from './media/api'
import { videoCode } from './media/code'
import { liveCode } from './media/live-code'
import { openLiveSocket } from './media/live-socket'
import { realtimeMusicCode } from './media/music-code'
import { speechCode } from './media/speech-code'
import { protocolCall } from './protocols'
import { apiKey, publicAPIBase } from './request'

vi.mock('@/desktop', () => ({ keepPage: () => {} }))
vi.mock('@/i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

vi.mock('@/console', () => ({
  useConsole: () => ({
    models: { value: [{ id: 'build-only', channels: ['build'] }] },
    config: { value: { proxy_api_key: 'first-key,second-key' } },
  }),
}))

const model: Model = {
  id: 'gemini-test',
  name: 'Test model',
  methods: ['generateContent'],
  channels: ['playground', 'build'],
}
const buildModel: Model = { ...model, id: 'build-only', channels: ['build'] }
const input: Parameters<typeof generateCode>[1] = {
  model: buildModel,
  modelID: buildModel.id,
  baseURL: 'https://example.test',
  request: { contents: [{ role: 'user', parts: [{ text: 'hello' }] }], generationConfig: {} },
  protocol: 'openai-chat',
  stream: false,
  images: { count: 1, size: 'auto', quality: 'auto' },
}

afterEach(() => vi.unstubAllGlobals())

describe('fixed public channel URLs', () => {
  it('keeps dual-channel and unknown models on Playground, and routes Build-only models explicitly', () => {
    expect(publicAPIBase(model.id, model)).toBe('/v1')
    expect(publicAPIBase('unknown')).toBe('/v1')
    expect(publicAPIBase(buildModel.id, buildModel)).toBe('/build/v1')
    expect(publicAPIBase('build-only')).toBe('/build/v1')
    expect(publicAPIBase('models/gemini-test-build(high)')).toBe('/build/v1')
    expect(publicAPIBase('models/gemini-test-build-search(high)')).toBe('/build/v1')
    expect(publicAPIBase('models/gemini-test-build(high)-search')).toBe('/build/v1')
    expect(publicAPIBase('assistant', { ...model, channels: ['assistant'] })).toBe('/build/v1')
    expect(publicAPIBase('alias-build', { ...model, channels: ['playground'] })).toBe('/v1')
  })

  it('uses the same catalog channel for anti-truncation variants without rewriting request models', () => {
    const requested = 'models/\u6297\u622a\u65ad-build-only(high)-search'
    expect(publicAPIBase(requested)).toBe('/build/v1')
    const call = protocolCall(
      'openai-chat',
      buildModel,
      requested,
      'key',
      input.request,
      false,
      input.images,
    )
    expect(call.path).toBe('/build/v1/chat/completions')
    expect(call.body).toMatchObject({ model: requested })
  })

  it.each(['openai-chat', 'openai-responses', 'anthropic', 'openai-images'] as const)(
    'uses the same channel for %s requests',
    (protocol) => {
      const call = protocolCall(
        protocol,
        buildModel,
        buildModel.id,
        'key',
        input.request,
        false,
        input.images,
      )
      expect(call.path).toMatch(/^\/build\/v1\//)
      const normal = protocolCall(
        protocol,
        model,
        model.id,
        'key',
        input.request,
        false,
        input.images,
      )
      expect(normal.path).toMatch(/^\/v1\//)
    },
  )

  it('keeps Gemini automatic routing and its explicit SSE format', () => {
    const call = protocolCall(
      'gemini',
      buildModel,
      buildModel.id,
      'key',
      input.request,
      true,
      input.images,
    )
    expect(call.path).toBe('/v1beta/models/build-only:streamGenerateContent?alt=sse')
  })

  it.each(['rest', 'openai-python', 'openai-typescript'] as const)(
    'uses the permitted channel in %s code',
    (language) => {
      expect(generateCode(language, input)).toContain('https://example.test/build/v1')
      expect(generateCode(language, { ...input, model, modelID: model.id })).toContain(
        'https://example.test/v1',
      )
    },
  )

  it('uses one configured key and the Build endpoint for Build-only Live models', () => {
    vi.stubGlobal('window', { location: { href: 'https://example.test/' } })
    vi.stubGlobal(
      'WebSocket',
      class {
        url: string
        constructor(url: URL) {
          this.url = String(url)
        }
      },
    )
    expect(apiKey()).toBe('first-key')
    expect(openLiveSocket('build-only').url).toBe('wss://example.test/build/v1/live?key=first-key')
    expect(openLiveSocket('\u6297\u622a\u65ad-build-only').url).toBe(
      'wss://example.test/build/v1/live?key=first-key',
    )
    expect(openLiveSocket('unknown').url).toBe('wss://example.test/v1/live?key=first-key')
  })

  it.each([
    ['gemini-test', '/v1'],
    ['build-only', '/build/v1'],
    ['models/\u6297\u622a\u65ad-build-only(high)-search', '/build/v1'],
  ])(
    'uses the same channel and first key for %s video creation and content',
    async (modelID, base) => {
      const form = new FormData()
      form.set('model', modelID)
      const video = { id: 'offline/id', model: modelID, status: 'completed' }
      const fetch = vi.fn().mockResolvedValue(Response.json(video))
      vi.stubGlobal('fetch', fetch)
      const signal = new AbortController().signal
      const created = await createOpenAIVideo(form, signal)
      const [path, init] = fetch.mock.calls[0] as [string, RequestInit]
      expect(path).toBe(`${base}/videos`)
      expect((init.headers as Headers).get('x-goog-api-key')).toBe('first-key')
      expect(init.body).toBe(form)
      expect(form.get('model')).toBe(modelID)
      expect(await waitOpenAIVideo(created, signal)).toBe(`${base}/videos/offline%2Fid/content`)
    },
  )

  it.each(['openai-python', 'openai-typescript'] as const)(
    'uses publicAPIBase in %s speech and video snippets',
    (language) => {
      for (const modelID of ['gemini-test', 'build-only', '\u6297\u622a\u65ad-build-only']) {
        const base = `https://example.test${publicAPIBase(modelID)}`
        expect(
          speechCode(language, {
            baseURL: 'https://example.test',
            model: modelID,
            prompt: 'offline',
            temperature: 1,
            speakers: [],
            openAI: { input: 'offline', voice: 'fixture' },
          }),
        ).toContain(base)
        expect(
          videoCode(language, {
            baseURL: 'https://example.test',
            prompt: 'offline',
            settings: {
              model: modelID,
              protocol: 'openai-videos',
              aspectRatio: '16:9',
              durationSeconds: 8,
              resolution: '720p',
            },
            references: [],
            aspectRatios: [],
            durations: [],
            resolutions: [],
          }),
        ).toContain(base)
      }
    },
  )

  it.each(['python', 'typescript'] as const)(
    'uses publicAPIBase in %s Live and realtime music snippets',
    (language) => {
      for (const modelID of ['gemini-test', 'build-only', '\u6297\u622a\u65ad-build-only']) {
        const url = `wss://example.test${publicAPIBase(modelID)}/live`
        expect(
          liveCode(language, {
            baseURL: 'https://example.test',
            setup: { model: modelID, input_modalities: ['text'] },
            prompt: 'offline',
          }),
        ).toContain(url)
        expect(
          realtimeMusicCode(language, {
            baseURL: 'https://example.test',
            model: modelID,
            prompts: [{ text: 'offline', weight: 1 }],
            config: {},
          }),
        ).toContain(url)
      }
    },
  )
})
