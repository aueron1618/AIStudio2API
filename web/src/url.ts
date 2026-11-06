import { onActivated, onUnmounted, ref, watch, type Ref } from 'vue'
import { readPreference, writePreference } from './preference'

// queryValue 读取地址栏查询参数并限制在允许值内
function queryValue<T extends string>(name: string, allowed: readonly T[], fallback: T): T {
  const value = new URLSearchParams(window.location.search).get(name) as T | null
  return value !== null && allowed.includes(value) ? value : fallback
}

// writeQuery 替换地址栏查询参数，不生成新的历史记录
function writeQuery(values: Record<string, string | null>): void {
  const url = new URL(window.location.href)
  for (const [name, value] of Object.entries(values)) {
    if (value === null) url.searchParams.delete(name)
    else url.searchParams.set(name, value)
  }
  if (url.href !== window.location.href) window.history.replaceState(null, '', url)
}

// bindQuery 将页面内状态同步到地址栏查询参数，等于 fallback 时不写入，浏览器前进后退时更新，缓存页面重新显示时写回
function bindQuery<T extends string>(name: string, read: () => T, fallback: T): Ref<T> {
  const state = ref(read()) as Ref<T>
  const write = (value: T): void => writeQuery({ [name]: value === fallback ? null : value })
  watch(state, write)
  onActivated(() => write(state.value))
  const sync = (): void => {
    state.value = read()
  }
  window.addEventListener('popstate', sync)
  onUnmounted(() => window.removeEventListener('popstate', sync))
  return state
}

// useQueryParam 以限定取值的查询参数保存页面内状态
export function useQueryParam<T extends string>(
  name: string,
  allowed: readonly T[],
  fallback: T,
): Ref<T> {
  return bindQuery(name, () => queryValue(name, allowed, fallback), fallback)
}

// useQueryText 以任意文本的查询参数保存页面内状态
export function useQueryText(name: string, fallback = ''): Ref<string> {
  return bindQuery(
    name,
    () => new URLSearchParams(window.location.search).get(name) ?? fallback,
    fallback,
  )
}

// readTab 优先读取显式 tab 链接，没有指定时恢复最后有效的管理页签。
export function readTab<T extends string>(
  allowed: readonly T[],
  fallback: T,
  preferenceKey?: string,
): T {
  const saved = preferenceKey
    ? readPreference(preferenceKey, fallback, (value) => allowed.includes(value as T))
    : fallback
  const value = new URLSearchParams(window.location.search).get('tab')
  if (value === null) return saved
  return allowed.includes(value as T) ? (value as T) : fallback
}

// useTabParam 同步页签、可选的浏览器记忆与地址栏，切换时清除上一页面的查询参数。
export function useTabParam<T extends string>(
  allowed: readonly T[],
  fallback: T,
  preferenceKey?: string,
): { tab: Ref<T>; select: (tab: T) => void } {
  const tab = ref(readTab(allowed, fallback, preferenceKey)) as Ref<T>
  if (preferenceKey) {
    watch(tab, (value) => writePreference(preferenceKey, value), { flush: 'sync', immediate: true })
  }
  const sync = (): void => {
    tab.value = readTab(allowed, fallback, preferenceKey)
  }
  window.addEventListener('popstate', sync)
  onUnmounted(() => window.removeEventListener('popstate', sync))
  const select = (next: T): void => {
    if (next === tab.value) return
    tab.value = next
    const url = new URL(window.location.href)
    url.search = ''
    if (next !== fallback) url.searchParams.set('tab', next)
    window.history.pushState(null, '', url)
  }
  return { tab, select }
}
