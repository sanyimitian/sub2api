<template>
  <div class="min-w-0">
    <div
      ref="track"
      class="gallery-track flex gap-5 overflow-x-auto pb-3"
      tabindex="0"
      :aria-label="t('modelIdentification.preview')"
      @scroll.passive="syncScroll"
    >
      <button
        v-for="(item, index) in tiles"
        :key="item.result.id"
        type="button"
        class="artwork-card shrink-0 overflow-hidden rounded-2xl border border-gray-200 bg-white text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:border-dark-600 dark:bg-dark-800"
        :aria-label="
          t('modelIdentification.details') + ' · ' + formatDateTime(item.result.started_at)
        "
        @click="selected = item.result"
      >
        <span
          class="artwork-window relative block overflow-hidden bg-gray-100 dark:bg-dark-900"
          aria-hidden="true"
        >
          <iframe
            v-if="item.preview && shouldRender(index)"
            :srcdoc="item.preview"
            sandbox="allow-scripts"
            referrerpolicy="no-referrer"
            tabindex="-1"
            :title="t('modelIdentification.preview') + ' ' + item.result.id"
            class="artwork-frame pointer-events-none absolute left-0 top-0 border-0 bg-white"
          />
          <span
            v-else-if="!item.preview"
            class="flex h-full flex-col justify-center gap-3 p-5 text-sm leading-relaxed"
            :class="
              item.result.status === 'failed'
                ? 'text-amber-800 dark:text-amber-300'
                : 'text-gray-600 dark:text-gray-300'
            "
          >
            <strong>{{
              t(
                item.result.status === 'failed'
                  ? 'modelIdentification.failed'
                  : 'modelIdentification.noPreview'
              )
            }}</strong>
            <span v-if="item.result.error_message" class="line-clamp-4 break-words">{{
              item.result.error_message
            }}</span>
          </span>
        </span>
        <span class="block space-y-1.5 px-4 py-3">
          <span class="flex items-center justify-between gap-3">
            <strong
              class="truncate font-mono text-sm text-gray-900 dark:text-gray-100"
              :title="item.result.model_id"
              >{{ item.result.model_id }}</strong
            >
            <span class="shrink-0 text-xs text-gray-600 dark:text-gray-300">{{
              t('modelIdentification.elapsed', {
                seconds: (item.result.latency_ms / 1000).toFixed(1)
              })
            }}</span>
          </span>
          <time
            :datetime="item.result.started_at"
            class="block text-xs text-gray-600 dark:text-gray-300"
            >{{ formatDateTime(item.result.started_at) }}</time
          >
        </span>
      </button>
    </div>
    <div v-if="tiles.length > 1" class="mt-1 flex items-center gap-3">
      <input
        type="range"
        min="0"
        :max="maxScroll"
        step="1"
        :value="scrollLeft"
        :disabled="maxScroll === 0"
        class="min-w-0 flex-1 accent-primary-600"
        :aria-label="t('modelIdentification.historyPosition', { group: groupName })"
        :title="t('modelIdentification.browse')"
        @input="slide"
      />
      <span class="shrink-0 text-xs tabular-nums text-gray-600 dark:text-gray-300"
        >{{ Math.min(tiles.length, Math.floor(scrollLeft / stride) + 1) }}–{{ Math.min(tiles.length, Math.ceil((scrollLeft + viewportWidth) / stride)) }} /
        {{ tiles.length }}</span
      >
    </div>
    <BaseDialog
      :show="selected !== null"
      :title="t('modelIdentification.details')"
      width="extra-wide"
      @close="selected = null"
    >
      <DegradationTestResultCard v-if="selected" :key="selected.id" :result="selected" />
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DegradationTestResult } from '@/types'
import { formatDateTime } from '@/utils/format'
import {
  buildDegradationTestPreview,
  extractDegradationTestHtml
} from '@/utils/degradationTestPreview'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DegradationTestResultCard from '@/components/admin/account/DegradationTestResultCard.vue'

const props = defineProps<{ results: DegradationTestResult[]; groupName: string }>()
const { t } = useI18n()
const track = ref<HTMLElement | null>(null)
const selected = ref<DegradationTestResult | null>(null)
const scrollLeft = ref(0)
const viewportWidth = ref(1000)
const maxScroll = ref(0)
const stride = 284
let observer: ResizeObserver | undefined
let cache = new Map<number, { response: string; preview: string | null }>()
const tiles = computed(() => {
  const next = new Map<number, { response: string; preview: string | null }>()
  const items = [...props.results]
    .sort(
      (a, b) =>
        Date.parse(b.created_at || b.started_at) - Date.parse(a.created_at || a.started_at) ||
        b.id - a.id
    )
    .slice(0, 24)
  const out = items.map((result) => {
    const response = result.status === 'success' ? result.response_text : ''
    let entry = cache.get(result.id)
    if (!entry || entry.response !== response) {
      const html = extractDegradationTestHtml(response)
      entry = { response, preview: html ? buildDegradationTestPreview(html) : null }
    }
    next.set(result.id, entry)
    return { result, preview: entry.preview }
  })
  cache = next
  return out
})
// 只运行视口附近的动画，拖动时即时装载，避免多组共数百个动画同时占用资源。
function shouldRender(index: number) {
  return (
    index >= Math.floor(scrollLeft.value / stride) - 1 &&
    index <= Math.ceil((scrollLeft.value + viewportWidth.value) / stride) + 1
  )
}
function syncScroll() {
  if (!track.value) return
  scrollLeft.value = track.value.scrollLeft
  viewportWidth.value = track.value.clientWidth
  maxScroll.value = Math.max(0, track.value.scrollWidth - track.value.clientWidth)
}
function slide(event: Event) {
  if (!track.value) return
  track.value.scrollLeft = Number((event.target as HTMLInputElement).value)
  syncScroll()
}
watch(tiles, async (items) => {
  if (selected.value && !items.some((item) => item.result.id === selected.value?.id))
    selected.value = null
  await nextTick()
  syncScroll()
})
onMounted(() => {
  syncScroll()
  if (typeof ResizeObserver !== 'undefined' && track.value) {
    observer = new ResizeObserver(syncScroll)
    observer.observe(track.value)
  }
})
onBeforeUnmount(() => observer?.disconnect())
</script>

<style scoped>
.artwork-card {
  width: 264px;
}
.artwork-window {
  width: 262px;
  height: 196.5px;
}
.artwork-frame {
  width: 1048px;
  height: 786px;
  transform: scale(0.25);
  transform-origin: top left;
}
.gallery-track {
  overscroll-behavior-x: contain;
}
</style>
