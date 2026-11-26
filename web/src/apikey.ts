// splitKeys 返回逗号分隔的 API key 列表中去掉首尾空白的各个非空 key
export function splitKeys(value: string): string[] {
  return value
    .split(',')
    .map((key) => key.trim())
    .filter(Boolean)
}

// A short key must not become visible in full, even in a tooltip or accessible name.
export function keySuffix(key: string): string {
  return key.length > 4 ? key.slice(-4) : ''
}

export function maskKey(key: string): string {
  return `••••${keySuffix(key)}`
}
