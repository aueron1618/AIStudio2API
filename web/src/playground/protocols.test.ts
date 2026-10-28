import { afterEach, describe, expect, it, vi } from 'vite-plus/test'
import type { Model } from '@/types'
import { generateCode, type CodeInput } from './code'
import { openLiveSocket } from './media/live-socket'
import { protocolCall } from './protocols'
import { apiKey, publicAPIBase } from './request'

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
const input: CodeInput = {
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
    expect(publicAPIBase('assistant', { ...model, channels: ['assistant'] })).toBe('/build/v1')
    expect(publicAPIBase('alias-build', { ...model, channels: ['playground'] })).toBe('/v1')
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
    expect(openLiveSocket('unknown').url).toBe('wss://example.test/v1/live?key=first-key')
  })
})
