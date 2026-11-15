<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import type { SavedPrompt } from '@/types'
import PlaygroundPage from './PlaygroundPage.vue'
import UiGradientSpinner from './ui/UiGradientSpinner.vue'
import { usePlayground } from './session'
import type { PromptData } from './types'
import { useVideo, type VideoData } from './media/video'

const VideoPage = defineAsyncComponent(() => import('./media/VideoPage.vue'))

const route = useRoute()
const { t } = useI18n()
const playground = usePlayground()
const video = useVideo()
// ready 表示已读取过地址中的会话，读取完成前显示读取中
const ready = ref(false)
const prompt = ref<SavedPrompt<PromptData | VideoData>>()
const kind = ref<'chat' | 'video'>('chat')
const savedVideo = computed(() => {
  const saved = prompt.value
  if (
    !saved ||
    saved.id !== route.params.id ||
    !('kind' in saved.data) ||
    saved.data.kind !== 'video'
  )
    return undefined
  return { title: saved.title, description: saved.description, data: saved.data as VideoData }
})

// load 读取已保存的会话交给对话页，读取失败时交给对话页再读一次并显示原因
async function load(id: string): Promise<void> {
  try {
    const saved = await api.prompt<PromptData | VideoData>(id)
    if (route.params.id !== id) return
    prompt.value = saved
    kind.value = 'kind' in saved.data && saved.data.kind === 'video' ? 'video' : 'chat'
  } catch {
    if (route.params.id !== id) return
    prompt.value = undefined
    kind.value = 'chat'
  }
  ready.value = true
}

// retry 重新打开读取失败的会话
function retry(): void {
  ready.value = false
  void load(String(route.params.id))
}

watch(
  () => route.params.id,
  (id) => {
    if (typeof id !== 'string' || id === '') return
    if (playground.session.id === id) {
      kind.value = 'chat'
      ready.value = true
      return
    }
    if (video.state.id === id) {
      kind.value = 'video'
      ready.value = true
      return
    }
    ready.value = false
    void load(id)
  },
  { immediate: true },
)
</script>

<template>
  <PlaygroundPage
    v-if="ready && kind === 'chat'"
    :prompt="prompt?.id === route.params.id ? (prompt as SavedPrompt<PromptData>) : undefined"
    @retry="retry"
  />
  <VideoPage
    v-else-if="ready && kind === 'video'"
    :prompt-id="String(route.params.id)"
    :prompt="savedVideo"
  />
  <div v-else class="prompt-loading" role="status" :aria-label="t('common.loading')">
    <UiGradientSpinner />
  </div>
</template>

<style scoped>
.prompt-loading {
  display: flex;
  flex: 1;
  align-items: center;
  justify-content: center;
  animation: prompt-loading-in 0.2s ease-out 0.3s both;
}

@keyframes prompt-loading-in {
  from {
    opacity: 0;
  }
}
</style>
