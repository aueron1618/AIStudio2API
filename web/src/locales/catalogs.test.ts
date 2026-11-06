import { describe, expect, it } from 'vite-plus/test'

const frontend = import.meta.glob<{ messages: Record<string, string> }>(
  './{zh-CN,zh-TW,en,ja,ko,fr,de}.ts',
  { eager: true },
)
const server = import.meta.glob<string>('../../../internal/i18n/messages/*.json', {
  eager: true,
  query: '?raw',
  import: 'default',
})

describe('merged translation catalogs', () => {
  it('includes all seven UI and six server translations', () => {
    expect(Object.keys(frontend)).toHaveLength(7)
    expect(Object.keys(server)).toHaveLength(6)
  })

  it.each(Object.entries(frontend))('%s retains local and new settings keys', (_file, catalog) => {
    expect(Object.keys(catalog.messages).sort()).toEqual(
      Object.keys(frontend['./zh-CN.ts']!.messages).sort(),
    )
    for (const key of [
      'app.resizeSidebar',
      'app.expandSidebar',
      'app.collapseSidebar',
      'accounts.cookieSecurity',
      'accounts.cookieMethodBrowser',
      'accounts.cookieMethodHttp',
      'accounts.cookieTaskHelp',
      'error.account_task_canceled',
      'settings.antiTruncationHelp',
      'settings.autoContinuationHelp',
      'settings.apiKeyAccountsHelp',
      'settings.firstEventHedgeModelsHelp',
      'settings.environmentLocked',
      'settings.stripThoughtSignaturesHelp',
      'settings.unitSeconds',
      'settings.unitMinutes',
      'usage.requestedModel',
      'usage.responseModel',
    ]) {
      expect(catalog.messages[key]?.trim(), key).toBeTruthy()
    }
    for (const path of ['/v1', '/build/v1', '/v1beta']) {
      expect(catalog.messages['settings.fixedChannelHelp']).toContain(path)
    }
  })

  it.each(Object.entries(server))(
    '%s is valid JSON without duplicate or lost local boolean keys',
    (_file, source) => {
      const catalog = JSON.parse(source) as Record<string, string>
      expect(catalog['\u8d26\u6237\u4efb\u52a1\u5df2\u53d6\u6d88']).toBeTruthy()
      expect(catalog['\u8d26\u6237\u4efb\u52a1\u5df2\u53d6\u6d88\u6216\u8d85\u65f6']).toBeTruthy()
      const keys = [...source.matchAll(/^\s*("(?:[^"\\]|\\.)*")\s*:/gm)].map(
        (match) => JSON.parse(match[1]!) as string,
      )
      expect(new Set(keys).size).toBe(keys.length)
      const suffix = ' \u5fc5\u987b\u662f true \u6216 false'
      for (const key of ['ANTI_TRUNCATION', 'AUTO_CONTINUATION', 'CONTINUATION_REWRITE']) {
        expect(catalog[key + suffix]).toContain(key)
      }
      expect(catalog['%s' + suffix]).toContain('{0}')
    },
  )
})
