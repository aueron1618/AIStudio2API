import { afterEach, describe, expect, it, vi } from 'vite-plus/test'
import { api } from './api'
import type { AccountTask, ServiceConfig } from './types'

vi.mock('@/desktop', () => ({ keepPage: () => () => {} }))

afterEach(() => vi.unstubAllGlobals())

const task: AccountTask = {
  id: 'fixture/task',
  kind: 'cookies',
  state: 'running',
  items: [{ label: '', state: 'pending' }],
  started_at: '2026-11-04T00:00:00Z',
}

describe('management API contracts', () => {
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
      strip_thought_signatures: true,
      api_key_accounts: 'first-key:account-a|account-b',
      model_accounts: 'models/gemini-test:account-a|account-b',
      account_order: 'account-b,account-a',
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
    'queues %s Cookie items without treating 202 as imported accounts',
    async (method) => {
      const input = {
        items: [{ email: '', cookies: 'fixture-cookie=offline', cdp: '', method, auth_user: 2 }],
        proxy: '',
        locale: '',
        timezone: '',
      }
      const fetch = vi.fn().mockResolvedValue(Response.json({ task }, { status: 202 }))
      vi.stubGlobal('fetch', fetch)
      const signal = new AbortController().signal
      expect(await api.importCookieAccounts(input, signal)).toEqual(task)
      const [path, init] = fetch.mock.calls[0] as [string, RequestInit]
      expect(path).toBe('/api/accounts/import/cookies')
      expect(init.signal).toBe(signal)
      expect(JSON.parse(await (init.body as Blob).text())).toEqual(input)
    },
  )

  it('queues Chrome accounts with source IDs and discovery emails', async () => {
    const input = {
      accounts: [{ id: 'fixture-id', email: 'fixture@example.test' }],
      proxy: '',
      locale: '',
      timezone: '',
    }
    const fetch = vi
      .fn()
      .mockResolvedValue(Response.json({ task: { ...task, kind: 'chrome' } }, { status: 202 }))
    vi.stubGlobal('fetch', fetch)
    expect(await api.importChromeAccounts(input)).toEqual({ ...task, kind: 'chrome' })
    const [path, init] = fetch.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/accounts/import/chrome')
    expect(JSON.parse(await (init.body as Blob).text())).toEqual(input)
  })

  it('recovers accepted tasks with GET and cancels only with DELETE', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(Response.json({ tasks: [task] }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetch)
    expect(await api.accountTasks()).toEqual([task])
    await api.dismissAccountTask(task.id)
    expect(fetch.mock.calls).toEqual([
      ['/api/accounts/tasks', undefined],
      ['/api/accounts/tasks/fixture%2Ftask', { method: 'DELETE' }],
    ])
    expect('importCookieAccount' in api).toBe(false)
  })

  it('keeps discovery cancellable without canceling an accepted background task', async () => {
    const fetch = vi.fn().mockImplementation(
      (_path: string, init: RequestInit) =>
        new Promise((_resolve, reject) => {
          init.signal!.addEventListener('abort', () => reject(init.signal!.reason))
        }),
    )
    vi.stubGlobal('fetch', fetch)
    const controller = new AbortController()
    const request = api.discoverCookieAccounts(
      { cookies: 'fixture=offline', proxy: '' },
      controller.signal,
    )
    const rejected = expect(request).rejects.toThrow()
    controller.abort()
    await rejected
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch.mock.calls[0]![0]).toBe('/api/accounts/import/cookies/discover')
  })

  it('encodes server-side Windows paths without reading local files', async () => {
    const listing = { path: 'D:\\fixture accounts', parent: 'D:\\', entries: [] }
    const fetch = vi.fn().mockResolvedValue(Response.json(listing))
    vi.stubGlobal('fetch', fetch)
    expect(await api.paths(listing.path)).toEqual(listing)
    expect(fetch).toHaveBeenCalledWith('/api/paths?path=D%3A%5Cfixture%20accounts', undefined)
  })
})
