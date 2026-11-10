<script setup lang="ts">
defineProps<{ adminUsername?: string }>()
const emit = defineEmits<{ logout: [] }>()
import {
  computed,
  defineAsyncComponent,
  h,
  onMounted,
  onUnmounted,
  ref,
  useTemplateRef,
  watch,
} from 'vue'
import type { AsyncComponentLoader } from 'vue'
import {
  DialogClose,
  DialogContent,
  DialogOverlay,
  DialogPortal,
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
import UiDisclosure from '@/components/UiDisclosure.vue'
import LogsPanel from '@/components/LogsPanel.vue'
import ModelsTable from '@/components/ModelsTable.vue'
import RequestsPanel from '@/components/RequestsPanel.vue'
import SettingsPanel from '@/components/SettingsPanel.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import UiIcon from '@/components/UiIcon.vue'
import { useClampToggle } from '@/clamp'
import { afterTask } from '@/lazy'
import { readPreference, writePreference } from '@/preference'
import { readTab, useTabParam } from '@/url'
import { useRoute, useRouter } from 'vue-router'
import type { IconName } from '@/icons'

const { availableLocales, locale, setLocale, t, logText } = useI18n()
// lazyPanel 创建按需加载的页面组件
function lazyPanel(loader: AsyncComponentLoader) {
  return defineAsyncComponent({
    loader: () => loader().then(afterTask),
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
const AccessPanel = lazyPanel(() => import('@/components/AccessPanel.vue'))
const validTabs: TabID[] = [
  'logs',
  'accounts',
  'access',
  'models',
  'requests',
  'usage',
  'settings',
  'version',
]
const { tab: currentTab, select: selectTab } = useTabParam(validTabs, 'logs', 'app.tab')
const router = useRouter()
const route = useRoute()
const sidebarCollapsed = ref(
  readPreference('app.sidebarCollapsed', false, (value) => typeof value === 'boolean'),
)
const mobileNavOpen = ref(false)
const mobileBreakpoint = window.matchMedia('(width < 48rem)')
const isMobile = ref(mobileBreakpoint.matches)

function syncMobileLayout(): void {
  isMobile.value = mobileBreakpoint.matches
  mobileNavOpen.value = false
  resizingSidebar.value = false
}

onMounted(() => mobileBreakpoint.addEventListener('change', syncMobileLayout))
onUnmounted(() => mobileBreakpoint.removeEventListener('change', syncMobileLayout))
watch(sidebarCollapsed, (value) => writePreference('app.sidebarCollapsed', value))
// currentTab 在从试用页返回时同步目标管理页面
watch(
  () => route.fullPath,
  () => {
    mobileNavOpen.value = false
    if (route.name === 'console') {
      currentTab.value = readTab(validTabs, 'logs', 'app.tab')
    }
  },
)

// openTab 切换页面，API 试用进入独立的试用页
function openTab(tab: TabID): void {
  mobileNavOpen.value = false
  if (tab === 'playground') void router.push('/prompts/new_chat')
  else selectTab(tab)
}
const navigationBar = ref<HTMLElement | null>(null)

function focusCurrentTab(event: Event): void {
  const item = navigationBar.value?.querySelector<HTMLElement>('[aria-current="page"]')
  if (!item) return
  event.preventDefault()
  item.focus()
}

// defaultSidebarWidth 是宽屏侧栏的默认宽度，双击拖动条恢复
const defaultSidebarWidth = 256
const minSidebarWidth = 176
const maxSidebarWidth = 360
const sidebarWidth = ref(
  readPreference('app.sidebarWidth', defaultSidebarWidth, (value) => typeof value === 'number'),
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
  if (event.button !== 0) return
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
  accountTasks,
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
  closeNotice,
  loadAccountData,
  loadAccountTasks,
  rememberAccountTask,
  loadRequestData,
  updateAccount,
  deleteAccounts,
  undoDelete,
  commitDelete,
  startService,
  stopService,
  clearLogs,
  waitForRestart,
} = useConsole()

// noticeText 是按界面语言显示的提示正文，服务文案翻译表到达后随之更新
const noticeText = computed(() => logText(notice.message))
// deletedNotice 是撤销提示的正文：删除一个账户时写账户名，批量删除时写数量
const deletedNotice = computed(() => {
  const accounts = pendingDelete.value?.accounts ?? []
  return accounts.length === 1
    ? t('accounts.deletedNamed').replace('{name}', accounts[0]!.label)
    : t('accounts.deletedCount').replace('{count}', String(accounts.length))
})
const noticeBody = useTemplateRef<HTMLElement>('noticeBody')
// noticeOpen 是提示是否显示全文
const noticeOpen = ref(false)
// noticeClamped 是提示正文当前是否截断为三行，收起过渡结束后才截断
const noticeClamped = useClampToggle(noticeBody, noticeOpen, 3)
// noticeClipped 是提示正文是否超过三行
const noticeClipped = ref(false)
watch(
  noticeText,
  () => {
    noticeOpen.value = false
    noticeClipped.value =
      noticeBody.value !== null && noticeBody.value.scrollHeight > noticeBody.value.clientHeight
  },
  { flush: 'post' },
)

const navigation: { id: TabID; label: TranslationKey; icon: IconName }[] = [
  { id: 'accounts', label: 'nav.accounts', icon: 'accounts' },
  { id: 'models', label: 'nav.models', icon: 'models' },
  { id: 'requests', label: 'nav.requests', icon: 'requests' },
  { id: 'usage', label: 'nav.usage', icon: 'usage' },
  { id: 'settings', label: 'nav.settings', icon: 'settings' },
  { id: 'playground', label: 'nav.playground', icon: 'playground' },
  { id: 'version', label: 'nav.version', icon: 'version' },
  { id: 'access', label: 'nav.access', icon: 'access' },
  { id: 'logs', label: 'nav.logs', icon: 'logs' },
]

const statusColor = computed(() => {
  if (serviceState.value === 'running') return 'bg-green-500'
  if (serviceState.value === 'launching') return 'bg-cyan-400 animate-pulse'
  return 'bg-gray-600'
})
const statusTextColor = computed(() => {
  if (serviceState.value === 'running') return 'text-green-400'
  if (serviceState.value === 'launching') return 'text-cyan-300'
  return 'text-gray-500'
})
// serviceStateLabel 是生成服务状态在当前语言下的文案
const serviceStateLabel = computed(() => t(`state.${serviceState.value}`))
// serviceStates 是状态区可能显示的服务状态，各状态文案叠放在同一格，宽度取最长的一项
const serviceStates = ['running', 'launching', 'stopped', 'unavailable'] as const

// logout 先在当前会话内提交等待中的删除再退出登录
async function logout(): Promise<void> {
  await commitDelete()
  emit('logout')
}
</script>

<template>
  <div class="flex h-full w-full flex-col md:flex-row">
    <DialogRoot v-model:open="mobileNavOpen">
      <header
        class="sidebar-mobile-header flex min-h-14 min-w-0 shrink-0 items-center gap-2 border-b border-line bg-panel px-2 md:hidden"
      >
        <DialogTrigger as-child>
          <button
            type="button"
            class="flex size-11 shrink-0 items-center justify-center rounded text-gray-400 transition hover:bg-raised hover:text-white"
            :aria-label="t('app.expandSidebar')"
          >
            <UiIcon name="menu" :size="20" />
          </button>
        </DialogTrigger>
        <span class="relative shrink-0" :title="`${t('app.status')}: ${serviceStateLabel}`">
          <img src="/favicon.svg" alt="" class="size-6" />
          <span
            class="absolute -right-0.5 -bottom-0.5 size-2.5 rounded-full ring-2 ring-panel"
            :class="statusColor"
          ></span>
        </span>
        <span class="min-w-0 flex-1 truncate text-base font-bold text-white">AIStudio2API</span>
        <ThemeToggle class="sidebar-touch-control" />
      </header>
      <DialogPortal :disabled="!isMobile">
        <DialogOverlay v-if="isMobile" class="sidebar-overlay fixed inset-0 z-40 bg-black/50" />
        <component
          :is="isMobile ? DialogContent : 'aside'"
          :as="isMobile ? 'aside' : undefined"
          :aria-modal="isMobile ? true : undefined"
          :aria-describedby="undefined"
          class="app-sidebar relative grid min-h-0 min-w-0 shrink-0 border-r border-line bg-panel outline-none"
          :class="{ 'is-collapsed': sidebarCollapsed }"
          :style="{ '--sidebar-width': `${sidebarWidth}px` }"
          @open-auto-focus="focusCurrentTab"
          @close-auto-focus="!isMobile && $event.preventDefault()"
        >
          <div
            class="sidebar-brand flex h-14 min-w-0 items-center gap-2.5 border-b border-line px-4 [grid-area:brand]"
          >
            <button
              type="button"
              class="hidden size-6 shrink-0 items-center justify-center rounded text-gray-400 transition hover:bg-raised hover:text-white md:flex"
              :aria-label="t(sidebarCollapsed ? 'app.expandSidebar' : 'app.collapseSidebar')"
              :aria-expanded="!sidebarCollapsed"
              aria-controls="app-navigation"
              @click="sidebarCollapsed = !sidebarCollapsed"
            >
              <UiIcon :name="sidebarCollapsed ? 'sidebarOpen' : 'sidebarClose'" :size="16" />
            </button>
            <DialogClose v-if="isMobile" as-child>
              <button
                type="button"
                class="-ml-2 flex size-11 shrink-0 items-center justify-center rounded text-gray-400 transition hover:bg-raised hover:text-white"
                :aria-label="t('app.collapseSidebar')"
              >
                <UiIcon name="close" :size="20" />
              </button>
            </DialogClose>
            <span
              v-tooltip="`${t('app.status')}: ${serviceStateLabel}`"
              class="sidebar-label relative shrink-0"
            >
              <img src="/favicon.svg" alt="" class="h-6 w-6" />
              <span
                class="absolute -right-0.5 -bottom-0.5 h-2.5 w-2.5 rounded-full ring-2 ring-panel"
                :class="statusColor"
              ></span>
            </span>
            <DialogTitle as-child>
              <h1
                class="sidebar-label relative top-px min-w-0 flex-1 truncate text-base font-bold text-white"
              >
                AIStudio2API
              </h1>
            </DialogTitle>
            <div class="sidebar-label flex shrink-0 items-center gap-1">
              <ThemeToggle />
              <DropdownMenuRoot :modal="isMobile">
                <DropdownMenuTrigger
                  v-tooltip="t('app.language')"
                  :aria-label="t('app.language')"
                  class="flex h-6 w-6 shrink-0 items-center justify-center rounded border border-gray-700 text-gray-500 transition hover:text-white data-[state=open]:text-white"
                >
                  <UiIcon name="language" :size="13" />
                </DropdownMenuTrigger>
                <DropdownMenuPortal>
                  <DropdownMenuContent
                    align="end"
                    :side-offset="4"
                    class="z-[60] max-h-[var(--reka-dropdown-menu-content-available-height)] overflow-y-auto rounded border border-line bg-panel shadow-xl"
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
          </div>

          <nav
            id="app-navigation"
            ref="navigationBar"
            class="flex min-h-0 w-full min-w-0 flex-col gap-1 overflow-x-hidden overflow-y-auto overscroll-contain p-2 [grid-area:nav]"
          >
            <button
              v-for="item in navigation"
              :key="item.id"
              :class="[
                'flex min-h-11 w-full shrink-0 items-center gap-2 rounded-md px-3 py-2 text-left transition md:min-h-0',
                currentTab === item.id
                  ? 'bg-blue-600 text-white'
                  : 'text-gray-400 hover:bg-raised hover:text-white',
              ]"
              type="button"
              :aria-current="currentTab === item.id ? 'page' : undefined"
              :aria-label="t(item.label)"
              :title="sidebarCollapsed ? t(item.label) : undefined"
              @click="openTab(item.id)"
            >
              <UiIcon :name="item.icon" :size="16" class="shrink-0" />
              <span class="sidebar-label min-w-0 [overflow-wrap:anywhere]">{{
                t(item.label)
              }}</span>
              <span
                v-if="item.id === 'version' && update?.available"
                class="ml-auto size-2 shrink-0 rounded-full bg-amber-400"
                aria-hidden="true"
                :title="t('version.available').replace('{version}', update.latest!)"
              />
            </button>
          </nav>

          <div
            v-if="adminUsername"
            class="sidebar-controls flex min-w-0 items-center gap-1 border-t border-line px-3 py-2 [grid-area:controls]"
          >
            <span
              v-tooltip.overflow="adminUsername"
              class="sidebar-label min-w-0 flex-1 truncate text-xs text-gray-400"
              >{{ adminUsername }}</span
            >
            <button
              v-tooltip="t('auth.logout')"
              type="button"
              :aria-label="`${adminUsername} · ${t('auth.logout')}`"
              class="sidebar-touch-control flex h-6 w-6 shrink-0 items-center justify-center rounded border border-gray-700 text-gray-500 transition hover:text-white"
              @click="logout"
            >
              <UiIcon name="login" :size="13" />
            </button>
          </div>

          <div
            class="sidebar-status relative min-w-0 overflow-hidden border-t border-line p-3 [grid-area:status] md:p-4"
            :class="serviceState === 'launching' ? 'launching-shell' : ''"
          >
            <div
              v-if="serviceState === 'launching'"
              class="launching-scan"
              aria-hidden="true"
            ></div>
            <div class="sidebar-label mb-2 hidden text-xs text-gray-500 md:block">
              {{ t('app.status') }}
            </div>
            <div class="sidebar-label mb-2 flex items-center justify-between gap-2 md:mb-4">
              <span class="grid min-w-0 font-mono font-bold uppercase" :class="statusTextColor">
                <span
                  v-for="state in serviceStates"
                  :key="state"
                  class="min-w-0 truncate [grid-area:1/1]"
                  :class="{ invisible: loading.status || state !== serviceState }"
                  :aria-hidden="loading.status || state !== serviceState"
                  >{{ t(`state.${state}`) }}</span
                >
              </span>
              <span
                v-tooltip="status?.version"
                class="max-w-[65%] truncate font-mono text-xs text-gray-500"
              >
                {{ status?.version || '—' }}
              </span>
            </div>
            <button
              v-if="serviceState === 'stopped' || serviceState === 'unavailable'"
              :class="{ invisible: serviceState === 'unavailable' }"
              class="flex min-h-11 w-full items-center justify-center gap-2 rounded bg-green-600 py-2 font-bold text-white shadow transition hover:bg-green-500 disabled:opacity-50 md:min-h-0"
              type="button"
              :disabled="startPending || stopPending"
              :aria-label="t('app.start')"
              @click="startService"
            >
              <UiIcon :name="startPending ? 'spinner' : 'play'" :size="16" />
              <span class="sidebar-label">{{ t('app.start') }}</span>
            </button>
            <button
              v-else-if="serviceState === 'launching' || serviceState === 'running'"
              class="flex min-h-11 w-full items-center justify-center gap-2 rounded bg-red-600 py-2 font-bold text-white shadow transition hover:bg-red-500 disabled:opacity-50 md:min-h-0"
              type="button"
              :disabled="stopPending"
              :aria-label="t('app.stop')"
              @click="stopService"
            >
              <UiIcon :name="stopPending ? 'spinner' : 'stop'" :size="16" />
              <span class="sidebar-label">{{ t('app.stop') }}</span>
            </button>
          </div>
          <div
            v-show="!sidebarCollapsed"
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
            @pointerup="resizingSidebar = false"
            @pointercancel="resizingSidebar = false"
            @dblclick="sidebarWidth = defaultSidebarWidth"
            @keydown="resizeSidebarWithKeyboard"
          ></div>
        </component>
      </DialogPortal>
    </DialogRoot>

    <main class="@container flex min-h-0 min-w-0 flex-1 flex-col bg-canvas">
      <div
        v-if="config?.reserved_listen_addr"
        role="alert"
        class="page-x overflow-hidden border-b border-amber-500/30 bg-amber-500/10 py-2.5 text-sm break-words text-amber-200 [scrollbar-gutter:stable]"
      >
        {{
          t('app.listenReserved')
            .replace('{reserved}', config.reserved_listen_addr)
            .replace('{active}', config.active_listen_addr)
        }}
      </div>
      <div
        v-if="config?.invalid.length"
        role="alert"
        class="page-x overflow-hidden border-b border-amber-500/30 bg-amber-500/10 py-2.5 text-sm break-words text-amber-200 [scrollbar-gutter:stable]"
      >
        <p>{{ t('app.invalidConfig') }}</p>
        <p v-for="item in config.invalid" :key="item.key" class="mt-1 font-mono text-xs">
          {{ item.value ? `${item.key}=${item.value}` : item.key }} · {{ logText(item.reason) }}
        </p>
      </div>
      <KeepAlive
        include="LogsPanel,AccountsPanel,AccessPanel,ModelsTable,RequestsPanel,SettingsPanel,VersionPanel,UsagePanel"
      >
        <LogsPanel v-if="currentTab === 'logs'" :logs="logs" @clear="clearLogs" />
        <AccountsPanel
          v-else-if="currentTab === 'accounts'"
          :accounts="visibleAccounts"
          :tasks="accountTasks"
          :tasks-error="errors.tasks"
          :loading="loading.accounts"
          :error="errors.accounts"
          :chrome-import="status?.chrome_import ?? false"
          @refresh="loadAccountData"
          @refresh-tasks="loadAccountTasks"
          @task="rememberAccountTask"
          @updated="updateAccount"
          @notice="showNotice"
          @delete="deleteAccounts"
        />
        <AccessPanel
          v-else-if="currentTab === 'access'"
          :config="config"
          :models="models"
          @settings="openTab('settings')"
        />
        <ModelsTable
          v-else-if="currentTab === 'models'"
          :models="models"
          :loading="loading.models"
          :error="errors.models"
          :config="config"
          :accounts="accounts"
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
          :accounts="accounts"
          :models="models"
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
    </main>

    <div
      class="pointer-events-none fixed right-5 bottom-5 left-5 z-50 flex flex-col items-end gap-2 sm:left-auto sm:max-w-md"
    >
      <Transition name="notice">
        <div
          v-if="notice.message"
          role="status"
          class="pointer-events-auto flex max-w-full items-start gap-2 rounded border bg-panel py-3 pr-2 pl-4 text-sm shadow-xl"
          :class="
            notice.tone === 'error'
              ? 'border-red-500/50 text-red-300'
              : 'border-green-500/50 text-green-300'
          "
        >
          <UiDisclosure v-if="noticeClipped" v-model="noticeOpen" />
          <p
            ref="noticeBody"
            class="min-w-0 flex-1 break-words"
            :class="noticeClamped ? 'line-clamp-3' : 'max-h-[40vh] overflow-y-auto'"
          >
            {{ noticeText }}
          </p>
          <button
            type="button"
            class="-my-1 shrink-0 rounded p-1 text-gray-500 transition hover:bg-raised hover:text-gray-200"
            :aria-label="t('common.close')"
            @click="closeNotice"
          >
            <UiIcon name="close" :size="14" />
          </button>
        </div>
      </Transition>
      <Transition name="notice">
        <div
          v-if="pendingDelete"
          class="pointer-events-auto flex max-w-full items-center gap-4 rounded border border-line-strong bg-panel px-4 py-3 text-sm text-gray-200 shadow-xl"
          role="status"
        >
          <span v-tooltip.overflow="deletedNotice" class="min-w-0 truncate">{{
            deletedNotice
          }}</span>
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

@media (width >= 48rem) {
  .app-sidebar.is-collapsed {
    width: 64px;
  }
  .is-collapsed .sidebar-label {
    display: none;
  }
  .is-collapsed nav button {
    justify-content: center;
    padding-inline: 0.5rem;
  }
  .is-collapsed .sidebar-status {
    padding: 0.5rem;
  }
}

@media (width < 48rem) {
  .sidebar-mobile-header {
    padding-top: env(safe-area-inset-top);
    padding-inline: max(0.5rem, env(safe-area-inset-left)) max(0.5rem, env(safe-area-inset-right));
  }

  .app-sidebar {
    position: fixed;
    z-index: 50;
    inset: 0 auto 0 0;
    width: min(20rem, calc(100vw - 2rem));
    height: 100dvh;
    padding-top: env(safe-area-inset-top);
    padding-bottom: env(safe-area-inset-bottom);
    padding-left: env(safe-area-inset-left);
    box-shadow: 8px 0 32px rgb(0 0 0 / 25%);
  }

  .sidebar-touch-control {
    width: 2.75rem;
    height: 2.75rem;
  }

  .app-sidebar[data-state='open'] {
    animation: sidebar-in 180ms ease-out;
  }

  .app-sidebar[data-state='closed'] {
    animation: sidebar-out 140ms ease-in;
  }

  .sidebar-overlay[data-state='open'] {
    animation: sidebar-overlay-in 180ms ease-out;
  }

  .sidebar-overlay[data-state='closed'] {
    animation: sidebar-overlay-out 140ms ease-in;
  }
}

@keyframes sidebar-in {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(0);
  }
}

@keyframes sidebar-out {
  from {
    transform: translateX(0);
  }
  to {
    transform: translateX(-100%);
  }
}

@keyframes sidebar-overlay-in {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}

@keyframes sidebar-overlay-out {
  from {
    opacity: 1;
  }
  to {
    opacity: 0;
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
</style>
