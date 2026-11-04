import { afterEach, describe, expect, it, vi } from 'vite-plus/test'
import { api } from './api'
import type { ServiceConfig } from './types'

vi.mock('@/desktop', () => ({ keepPage: () => () => {} }))

afterEach(() => vi.unstubAllGlobals())

describe('stage-one management API contracts', () => {
  it.each([
    [false, false],
    [false, true],
    [true, false],
    [true, true],
  ])('serializes anti=%s and auto=%s independently', async (anti, auto) => {
    const config = {
      anti_truncation: anti,
      auto_continuation: auto,
      continuation_rewrite: false,
      api_key_accounts: 'first-key:account-a|account-b',
      first_event_hedge_models: 'gemini-test',
    } as ServiceConfig
    const fetch = vi.fn().mockResolvedValue(Response.json(config))
    vi.stubGlobal('fetch', fetch)
    expect(await api.saveConfig(config)).toEqual(config)
    const [path, init] = fetch.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/config')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(await (init.body as Blob).text())).toEqual(config)
  })

  it('does not synthesize auto_continuation in an older DTO', async () => {
    const legacy = { anti_truncation: false } as ServiceConfig
    const fetch = vi.fn().mockResolvedValue(Response.json(legacy))
    vi.stubGlobal('fetch', fetch)
    await api.saveConfig(legacy)
    const [, init] = fetch.mock.calls[0] as [string, RequestInit]
    expect(JSON.parse(await (init.body as Blob).text())).toEqual({ anti_truncation: false })
  })

  it.each(['browser', 'http'] as const)(
    'keeps %s Cookie imports synchronous, cancellable and region-neutral',
    async (method) => {
      const input = {
        label: '',
        cookies: 'fixture-cookie=offline',
        cdp: '',
        method,
        auth_user: 2,
        proxy: '',
        locale: '',
        timezone: '',
      }
      const result = { account: { id: 'fixture-account' } }
      const fetch = vi.fn().mockResolvedValue(Response.json(result))
      vi.stubGlobal('fetch', fetch)
      const signal = new AbortController().signal
      expect(await api.importCookieAccount(input, signal)).toEqual(result)
      const [path, init] = fetch.mock.calls[0] as [string, RequestInit]
      expect(path).toBe('/api/accounts/import/cookies')
      expect(init.signal).toBe(signal)
      expect(JSON.parse(await (init.body as Blob).text())).toEqual(input)
    },
  )

  it('keeps Chrome imports on the synchronous account_ids DTO', async () => {
    const input = { account_ids: ['fixture-id'], proxy: '', locale: '', timezone: '' }
    const result = { accounts: [{ id: 'fixture-account' }] }
    const fetch = vi.fn().mockResolvedValue(Response.json(result))
    vi.stubGlobal('fetch', fetch)
    expect(await api.importChromeAccounts(input)).toEqual(result)
    const [path, init] = fetch.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/accounts/import/chrome')
    expect(JSON.parse(await (init.body as Blob).text())).toEqual(input)
  })
})
