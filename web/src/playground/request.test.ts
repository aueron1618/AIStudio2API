import { afterEach, describe, expect, it, vi } from 'vite-plus/test'
import type { Model } from '@/types'
import { generateCode } from './code'
import { createOpenAIVideo, waitOpenAIVideo } from './media/api'
import { pageKindOf } from './media/kinds'
import { generate, protocolCall } from './protocols'
import { countTokensDetailed, publicAPIBase, type GenerateRequest } from './request'

vi.mock('@/desktop', () => ({ keepPage: () => {} }))
vi.mock('@/i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/console', () => ({
  useConsole: () => ({
    models: {
      value: [
        { id: 'build-only', name: 'Build', methods: ['generateContent'], channels: ['build'] },
        {
          id: 'assistant-build',
          name: 'Assistant',
          methods: ['generateContent'],
          channels: ['assistant'],
          capabilities: { assistant_route: true },
          capability_options: { aliases: ['assistant-alias'] },
        },
      ],
    },
    config: { value: { proxy_api_key: 'live-key' } },
  }),
}))

const model: Model = {
  id: 'assistant-build',
  name: 'Assistant',
  methods: ['generateContent'],
  channels: ['assistant'],
  capabilities: { assistant_route: true },
}
const request: GenerateRequest = {
  contents: [{ role: 'user', parts: [{ text: 'hello' }] }],
  generationConfig: {},
}
const images = { count: 1, size: 'auto', quality: 'auto' } as const

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('assistant channel selection', () => {
  it('requires catalog capability and channel metadata instead of trusting a model suffix', () => {
    for (const id of [
      'assistant-build',
      'assistant-alias',
      'models/\u6297\u622a\u65ad-assistant-build-search(high)',
    ])
      expect(publicAPIBase(id)).toBe('/build/v1')
    expect(publicAPIBase(model.id, model)).toBe('/build/v1')
    expect(publicAPIBase('unknown-build')).toBe('/v1')
    expect(publicAPIBase(model.id, { ...model, capabilities: {} })).toBe('/v1')
    expect(publicAPIBase(model.id, { ...model, channels: ['playground', 'assistant'] })).toBe('/v1')
  })

  it.each([
    ['openai-chat', '/chat/completions'],
    ['openai-responses', '/responses'],
    ['anthropic', '/messages'],
  ] as const)(
    'uses the explicit Build entry for %s requests and subpath examples',
    (protocol, suffix) => {
      expect(
        protocolCall(protocol, model, model.id, 'key', request, true, images, undefined, undefined)
          .path,
      ).toBe(`/build/v1${suffix}`)
      expect(
        generateCode('rest', {
          protocol,
          model,
          modelID: model.id,
          baseURL: 'https://example.test/proxy',
          request,
          images,
          stream: true,
          agent: undefined,
          previous: undefined,
        }),
      ).toContain(`https://example.test/proxy/build/v1${suffix}`)
    },
  )
})

describe('video request preservation', () => {
  it('pins the creation endpoint through polling even if the returned model is different', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('window', { setTimeout, clearTimeout })
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({ id: 'job/1', model: 'renamed-model', status: 'queued' }),
      )
      .mockResolvedValueOnce(
        Response.json({ id: 'job/1', model: 'renamed-model', status: 'completed' }),
      )
    vi.stubGlobal('fetch', fetchMock)
    const form = new FormData()
    form.set('model', 'build-only')
    const signal = new AbortController().signal
    const created = await createOpenAIVideo(form, signal)
    const pending = expect(waitOpenAIVideo(created, signal)).resolves.toBe(
      '/build/v1/videos/job%2F1/content',
    )
    await vi.advanceTimersByTimeAsync(5000)
    await pending
    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
      '/build/v1/videos',
      '/build/v1/videos/job%2F1',
    ])
  })

  it('does not start polling when the caller has already cancelled', async () => {
    const controller = new AbortController()
    controller.abort()
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(
      waitOpenAIVideo({ id: 'job', model: 'build-only', status: 'queued' }, controller.signal),
    ).rejects.toMatchObject({ name: 'AbortError' })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('keeps Omni generation in chat without removing dedicated video model pages', () => {
    const video: Model = { ...model, capabilities: { capability_code_20: true } }
    expect(pageKindOf(video)).toBe('video')
    expect(
      pageKindOf({
        ...video,
        capabilities: {
          ...video.capabilities,
          capability_code_1: true,
          secondary_capability_code_14: true,
        },
      }),
    ).toBe('chat')
  })
})

describe('token usage', () => {
  it('retains detailed counts and the automatic Gemini counting endpoint', async () => {
    const counts = {
      totalTokens: 40,
      systemInstructionsTokens: { partTokens: [3], roleTokens: 1 },
      contentTokens: [
        { partTokens: [11], roleTokens: 1 },
        { partTokens: [20, 3], roleTokens: 1 },
      ],
    }
    const fetchMock = vi.fn().mockResolvedValue(Response.json(counts))
    vi.stubGlobal('fetch', fetchMock)
    await expect(
      countTokensDetailed(model.id, 'live-key', request, new AbortController().signal),
    ).resolves.toEqual({ totalTokens: 40, systemTokens: 4, contentTokens: [12, 24] })
    expect(fetchMock).toHaveBeenCalledWith(
      '/v1beta/models/assistant-build:countTokens',
      expect.objectContaining({
        headers: expect.any(Headers),
      }),
    )
    const headers = fetchMock.mock.calls[0]![1].headers as Headers
    expect(headers.get('content-type')).toBe('application/json')
    expect(headers.get('x-goog-api-key')).toBe('live-key')
    const body = fetchMock.mock.calls[0]![1].body as BodyInit
    expect(JSON.parse(await new Response(body).text())).toMatchObject({
      generateContentRequest: { model: 'models/assistant-build', ...request },
    })
  })

  it.each([
    [
      'gemini',
      {
        candidates: [{ content: { parts: [{ text: 'answer' }] } }],
        usageMetadata: { candidatesTokenCount: 7, cachedContentTokenCount: 11 },
      },
    ],
    [
      'openai-chat',
      {
        choices: [{ message: { content: 'answer' } }],
        usage: { completion_tokens: 7, prompt_tokens_details: { cached_tokens: 11 } },
      },
    ],
    [
      'openai-responses',
      {
        output: [{ type: 'message', content: [{ type: 'output_text', text: 'answer' }] }],
        usage: { output_tokens: 7, input_tokens_details: { cached_tokens: 11 } },
      },
    ],
    [
      'anthropic',
      {
        content: [{ type: 'text', text: 'answer' }],
        usage: { output_tokens: 7, cache_read_input_tokens: 11 },
      },
    ],
  ] as const)('retains cached token usage through %s decoding', async (protocol, response) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json(response)))
    const updates: { tokens?: number; cachedTokens?: number }[] = []
    await generate({
      protocol,
      model,
      modelID: model.id,
      request,
      images,
      stream: false,
      apiKey: 'key',
      agent: undefined,
      previous: undefined,
      signal: new AbortController().signal,
      onUpdate: (update) => updates.push(update),
    })
    expect(updates.at(-1)).toMatchObject({ tokens: 7, cachedTokens: 11 })
  })
})
