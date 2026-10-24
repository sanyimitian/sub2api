<template>
  <div class="min-w-0" data-testid="results-gallery">
    <div v-if="latest" class="mb-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
      <span class="font-medium" :class="latest.status === 'success' ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
        {{ t(`admin.degradationTests.${latest.status === 'success' ? 'normal' : 'failed'}`) }}
      </span>
      <span class="text-gray-500">{{ ((latest.latency_ms || 0) / 1000).toFixed(1) }}s</span>
      <span class="text-gray-400">{{ relativeTime(latest.started_at) }}</span>
      <span class="ml-auto text-gray-400">{{ t(`admin.degradationTests.${displayLimit > 10 ? 'allResults' : 'retentionSummary'}`) }}</span>
    </div>

    <div class="flex gap-1.5 pb-1.5" :class="wrap ? 'flex-wrap' : 'overflow-x-auto'" :aria-label="t(`admin.degradationTests.${displayLimit > 10 ? 'allResults' : 'recentResults'}`)">
      <button
        v-for="item in tiles"
        :key="item.result.id"
        type="button"
        class="result-tile group relative shrink-0 overflow-hidden rounded-lg border-2 bg-white text-left transition-shadow hover:shadow-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2 dark:bg-dark-900"
        :class="[
          item.result.status === 'success' ? 'border-emerald-500' : 'border-amber-500',
          selectedId === item.result.id ? 'ring-2 ring-primary-400 ring-offset-2 dark:ring-offset-dark-800' : ''
        ]"
        :data-testid="`result-tile-${item.result.id}`"
        :aria-label="`${t('admin.degradationTests.viewDetails')}: ${formatDateTime(item.result.started_at)}`"
        :aria-expanded="selectedId === item.result.id"
        :title="item.result.error_message || formatDateTime(item.result.started_at)"
        @click="selectResult(item.result.id)"
      >
        <span class="result-artwork relative block overflow-hidden" aria-hidden="true">
          <iframe
            v-if="item.preview && item.result.status === 'success'"
            :srcdoc="item.preview"
            sandbox="allow-scripts"
            referrerpolicy="no-referrer"
            tabindex="-1"
            :title="`${t('admin.degradationTests.preview')} ${item.result.id}`"
            class="result-thumbnail pointer-events-none absolute left-0 top-0 border-0 bg-white"
          />
          <span v-else-if="item.result.status !== 'success'" class="block h-full overflow-hidden whitespace-pre-wrap break-words px-1.5 pb-1 pt-6 font-mono text-[10px] leading-[13px] text-amber-700 dark:text-amber-400">{{ item.result.error_message || t('admin.degradationTests.runFailed') }}</span>
          <span v-else class="block h-full overflow-hidden whitespace-pre-wrap break-words px-1.5 pb-1 pt-6 text-[10px] leading-[13px] text-gray-600 dark:text-gray-300">{{ item.result.response_text || t('admin.degradationTests.noPreview') }}</span>
        </span>
        <span class="absolute right-0.5 top-0.5 rounded px-1 py-0.5 text-[10px] font-medium leading-3 text-white" :class="item.result.status === 'success' ? 'bg-emerald-500' : 'bg-amber-500'">
          {{ t(`admin.degradationTests.${item.result.status === 'success' ? 'normal' : 'failed'}`) }}
        </span>
        <time :datetime="item.result.started_at" class="block truncate px-1.5 py-1 text-[10px] leading-3 text-gray-400">{{ relativeTime(item.result.started_at) }}</time>
      </button>
    </div>

    <div v-if="tiles.length" class="mt-1 flex items-center justify-between gap-3">
      <div class="flex gap-0.5" :aria-label="t('admin.degradationTests.history')">
        <button v-for="item in tiles" :key="item.result.id" type="button" class="h-3 w-1.5 rounded-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500" :class="item.result.status === 'success' ? 'bg-emerald-500' : 'bg-amber-500'" :title="`${formatDateTime(item.result.started_at)} · ${t(`admin.degradationTests.${item.result.status === 'success' ? 'normal' : 'failed'}`)}`" :aria-label="`${t('admin.degradationTests.viewDetails')}: ${formatDateTime(item.result.started_at)}`" @click="selectResult(item.result.id)" />
      </div>
      <button v-if="showDetailsButton" class="text-xs text-primary-600 hover:underline dark:text-primary-400" data-testid="details" @click="emit('details')">
        {{ t('admin.degradationTests.details') }}
      </button>
    </div>

    <div v-if="selectedResult" class="mt-4" data-testid="result-detail">
      <DegradationTestResultCard :key="selectedResult.id" :result="selectedResult" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DegradationTestResult } from '@/types'
import { formatDateTime } from '@/utils/format'
import { buildDegradationTestPreview, extractDegradationTestHtml } from '@/utils/degradationTestPreview'
import DegradationTestResultCard from './DegradationTestResultCard.vue'

const props = withDefaults(defineProps<{ results: DegradationTestResult[]; displayLimit?: number; showDetailsButton?: boolean; wrap?: boolean }>(), {
  displayLimit: 10,
  showDetailsButton: true,
  wrap: false
})
const emit = defineEmits<{ details: [] }>()
const { t } = useI18n()
const selectedId = ref<number | null>(null)
const now = ref(Date.now())
const relativeTimer = setInterval(() => { now.value = Date.now() }, 30000)
onBeforeUnmount(() => clearInterval(relativeTimer))

// Keep a stable preview document across polling, so playing animations continue.
let previewCache = new Map<number, { response: string; status: string; preview: string | null }>()
const tiles = computed(() => {
  const nextCache = new Map<number, { response: string; status: string; preview: string | null }>()
  const recent = [...props.results].sort((a, b) => {
    const delta = new Date(b.started_at).getTime() - new Date(a.started_at).getTime()
    return (Number.isFinite(delta) && delta !== 0 ? delta : b.id - a.id)
  }).slice(0, Math.max(1, Math.min(props.displayLimit, 24)))
  const result = recent.map(item => {
    const response = item.response_text || ''
    let cached = previewCache.get(item.id)
    if (!cached || cached.response !== response || cached.status !== item.status) {
      const html = item.status === 'success' ? extractDegradationTestHtml(response) : null
      cached = { response, status: item.status, preview: html ? buildDegradationTestPreview(html) : null }
    }
    nextCache.set(item.id, cached)
    return { result: item, preview: cached.preview }
  })
  previewCache = nextCache
  return result
})
const latest = computed(() => tiles.value[0]?.result)
const selectedResult = computed(() => tiles.value.find(item => item.result.id === selectedId.value)?.result)
watch(tiles, items => {
  if (selectedId.value !== null && !items.some(item => item.result.id === selectedId.value)) selectedId.value = null
})

function selectResult(id: number) { selectedId.value = selectedId.value === id ? null : id }
function relativeTime(date: string): string {
  const delta = Math.max(0, now.value - new Date(date).getTime())
  if (!Number.isFinite(delta) || delta < 60000) return t('admin.degradationTests.relativeJustNow')
  const minutes = Math.floor(delta / 60000)
  if (minutes < 60) return t('admin.degradationTests.relativeMinutes', { count: minutes })
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return t('admin.degradationTests.relativeHours', { count: hours })
  return t('admin.degradationTests.relativeDays', { count: Math.floor(hours / 24) })
}
</script>

<style scoped>
.result-tile { width: 104px; }
.result-artwork { width: 100px; height: 80px; }
.result-thumbnail { width: 800px; height: 640px; transform: scale(0.125); transform-origin: top left; }
</style>
