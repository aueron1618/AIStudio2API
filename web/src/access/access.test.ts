import { nextTick, reactive, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { messages } from '@/locales/en'
import { mountSetup } from '@/test-component'
import type { Model } from '@/types'
import AccessPanel from '@/components/AccessPanel.vue'
import GetCodeDialog from '@/playground/GetCodeDialog.vue'
import { capabilities } from './capabilities'
import { clients, type ClientInput } from './clients'
import { endpointExample, endpointGroups, endpointPath } from './endpoints'

const catalog = vi.hoisted(() => ({ value: [] as Model[] }))
const normal: Model = {
  id: 'normal',
  name: 'Normal',
  methods: ['generateContent'],
  channels: ['playground', 'build'],
  capabilities: { chat_model: true },
}
const build: Model = { ...normal, id: 'build-only', name: 'Build', channels: ['build'] }
const alias = '\u6297\u622a\u65ad-build-only(high)-search'
const aliasModel: Model = {
  id: alias,
  name: 'Alias',
  methods: ['generateContent'],
  capabilities: { chat_model: true },
}
catalog.value = [normal, build, aliasModel]
const origin = 'https://console.example.test'
const dialogSettings = {
  model: alias,
  protocol: 'openai-chat',
  stream: false,
  imageCount: 1,
  openAISize: 'auto',
  imageQuality: 'auto',
}

vi.mock('@/desktop', () => ({ keepPage: () => () => {} }))
vi.mock('@/console', () => ({
  useConsole: () => ({
    models: catalog,
    config: { value: { proxy_api_key: 'first-key,second-key' } },
  }),
}))
vi.mock('@/i18n', () => ({
  useI18n: () => ({ t: (key: keyof typeof messages) => messages[key], locale: { value: 'en' } }),
}))
vi.mock('@/url', () => ({
  useQueryParam: (_key: string, _options: string[], initial: string) => ref(initial),
}))
vi.mock('@/playground/session', () => ({
  usePlayground: () => ({
    session: { settings: dialogSettings, turns: [], draft: '', attachments: [] },
    currentModel: { value: build },
  }),
}))
vi.mock('@/playground/request', async (original) => ({
  ...(await original<typeof import('@/playground/request')>()),
  buildRequest: () => ({
    contents: [{ role: 'user', parts: [{ text: 'offline' }] }],
    generationConfig: {},
  }),
}))

const cleanups: (() => void)[] = []
beforeEach(() => {
  vi.stubGlobal('window', { location: new URL(origin), clearTimeout })
  vi.stubGlobal('navigator', { userAgent: 'offline-fixture' })
})
afterEach(() => {
  cleanups.splice(0).forEach((cleanup) => cleanup())
  vi.unstubAllGlobals()
})

function clientInput(model: Model): ClientInput {
  return {
    origin,
    key: 'fixture-key',
    model,
    modelID: model.id,
    tiers: { opus: model.id, sonnet: model.id, haiku: model.id },
    models: catalog.value,
    catalog: catalog.value,
    shell: 'unix',
    cherry: 'openai',
    t: (key) => messages[key],
  }
}

describe('Access fixed-channel contracts', () => {
  it('retains the fourteen upstream clients and all capability families', () => {
    expect(clients).toHaveLength(14)
    expect(new Set(clients.map((client) => client.id)).size).toBe(14)
    expect(capabilities.map((capability) => capability.id)).toEqual([
      'chat',
      'image',
      'speech',
      'transcription',
      'music',
      'video',
      'embedding',
      'live',
      'agent',
      'files',
    ])
  })

  it.each(clients.map((client) => [client.id, client] as const))(
    '%s keeps Build aliases on their permitted protocol entrance',
    (_id, client) => {
      const text = JSON.stringify(client.steps(clientInput(aliasModel)))
      if (client.id === 'claude-code') {
        expect(text).toContain(`${origin}/build`)
        expect(text).not.toContain(`${origin}/build/v1`)
      } else if (client.protocol.includes('OpenAI')) {
        expect(text).toContain(`${origin}/build/v1`)
        expect(text).not.toContain(`${origin}/v1/chat`)
      } else {
        expect(text).toContain(origin)
        expect(text).not.toContain(`${origin}/build`)
      }
      const normalText = JSON.stringify(client.steps(clientInput(normal)))
      expect(normalText).not.toContain(`${origin}/build`)
    },
  )

  it('does not advertise Playground models in a fixed Build OpenCode provider', () => {
    const steps = clients.find((client) => client.id === 'opencode')!.steps(clientInput(aliasModel))
    const step = steps.find((item) => item.kind === 'code' && item.language === 'json')!
    if (step.kind !== 'code') throw new Error('Missing configuration')
    const config = JSON.parse(step.code) as {
      provider: Record<string, { models: Record<string, unknown> }>
    }
    expect(Object.keys(config.provider.aistudio2api!.models)).toEqual(['build-only', alias])
  })

  it.each(['openai', 'anthropic', 'gemini'] as const)(
    'uses the same %s URL in Cherry fields and import links',
    (cherry) => {
      const client = clients.find((item) => item.id === 'cherry-studio')!
      const input = { ...clientInput(aliasModel), cherry }
      const expected =
        origin + (cherry === 'openai' ? '/build/v1' : cherry === 'anthropic' ? '/build' : '')
      expect(JSON.stringify(client.steps(input))).toContain(expected)
      const url = new URL(client.links!(input)[0]!.href)
      expect(JSON.parse(atob(url.searchParams.get('data')!))).toMatchObject({ baseUrl: expected })
    },
  )

  it('routes every REST/WebSocket endpoint example while leaving v1beta auto and media unchanged', () => {
    for (const group of endpointGroups)
      for (const endpoint of group.endpoints) {
        const code = endpointExample(origin, group, endpoint, alias)
        const routed = endpointPath(endpoint.path, alias)
        if (endpoint.path.startsWith('/v1/')) {
          expect(routed).toMatch(/^\/build\/v1\//)
          expect(code).toContain(
            `${endpoint.socket ? origin.replace('https:', 'wss:') : origin}/build/v1/`,
          )
        } else {
          expect(routed).toBe(endpoint.path)
          expect(code).not.toContain(`${origin}/build/v1`)
        }
      }
  })

  it('routes all OpenAI and Anthropic capability snippets without altering the requested alias', () => {
    for (const capability of capabilities)
      for (const protocol of capability.protocols) {
        if (!protocol.value.startsWith('openai') && protocol.value !== 'anthropic') continue
        for (const language of capability.languages(protocol.value)) {
          const code = capability.code({
            origin,
            model: aliasModel,
            modelID: alias,
            protocol: protocol.value,
            language: language.value,
          })
          expect(code, `${capability.id}/${protocol.value}/${language.value}`).toContain(
            `${origin}/build`,
          )
          expect(code).toContain(alias)
        }
      }
  })

  it('uses fixed Build URLs and the first key for Access connectivity probes', async () => {
    const props = reactive({
      models: catalog.value,
      config: { active_proxy_api_key: 'first-key,second-key' },
    })
    const instance = mountSetup<{
      testModel: string
      runTest: () => Promise<void>
      testing: boolean
      results: { ok: boolean }[]
      baseUrls: { url: string }[]
    }>(AccessPanel, props)
    cleanups.push(instance.unmount)
    instance.state.testModel = alias
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(Response.json({ data: [{ id: alias }] }))
      .mockResolvedValueOnce(Response.json({ choices: [{ message: { content: 'OK' } }] }))
    vi.stubGlobal('fetch', fetch)
    await instance.state.runTest()
    expect(fetch.mock.calls.map((call) => call[0])).toEqual([
      '/build/v1/models',
      '/build/v1/chat/completions',
    ])
    const init = fetch.mock.calls[1]![1] as RequestInit
    expect(init.headers).toMatchObject({ Authorization: 'Bearer first-key' })
    expect(JSON.parse(init.body as string)).toMatchObject({ model: alias })
    expect(instance.state.baseUrls.map((item) => item.url)).toEqual([
      `${origin}/build/v1`,
      `${origin}/build`,
      origin,
    ])
    expect(instance.state.testing).toBe(false)
    expect(instance.state.results.map((result) => result.ok)).toEqual([true, true])
  })

  it('keeps Claude tier choices on the same channel as the selected provider', async () => {
    const instance = mountSetup<{
      clientModel: string
      tierModels: Model[]
      clientInput: ClientInput
    }>(AccessPanel, reactive({ models: catalog.value, config: null }))
    cleanups.push(instance.unmount)
    instance.state.clientModel = alias
    await nextTick()
    expect(instance.state.tierModels.map((model) => model.id)).toEqual(['build-only', alias])
    expect(Object.values(instance.state.clientInput.tiers)).not.toContain('normal')
  })

  it('keeps the request model and channel together if selection changes during discovery', async () => {
    const instance = mountSetup<{ testModel: string; runTest: () => Promise<void> }>(
      AccessPanel,
      reactive({ models: catalog.value, config: null }),
    )
    cleanups.push(instance.unmount)
    instance.state.testModel = alias
    let list!: (response: Response) => void
    const fetch = vi
      .fn()
      .mockReturnValueOnce(
        new Promise((resolve) => {
          list = resolve
        }),
      )
      .mockResolvedValueOnce(Response.json({ choices: [{ message: { content: 'OK' } }] }))
    vi.stubGlobal('fetch', fetch)
    const probe = instance.state.runTest()
    instance.state.testModel = 'normal'
    list(Response.json({ data: [{ id: alias }] }))
    await probe
    expect(fetch.mock.calls[1]![0]).toBe('/build/v1/chat/completions')
    expect(JSON.parse((fetch.mock.calls[1]![1] as RequestInit).body as string)).toMatchObject({
      model: alias,
    })
  })

  it('keeps GetCodeDialog raw model IDs while looking up the base catalog channel', () => {
    const instance = mountSetup<{
      code: (language: 'rest' | 'openai-python' | 'openai-typescript') => string
    }>(GetCodeDialog, { open: true })
    cleanups.push(instance.unmount)
    for (const language of ['rest', 'openai-python', 'openai-typescript'] as const) {
      expect(instance.state.code(language)).toContain(`${origin}/build/v1`)
      expect(instance.state.code(language)).toContain(alias)
    }
  })
})
