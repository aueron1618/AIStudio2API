import { afterEach, describe, expect, it, vi } from 'vite-plus/test'
import type { Model } from '@/types'
import { generateCode } from './code'
import { createOpenAIVideo, waitOpenAIVideo } from './media/api'
import { videoCode } from './media/code'
import { liveCode } from './media/live-code'
import { openLiveSocket } from './media/live-socket'
import { realtimeMusicCode } from './media/music-code'
import { speechCode } from './media/speech-code'
import { protocolCall, protocolsFor } from './protocols'
import { agentConfig, apiKey, buildRequest, publicAPIBase } from './request'
import { defaultSettings } from './session'

vi.mock('@/desktop', () => ({ keepPage: () => {} }))
vi.mock('@/i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

vi.mock('@/console', () => ({
  useConsole: () => ({
    models: {
      value: [
        { id: 'build-only', name: 'Build', methods: ['generateContent'], channels: ['build'] },
      ],
    },
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
  agent: undefined,
  previous: undefined,
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
    expect(
      publicAPIBase(requested, { id: requested, name: 'Alias', methods: ['generateContent'] }),
    ).toBe('/build/v1')
    const call = protocolCall(
      'openai-chat',
      buildModel,
      requested,
      'key',
      input.request,
      false,
      input.images,
      undefined,
      undefined,
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
        undefined,
        undefined,
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
        undefined,
        undefined,
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
      undefined,
      undefined,
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

describe('Interactions and current media capabilities', () => {
  const agent: Model = {
    ...model,
    id: 'antigravity',
    agent_model: 'gemini-base',
    capabilities: {
      secondary_capability_code_19: true,
      capability_code_9: true,
      capability_code_12: true,
      default_capability_code_9: true,
      default_capability_code_12: true,
    },
  }

  it('distinguishes omitted agent tools from an explicit empty list', () => {
    const omitted = protocolCall(
      'interactions',
      agent,
      agent.id,
      'key',
      input.request,
      false,
      input.images,
      undefined,
      undefined,
    )
    expect(omitted.path).toBe('/v1beta/interactions')
    expect(omitted.body).not.toHaveProperty('tools')
    const disabled = protocolCall(
      'interactions',
      agent,
      agent.id,
      'key',
      { ...input.request, tools: [] },
      false,
      input.images,
      undefined,
      'previous-id',
    )
    expect(disabled.body).toMatchObject({
      agent: 'antigravity',
      tools: [],
      previous_interaction_id: 'previous-id',
    })
  })

  it('sends explicit empty tools when the user disables the agent defaults', () => {
    const settings = defaultSettings(agent)
    expect(settings.tools).toEqual(['code_execution', 'google_search'])
    const enabled = buildRequest(settings, agent, [])
    expect(enabled.tools).toEqual([{ codeExecution: {} }, { googleSearch: {} }])
    expect(enabled.toolConfig).toEqual({ includeServerSideToolInvocations: true })
    settings.tools = []
    expect(buildRequest(settings, agent, []).tools).toEqual([])
    expect(buildRequest(defaultSettings(model), model, [])).not.toHaveProperty('tools')
  })

  it('keeps Antigravity model/token configuration separate from Build assistant routing', () => {
    const settings = defaultSettings(agent)
    expect(agentConfig(settings, agent)).toBeUndefined()
    settings.agentModel = 'gemini-chosen'
    settings.maxTotalTokens = 4096
    expect(agentConfig(settings, agent)).toEqual({
      type: 'antigravity',
      model: 'gemini-chosen',
      max_total_tokens: '4096',
    })
    expect(protocolsFor(agent).map((item) => item.value)).toEqual(['interactions'])
    const assistant: Model = { ...model, channels: ['assistant'], agent_model: 'not-an-agent' }
    expect(agentConfig(settings, assistant)).toBeUndefined()
    expect(protocolsFor(assistant).some((item) => item.value === 'interactions')).toBe(false)
    expect(publicAPIBase(assistant.id, assistant)).toBe('/build/v1')
  })

  it('keeps Deep Research capability switches in its own configuration type', () => {
    const research: Model = {
      ...model,
      capabilities: {
        secondary_capability_code_19: true,
        capability_code_59: true,
        capability_code_60: true,
        capability_code_61: true,
      },
    }
    const settings = defaultSettings(research)
    settings.thinkingSummaries = true
    settings.visualization = false
    settings.collaborativePlanning = true
    expect(agentConfig(settings, research)).toEqual({
      type: 'deep-research',
      thinking_summaries: 'auto',
      visualization: 'off',
      collaborative_planning: true,
    })
  })

  it('decodes real server tool steps and terminal interaction usage', () => {
    const call = protocolCall(
      'interactions',
      agent,
      agent.id,
      'key',
      input.request,
      true,
      input.images,
      undefined,
      undefined,
    )
    expect(
      call.decoder.event({
        event_type: 'step.start',
        step: {
          type: 'google_search_call',
          id: 'tool-id',
          arguments: { queries: ['offline'] },
        },
      }).parts,
    ).toEqual([
      {
        toolCall: { toolType: 'GOOGLE_SEARCH_WEB', id: 'tool-id', args: { queries: ['offline'] } },
      },
    ])
    expect(
      call.decoder.event({
        event_type: 'step.start',
        step: {
          type: 'function_call',
          id: 'function-id',
          name: 'fixture',
          arguments: { n: 1 },
        },
      }).parts,
    ).toEqual([{ functionCall: { id: 'function-id', name: 'fixture', args: { n: 1 } } }])
    const completed = {
      event_type: 'interaction.completed',
      interaction: {
        id: 'interaction-id',
        status: 'completed',
        usage: { total_input_tokens: 2, total_output_tokens: 3, total_thought_tokens: 4 },
      },
    }
    expect(call.decoder.event(completed)).toMatchObject({
      interactionId: 'interaction-id',
      tokens: 7,
      thoughtTokens: 4,
      promptTokens: 2,
    })
    expect(call.decoder.done(completed)).toBe(true)
  })

  it.each(['openai-python', 'openai-typescript'] as const)(
    'keeps default MP3 and explicit WAV in %s speech examples',
    (language) => {
      const speech = {
        baseURL: 'https://example.test',
        model: 'build-only',
        prompt: 'offline',
        temperature: 1,
        speakers: [],
        openAI: { input: 'offline', voice: 'fixture' },
      }
      const mp3 = speechCode(language, speech)
      expect(mp3).toContain('speech.mp3')
      expect(mp3).not.toContain('response_format')
      const wav = speechCode(language, {
        ...speech,
        openAI: { ...speech.openAI, response_format: 'wav' },
      })
      expect(wav).toContain('speech.wav')
      expect(wav).toContain('response_format')
      expect(wav).toContain('https://example.test/build/v1')
    },
  )
})
