<script setup lang="ts">
defineProps<{ adminUsername?: string }>()
const emit = defineEmits<{ logout: [] }>()
import { computed, defineAsyncComponent, h, onMounted, onUnmounted, ref, watch } from 'vue'
import type { AsyncComponentLoader } from 'vue'
import {
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogRoot,
  DialogTitle,
  DialogTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { useConsole } from '@/console'
import { useI18n, type TranslationKey } from '@/i18n'
import type { TabID } from '@/types'
import AccountsPanel from '@/components/AccountsPanel.vue'
import LogsPanel from '@/components/LogsPanel.vue'
import ModelsTable from '@/components/ModelsTable.vue'
import RequestsPanel from '@/components/RequestsPanel.vue'
import SettingsPanel from '@/components/SettingsPanel.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import UiIcon from '@/components/UiIcon.vue'
import { readPreference, writePreference } from '@/preference'
import { useTabParam } from '@/url'
import { useRouter } from 'vue-router'
import type { IconName } from '@/icons'
import { useMediaQuery } from '@/playground/media-query'

const { availableLocales, locale, setLocale, t } = useI18n()
// lazyPanel 创建按需加载的页面组件
function lazyPanel(loader: AsyncComponentLoader) {
  return defineAsyncComponent({
    loader,
    delay: 150,
    loadingComponent: {
      render: () =>
        h('p', { class: 'p-8 text-sm text-gray-500', role: 'status' }, t('app.panelLoading')),
    },
    errorComponent: {
      render: () =>
        h('p', { class: 'p-8 text-sm text-red-300', role: 'alert' }, t('app.panelFailed')),
    },
  })
}
const UsagePanel = lazyPanel(() => import('@/components/UsagePanel.vue'))
const VersionPanel = lazyPanel(() => import('@/components/VersionPanel.vue'))
const validTabs: TabID[] = [
  'accounts',
  'models',
  'requests',
  'usage',
  'settings',
  'playground',
  'logs',
  'version',
]
const { tab: currentTab, select: selectTab } = useTabParam(validTabs, 'accounts')
const router = useRouter()
const isMobile = useMediaQuery('(width < 48rem)')
const mobileSidebarOpen = ref(false)
const sidebarCollapsed = ref(
  readPreference('app.sidebarCollapsed', false, (value) => typeof value === 'boolean'),
)
const compactSidebar = computed(() => !isMobile.value && sidebarCollapsed.value)
watch(sidebarCollapsed, (value) => writePreference('app.sidebarCollapsed', value))
watch(isMobile, () => {
  mobileSidebarOpen.value = false
  resizingSidebar.value = false
})

function toggleSidebar(): void {
  if (isMobile.value) mobileSidebarOpen.value = false
  else sidebarCollapsed.value = !sidebarCollapsed.value
}

// openTab 切换页面，API 试用进入独立的试用页
function openTab(tab: TabID): void {
  mobileSidebarOpen.value = false
  if (tab === 'playground') void router.push('/prompts/new_chat')
  else selectTab(tab)
}
watch(
  currentTab,
  (tab) => {
    if (tab === 'playground') void router.replace('/prompts/new_chat')
  },
  { immediate: true },
)
const navigationBar = ref<HTMLElement | null>(null)

// revealCurrentTab 只滚动侧栏，避免切换导航时移动页面正文。
function revealCurrentTab(): void {
  const bar = navigationBar.value
  const item = bar?.querySelector<HTMLElement>('[aria-current="page"]')
  if (!bar || !item) return
  const barRect = bar.getBoundingClientRect()
  const itemRect = item.getBoundingClientRect()
  if (itemRect.top < barRect.top) bar.scrollTop -= barRect.top - itemRect.top
  else if (itemRect.bottom > barRect.bottom) bar.scrollTop += itemRect.bottom - barRect.bottom
}
watch([currentTab, mobileSidebarOpen, compactSidebar], revealCurrentTab, { flush: 'post' })
watch(currentTab, () => (mobileSidebarOpen.value = false))

// defaultSidebarWidth 是宽屏侧栏的默认宽度，双击拖动条恢复
const defaultSidebarWidth = 256
const minSidebarWidth = 176
const maxSidebarWidth = 360
const sidebarWidth = ref(
  readPreference(
    'app.sidebarWidth',
    defaultSidebarWidth,
    (value) => typeof value === 'number' && Number.isFinite(value),
  ),
)
const resizingSidebar = ref(false)
let sidebarResizeStartX = 0
let sidebarResizeStartWidth = 0
watch(sidebarWidth, (value) => writePreference('app.sidebarWidth', value))

function clampSidebarWidth(value: number): number {
  return Math.round(Math.min(maxSidebarWidth, Math.max(minSidebarWidth, value)))
}
sidebarWidth.value = clampSidebarWidth(sidebarWidth.value)

function startSidebarResize(event: PointerEvent): void {
  if (event.button !== 0 || compactSidebar.value || isMobile.value) return
  const handle = event.currentTarget as HTMLElement
  handle.setPointerCapture(event.pointerId)
  sidebarResizeStartX = event.clientX
  sidebarResizeStartWidth = sidebarWidth.value
  resizingSidebar.value = true
  event.preventDefault()
}

function moveSidebarResize(event: PointerEvent): void {
  if (!resizingSidebar.value) return
  sidebarWidth.value = clampSidebarWidth(
    sidebarResizeStartWidth + event.clientX - sidebarResizeStartX,
  )
}

function stopSidebarResize(event: PointerEvent): void {
  if (!resizingSidebar.value) return
  const handle = event.currentTarget as HTMLElement
  if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId)
  resizingSidebar.value = false
}

function resizeSidebarWithKeyboard(event: KeyboardEvent): void {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
  event.preventDefault()
  sidebarWidth.value = clampSidebarWidth(
    sidebarWidth.value + (event.key === 'ArrowRight' ? 16 : -16),
  )
}
const {
  status,
  logs,
  accounts,
  visibleAccounts,
  models,
  cooldowns,
  requests,
  config,
  update,
  loading,
  errors,
  notice,
  pendingDelete,
  serviceState,
  startPending,
  stopPending,
  showNotice,
  loadAccountData,
  loadRequestData,
  deleteAccount,
  undoDelete,
  commitDelete,
  startService,
  stopService,
  clearLogs,
  waitForRestart,
  startConsole,
  stopConsole,
} = useConsole()

const navigation: { id: TabID; label: TranslationKey; icon: IconName }[] = [
  { id: 'accounts', label: 'nav.accounts', icon: 'accounts' },
  { id: 'requests', label: 'nav.requests', icon: 'requests' },
  { id: 'usage', label: 'nav.usage', icon: 'usage' },
  { id: 'settings', label: 'nav.settings', icon: 'settings' },
  { id: 'models', label: 'nav.models', icon: 'models' },
  { id: 'playground', label: 'nav.playground', icon: 'playground' },
  { id: 'logs', label: 'nav.logs', icon: 'logs' },
  { id: 'version', label: 'nav.version', icon: 'version' },
]

const statusColor = computed(() => {
  if (serviceState.value === 'running') return 'bg-green-500 shadow-[0_0_10px_rgba(34,197,94,0.5)]'
  if (serviceState.value === 'launching') return 'bg-cyan-400 animate-pulse'
  return 'bg-gray-600'
})
const statusTextColor = computed(() => {
  if (serviceState.value === 'running') return 'text-green-400'
  if (serviceState.value === 'launching') return 'text-cyan-300'
  return 'text-gray-500'
})

// logout 先在当前会话内提交等待中的删除再退出登录
async function logout(): Promise<void> {
  mobileSidebarOpen.value = false
  await commitDelete()
  emit('logout')
}

onMounted(() => {
  document.title = 'AIStudio2API'
  revealCurrentTab()
  void startConsole()
})

onUnmounted(stopConsole)
</script>

<template>
  <DialogRoot v-model:open="mobileSidebarOpen">
    <div class="flex h-full w-full flex-col md:flex-row">
      <header
        v-if="isMobile"
        class="flex h-12 shrink-0 items-center gap-3 border-b border-line bg-panel px-3"
      >
        <DialogTrigger
          class="flex size-9 shrink-0 items-center justify-center rounded text-gray-400 hover:bg-raised hover:text-white"
          :aria-label="t('app.expandSidebar')"
        >
          <UiIcon name="menu" :size="20" />
        </DialogTrigger>
        <span class="min-w-0 truncate font-medium text-white">
          {{ t(navigation.find((item) => item.id === currentTab)?.label ?? 'app.navigation') }}
        </span>
        <span
          class="ml-auto size-2 shrink-0 rounded-full"
          :class="statusColor"
          :title="`${t('app.status')}: ${serviceState.toUpperCase()}`"
        />
      </header>
      <DialogOverlay v-if="isMobile" class="sidebar-overlay fixed inset-0 z-40 bg-black/60" />
      <component
        :is="isMobile ? DialogContent : 'aside'"
        class="app-sidebar relative grid min-h-0 min-w-0 shrink-0 border-r border-line bg-panel outline-none"
        :class="{ 'is-compact': compactSidebar }"
        :style="{ '--sidebar-width': `${compactSidebar ? 64 : sidebarWidth}px` }"
      >
        <DialogTitle v-if="isMobile" class="sr-only">AIStudio2API</DialogTitle>
        <DialogDescription v-if="isMobile" class="sr-only">{{
          t('app.navigation')
        }}</DialogDescription>
        <div
          class="flex min-h-14 min-w-0 items-center gap-2 border-b border-line px-3 py-2 [grid-area:brand]"
          :class="{ 'flex-col': compactSidebar }"
        >
          <span
            v-tooltip="`${t('app.status')}: ${serviceState.toUpperCase()}`"
            class="relative shrink-0"
          >
            <img src="/favicon.svg" alt="" class="h-6 w-6" />
            <span
              class="absolute -right-0.5 -bottom-0.5 h-2.5 w-2.5 rounded-full ring-2 ring-panel"
              :class="statusColor"
            ></span>
          </span>
          <h1
            v-if="!compactSidebar"
            class="relative top-px min-w-0 flex-1 truncate text-base font-bold text-white"
          >
            AIStudio2API
          </h1>
          <button
            v-tooltip="
              isMobile
                ? t('common.close')
                : compactSidebar
                  ? t('app.expandSidebar')
                  : t('app.collapseSidebar')
            "
            type="button"
            class="flex size-9 shrink-0 items-center justify-center rounded text-gray-400 transition hover:bg-raised hover:text-white"
            :aria-label="
              isMobile
                ? t('common.close')
                : compactSidebar
                  ? t('app.expandSidebar')
                  : t('app.collapseSidebar')
            "
            aria-controls="console-navigation"
            :aria-expanded="!compactSidebar"
            @click="toggleSidebar"
          >
            <UiIcon
              :name="isMobile ? 'close' : compactSidebar ? 'sidebarOpen' : 'sidebarClose'"
              :size="18"
            />
          </button>
        </div>

        <div
          class="flex min-w-0 flex-wrap items-center gap-2 border-t border-line p-3 [grid-area:controls]"
          :class="{ 'flex-col': compactSidebar }"
        >
          <span
            v-if="adminUsername && !compactSidebar"
            v-tooltip.overflow="adminUsername"
            class="w-full min-w-0 truncate text-xs text-gray-400"
            >{{ adminUsername }}</span
          >
          <div class="flex items-center gap-2" :class="{ 'flex-col': compactSidebar }">
            <ThemeToggle class="min-h-8 min-w-8" />
            <DropdownMenuRoot :modal="false">
              <DropdownMenuTrigger
                v-tooltip="t('app.language')"
                :aria-label="t('app.language')"
                class="flex size-8 shrink-0 items-center justify-center rounded border border-gray-700 text-gray-500 transition hover:text-white data-[state=open]:text-white"
              >
                <UiIcon name="language" :size="13" />
              </DropdownMenuTrigger>
              <DropdownMenuPortal>
                <DropdownMenuContent
                  align="end"
                  :side-offset="4"
                  class="z-[60] overflow-hidden rounded border border-line bg-panel shadow-xl"
                >
                  <DropdownMenuItem
                    v-for="item in availableLocales"
                    :key="item.code"
                    class="flex w-full cursor-pointer items-center justify-between gap-4 px-4 py-2 text-left text-xs whitespace-nowrap text-gray-300 outline-none data-[highlighted]:bg-blue-600 data-[highlighted]:text-white"
                    @select="setLocale(item.code)"
                  >
                    {{ item.label }}
                    <UiIcon v-if="item.code === locale" name="check" :size="12" />
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenuPortal>
            </DropdownMenuRoot>
          </div>
          <button
            v-if="adminUsername"
            v-tooltip="t('auth.logout')"
            type="button"
            :aria-label="`${adminUsername} · ${t('auth.logout')}`"
            class="flex size-8 shrink-0 items-center justify-center rounded border border-gray-700 text-gray-500 transition hover:text-white"
            :class="{ 'ml-auto': !compactSidebar }"
            @click="logout"
          >
            <UiIcon name="login" :size="13" />
          </button>
        </div>

        <nav
          id="console-navigation"
          ref="navigationBar"
          :aria-label="t('app.navigation')"
          class="flex min-h-0 w-full min-w-0 flex-col gap-1 overflow-x-hidden overflow-y-auto p-2 [grid-area:nav]"
        >
          <button
            v-for="item in navigation"
            :key="item.id"
            v-tooltip="compactSidebar ? t(item.label) : ''"
            :class="[
              'relative flex min-h-10 w-full shrink-0 items-center gap-2 rounded-md px-3 py-2 text-left transition',
              compactSidebar ? 'justify-center' : '',
              currentTab === item.id
                ? 'bg-blue-600 text-white'
                : 'text-gray-400 hover:bg-raised hover:text-white',
            ]"
            type="button"
            :aria-current="currentTab === item.id ? 'page' : undefined"
            :aria-label="t(item.label)"
            @click="openTab(item.id)"
          >
            <UiIcon :name="item.icon" :size="16" class="shrink-0" />
            <span
              v-if="!compactSidebar"
              v-tooltip.overflow="t(item.label)"
              class="min-w-0 truncate"
              >{{ t(item.label) }}</span
            >
            <span
              v-if="item.id === 'version' && update?.available"
              class="size-2 shrink-0 rounded-full bg-amber-400"
              :class="compactSidebar ? 'absolute top-1.5 right-1.5' : 'ml-auto'"
              aria-hidden="true"
              :title="t('version.available').replace('{version}', update.latest ?? '')"
            />
          </button>
        </nav>

        <div
          class="relative min-w-0 overflow-hidden border-t border-line [grid-area:status]"
          :class="[
            compactSidebar ? 'p-2' : 'p-4',
            serviceState === 'launching' ? 'launching-shell' : '',
          ]"
        >
          <div v-if="serviceState === 'launching'" class="launching-scan" aria-hidden="true"></div>
          <div v-if="!compactSidebar" class="mb-2 text-xs text-gray-500">{{ t('app.status') }}</div>
          <div v-if="!compactSidebar" class="mb-4 flex items-center justify-between gap-2">
            <span class="font-mono font-bold" :class="statusTextColor">
              {{ serviceState.toUpperCase() }}
            </span>
            <span
              v-tooltip="status?.version || ''"
              class="max-w-[65%] truncate font-mono text-xs text-gray-500"
            >
              {{ status?.version || '—' }}
            </span>
          </div>
          <button
            v-if="serviceState === 'stopped'"
            v-tooltip="compactSidebar ? t('app.start') : ''"
            class="flex min-h-10 w-full items-center justify-center gap-2 rounded bg-green-600 py-2 font-bold text-white shadow transition hover:bg-green-500 disabled:opacity-50"
            type="button"
            :aria-label="t('app.start')"
            :disabled="startPending || stopPending"
            @click="startService"
          >
            <UiIcon :name="startPending ? 'spinner' : 'play'" :size="16" />
            <span v-if="!compactSidebar">{{ t('app.start') }}</span>
          </button>
          <button
            v-else-if="serviceState === 'launching' || serviceState === 'running'"
            v-tooltip="compactSidebar ? t('app.stop') : ''"
            class="flex min-h-10 w-full items-center justify-center gap-2 rounded bg-red-600 py-2 font-bold text-white shadow transition hover:bg-red-500 disabled:opacity-50"
            type="button"
            :aria-label="t('app.stop')"
            :disabled="stopPending"
            @click="stopService"
          >
            <UiIcon :name="stopPending ? 'spinner' : 'stop'" :size="16" />
            <span v-if="!compactSidebar">{{ t('app.stop') }}</span>
          </button>
        </div>
        <div
          v-if="!isMobile && !compactSidebar"
          class="sidebar-resizer"
          :class="{ 'is-resizing': resizingSidebar }"
          role="separator"
          tabindex="0"
          aria-orientation="vertical"
          :aria-label="t('app.resizeSidebar')"
          :aria-valuemin="minSidebarWidth"
          :aria-valuemax="maxSidebarWidth"
          :aria-valuenow="sidebarWidth"
          @pointerdown="startSidebarResize"
          @pointermove="moveSidebarResize"
          @pointerup="stopSidebarResize"
          @pointercancel="stopSidebarResize"
          @dblclick="sidebarWidth = defaultSidebarWidth"
          @keydown="resizeSidebarWithKeyboard"
        ></div>
      </component>

      <main class="flex min-h-0 min-w-0 flex-1 flex-col bg-canvas">
        <Transition name="page">
          <KeepAlive include="SettingsPanel,VersionPanel">
            <LogsPanel v-if="currentTab === 'logs'" :logs="logs" @clear="clearLogs" />
            <AccountsPanel
              v-else-if="currentTab === 'accounts'"
              :accounts="visibleAccounts"
              :loading="loading.accounts"
              :error="errors.accounts"
              @refresh="loadAccountData"
              @notice="showNotice"
              @delete="deleteAccount"
            />
            <ModelsTable
              v-else-if="currentTab === 'models'"
              :models="models"
              :loading="loading.models"
              :error="errors.models"
              :config="config"
              @saved="config = $event"
              @notice="showNotice"
            />
            <RequestsPanel
              v-else-if="currentTab === 'requests'"
              :accounts="accounts"
              :cooldowns="cooldowns"
              :requests="requests"
              :loading="loading.requests || loading.cooldowns"
              :cooldown-error="errors.cooldowns"
              :request-error="errors.requests"
              @refresh="loadRequestData"
              @notice="showNotice"
            />
            <UsagePanel v-else-if="currentTab === 'usage'" :models="models" />
            <SettingsPanel
              v-else-if="currentTab === 'settings'"
              :config="config"
              :loading="loading.config"
              :error="errors.config"
              @saved="config = $event"
              @notice="showNotice"
            />
            <VersionPanel
              v-else-if="currentTab === 'version'"
              :status="update"
              :active-requests="status?.active_requests ?? 0"
              @status="update = $event"
              @restarting="waitForRestart"
            />
          </KeepAlive>
        </Transition>
      </main>

      <div
        class="pointer-events-none fixed right-5 bottom-5 left-5 z-50 flex flex-col items-end gap-2 sm:left-auto sm:max-w-md"
      >
        <Transition name="notice">
          <div
            v-if="notice.message"
            role="status"
            class="pointer-events-auto max-w-full rounded border bg-panel px-4 py-3 text-sm break-words shadow-xl"
            :class="
              notice.tone === 'error'
                ? 'border-red-500/50 text-red-300'
                : 'border-green-500/50 text-green-300'
            "
          >
            {{ notice.message }}
          </div>
        </Transition>
        <Transition name="notice">
          <div
            v-if="pendingDelete"
            class="pointer-events-auto flex max-w-full items-center gap-4 rounded border border-line-strong bg-panel px-4 py-3 text-sm text-gray-200 shadow-xl"
            role="status"
          >
            <span
              v-tooltip.overflow="
                t('accounts.deletedNamed').replace('{name}', pendingDelete.account.label)
              "
              class="min-w-0 truncate"
              >{{ t('accounts.deletedNamed').replace('{name}', pendingDelete.account.label) }}</span
            >
            <button
              type="button"
              class="flex shrink-0 items-center gap-1.5 rounded px-2 py-1 font-medium text-blue-400 hover:bg-raised"
              @click="undoDelete"
            >
              <UiIcon name="undo" :size="14" />
              {{ t('common.undo') }}
            </button>
          </div>
        </Transition>
      </div>
    </div>
  </DialogRoot>
</template>

<style scoped>
.app-sidebar {
  width: var(--sidebar-width);
  grid-template-rows: auto minmax(0, 1fr) auto auto;
  grid-template-columns: minmax(0, 1fr);
  grid-template-areas:
    'brand'
    'nav'
    'controls'
    'status';
}

@media (width < 48rem) {
  .app-sidebar {
    position: fixed;
    z-index: 50;
    inset: 0 auto 0 0;
    width: min(20rem, calc(100vw - 3rem));
    height: 100dvh;
    padding-top: env(safe-area-inset-top);
    padding-bottom: env(safe-area-inset-bottom);
    box-shadow: 12px 0 32px rgb(0 0 0 / 25%);
  }

  .app-sidebar[data-state='open'] {
    animation: sidebar-enter 160ms ease-out;
  }

  .app-sidebar[data-state='closed'] {
    animation: sidebar-leave 120ms ease-in;
  }
}

@keyframes sidebar-enter {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(0);
  }
}

@keyframes sidebar-leave {
  from {
    transform: translateX(0);
  }
  to {
    transform: translateX(-100%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .app-sidebar[data-state] {
    animation: none;
  }
}

.sidebar-resizer {
  display: none;
  position: absolute;
  z-index: 20;
  top: 0;
  right: -0.375rem;
  bottom: 0;
  width: 0.75rem;
  cursor: col-resize;
  touch-action: none;
  user-select: none;
}

.sidebar-resizer::after {
  position: absolute;
  top: 0;
  bottom: 0;
  left: calc(50% - 1px);
  width: 2px;
  background: transparent;
  content: '';
  transition: background-color 150ms ease;
}

.sidebar-resizer:hover::after,
.sidebar-resizer:focus-visible::after,
.sidebar-resizer.is-resizing::after {
  background: var(--color-blue-500);
}

.sidebar-resizer:focus-visible {
  outline: none;
}

@media (width >= 48rem) {
  .sidebar-resizer {
    display: block;
  }
}

.launching-shell {
  background:
    radial-gradient(circle at 12% 0%, rgb(34 211 238 / 14%), transparent 55%), var(--color-panel);
}

.launching-scan {
  position: absolute;
  top: 0;
  left: -45%;
  width: 45%;
  height: 1px;
  background: linear-gradient(90deg, transparent, rgb(103 232 249), transparent);
  animation: launching-scan 1.25s ease-in-out infinite;
}

@keyframes launching-scan {
  to {
    transform: translateX(320%);
  }
}
</style>
