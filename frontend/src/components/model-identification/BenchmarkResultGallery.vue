<template>
  <div class="min-w-0">
    <div ref="track" tabindex="0" class="flex min-w-0 gap-3 overflow-x-auto overscroll-x-contain pb-3" :aria-label="t('modelBenchmark.resultGallery', { name: displayName })" @scroll.passive="syncScroll">
      <button v-for="item in cards" :key="item.key" type="button" class="result-card shrink-0 overflow-hidden rounded-xl border border-gray-200 bg-white text-left transition-colors hover:border-primary-400 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:border-dark-600 dark:bg-dark-800" :data-shared-result="item.result.id" :data-shared-schedule="item.schedule.id" :aria-label="`${t('modelBenchmark.details')} · ${item.result.model} · ${formatDate(item.result.finished_at || item.result.created_at)}`" @click="selectedKey = item.key">
        <div class="space-y-2.5 bg-gray-50/70 p-3 dark:bg-dark-900/40">
          <div class="flex items-center justify-between gap-2">
            <span class="rounded px-2 py-0.5 text-xs font-medium" :class="testSource === 'admin' ? 'bg-primary-100 text-primary-800 dark:bg-primary-900/40 dark:text-primary-200' : 'bg-gray-200 text-gray-700 dark:bg-dark-600 dark:text-gray-200'" data-testid="test-source">{{ t(testSource === 'admin' ? 'modelBenchmark.adminTest' : 'modelBenchmark.userTest') }}</span>
            <span class="shrink-0 text-xs" :class="item.result.status === 'completed' ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-300'">{{ t(`modelBenchmark.statuses.${item.result.status}`) }}</span>
          </div>
          <p v-if="item.schedule.name" class="truncate text-xs text-gray-600 dark:text-gray-300" :title="item.schedule.name">{{ item.schedule.name }}</p>
          <div class="flex items-end justify-between gap-2">
            <div><span class="text-xs text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.passRate') }}</span><strong class="block text-2xl font-bold tabular-nums" :class="benchmarkScoreClass(item.result.pass_rate, item.result.status)" data-testid="shared-pass-rate">{{ formatBenchmarkPassRate(item.result.pass_rate, item.result.status) }}</strong></div>
            <div class="text-right text-xs leading-5 text-gray-600 dark:text-gray-300"><p>{{ t('modelBenchmark.passed', { passed: item.result.passed, total: item.result.total }) }}</p><p>{{ item.result.completed }}/{{ item.result.total }} {{ t('modelBenchmark.questionsDone') }}</p></div>
          </div>
          <div class="grid grid-cols-3 gap-3">
            <div v-for="category in categories" :key="category" class="min-w-0 space-y-1">
              <p class="text-xs text-gray-600 dark:text-gray-300">{{ t(`modelBenchmark.categories.${category}`) }}</p>
              <p class="text-xs tabular-nums" :class="benchmarkScoreClass(benchmarkCategoryScore(item.result.categories[category].passed, item.result.categories[category].total), item.result.status)">{{ item.result.categories[category].passed }}/{{ item.result.categories[category].total }}</p>
              <div class="h-1 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600"><div class="h-full rounded-full" :class="benchmarkScoreBarClass(benchmarkCategoryScore(item.result.categories[category].passed, item.result.categories[category].total), item.result.status)" :style="{ width: `${categoryPercent(item.result, category)}%` }" /></div>
            </div>
          </div>
        </div>
        <div class="space-y-1.5 border-t border-gray-100 p-3 dark:border-dark-700">
          <p class="break-all text-sm text-gray-700 dark:text-gray-200"><span data-testid="result-url">{{ item.result.base_url || t('modelBenchmark.urlUnavailable') }}</span><span v-if="item.result.url_masked" class="ml-2 text-xs text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.urlMasked') }}</span></p>
          <div class="flex items-center justify-between gap-2"><strong class="min-w-0 truncate font-mono text-sm" :title="item.result.model">{{ item.result.model }}</strong><span class="shrink-0 text-xs text-gray-500">{{ t('modelBenchmark.seconds', { count: Math.round(item.result.elapsed_seconds) }) }}</span></div>
          <div class="flex items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400"><span>{{ t('modelBenchmark.effort') }}: {{ t(`modelBenchmark.efforts.${item.result.effort}`) }}</span><span>{{ t('modelBenchmark.tokens', { count: item.result.usage ? item.result.usage.total_tokens.toLocaleString() : '—' }) }}</span></div>
          <time class="block text-xs text-gray-500 dark:text-gray-400" :datetime="item.result.finished_at || item.result.created_at">{{ formatDate(item.result.finished_at || item.result.created_at) }}</time>
          <div class="flex items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400"><span>{{ item.result.suite_day }} · v{{ item.result.suite_revision }}</span><code :title="item.result.suite_fingerprint">{{ item.result.suite_fingerprint.slice(0, 8) }}</code></div>
          <BenchmarkErrorSummary :result="item.result" />
          <span class="block text-xs font-medium text-primary-600 dark:text-primary-400">{{ t('modelBenchmark.details') }} →</span>
        </div>
      </button>
    </div>
    <div v-if="cards.length > 1" class="mt-2 flex items-center gap-3"><input type="range" min="0" :max="maxScroll" step="1" :value="scrollLeft" :disabled="!maxScroll" class="min-w-0 flex-1 accent-primary-600" :aria-label="t('modelBenchmark.browseResults', { name: displayName })" @input="slide" /><span class="shrink-0 text-xs tabular-nums text-gray-500">{{ visibleStart }}–{{ visibleEnd }} / {{ cards.length }}</span></div>
    <BaseDialog :show="!!selected" :title="t('modelBenchmark.details')" width="wide" @close="selectedKey = ''">
      <div v-if="selected" class="space-y-5" data-testid="shared-result-detail">
        <div class="space-y-1"><p class="text-sm font-medium text-gray-700 dark:text-gray-200" data-testid="detail-test-source">{{ t(testSource === 'admin' ? 'modelBenchmark.adminTest' : 'modelBenchmark.userTest') }}</p><p class="text-xs text-gray-500">{{ displayName }}<template v-if="selected.schedule.name"> · {{ selected.schedule.name }}</template></p><h3 class="break-all text-lg font-semibold">{{ selected.result.model }}</h3><p class="text-sm text-gray-500">{{ t(`modelBenchmark.statuses.${selected.result.status}`) }} · {{ t('modelBenchmark.effort') }}: {{ t(`modelBenchmark.efforts.${selected.result.effort}`) }}</p></div>
        <div class="space-y-1"><p class="text-xs text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.url') }}</p><p class="break-all text-sm" data-testid="detail-url">{{ selected.result.base_url || t('modelBenchmark.urlUnavailable') }}</p><p v-if="selected.result.url_masked" class="text-xs text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.urlMasked') }}</p></div>
        <div class="rounded-xl bg-primary-50 p-4 dark:bg-primary-900/20"><p class="text-xs text-gray-500">{{ t('modelBenchmark.passRate') }}</p><strong class="text-4xl tabular-nums" :class="benchmarkScoreClass(selected.result.pass_rate, selected.result.status)" data-testid="shared-detail-pass-rate">{{ formatBenchmarkPassRate(selected.result.pass_rate, selected.result.status) }}</strong><p class="mt-2 text-sm">{{ t('modelBenchmark.passed', { passed: selected.result.passed, total: selected.result.total }) }} · {{ t('modelBenchmark.completed', { completed: selected.result.completed, total: selected.result.total }) }}</p></div>
        <div class="grid grid-cols-3 gap-3"><div v-for="category in categories" :key="category" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800"><p class="text-xs text-gray-500">{{ t(`modelBenchmark.categories.${category}`) }}</p><strong class="mt-1 block text-lg" :class="benchmarkScoreClass(benchmarkCategoryScore(selected.result.categories[category].passed, selected.result.categories[category].total), selected.result.status)">{{ selected.result.categories[category].passed }}/{{ selected.result.categories[category].total }}</strong></div></div>
        <dl class="grid gap-3 text-sm sm:grid-cols-2"><div><dt class="text-xs text-gray-500">{{ t('modelBenchmark.finishedAt') }}</dt><dd>{{ formatDate(selected.result.finished_at || selected.result.created_at) }}</dd></div><div><dt class="text-xs text-gray-500">{{ t('modelBenchmark.elapsed') }}</dt><dd>{{ t('modelBenchmark.seconds', { count: selected.result.elapsed_seconds }) }}</dd></div><div><dt class="text-xs text-gray-500">{{ t('modelBenchmark.suiteVersion') }}</dt><dd>{{ selected.result.suite_day }} · v{{ selected.result.suite_revision }}</dd></div><div><dt class="text-xs text-gray-500">{{ t('modelBenchmark.tokenBreakdown') }}</dt><dd>{{ selected.result.usage ? `${selected.result.usage.input_tokens.toLocaleString()} / ${selected.result.usage.output_tokens.toLocaleString()} / ${selected.result.usage.total_tokens.toLocaleString()}` : '—' }}</dd></div><div class="sm:col-span-2"><dt class="text-xs text-gray-500">{{ t('modelBenchmark.suiteFingerprint') }}</dt><dd class="break-all font-mono text-xs">{{ selected.result.suite_fingerprint }}</dd></div></dl>
        <BenchmarkErrorSummary :result="selected.result" />
        <p class="text-xs leading-5 text-gray-500">{{ t('modelBenchmark.passRateHint') }}</p>
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import BenchmarkErrorSummary from './BenchmarkErrorSummary.vue'
import type { Category } from '@/api/admin/model-benchmarks'
import { formatBenchmarkPassRate, benchmarkScoreClass, benchmarkCategoryScore, benchmarkScoreBarClass } from '@/utils/benchmarkScore'
import type { SharedResult, SharedSchedule } from '@/api/model-benchmarks'
const props = defineProps<{ items: SharedSchedule[]; displayName: string; testSource: 'admin' | 'user' }>()
const { t } = useI18n()
const categories: Category[] = ['coding', 'math', 'reasoning']
const track = ref<HTMLElement | null>(null)
const scrollLeft = ref(0)
const viewportWidth = ref(0)
const cardWidth = ref(280)
const maxScroll = ref(0)
const selectedKey = ref('')
let observer: ResizeObserver | undefined
const cards = computed(() => props.items.flatMap(schedule => schedule.results.map(result => ({ schedule, result, key: `${schedule.id}:${result.id}` }))).sort((a, b) => Date.parse(b.result.finished_at || b.result.created_at) - Date.parse(a.result.finished_at || a.result.created_at)))
const selected = computed(() => cards.value.find(card => card.key === selectedKey.value))
const stride = computed(() => cardWidth.value + 12)
const visibleStart = computed(() => Math.min(cards.value.length, Math.floor(scrollLeft.value / stride.value) + 1))
const visibleEnd = computed(() => Math.min(cards.value.length, Math.ceil((scrollLeft.value + viewportWidth.value) / stride.value)))
const formatDate = (value: string) => new Date(value).toLocaleString()
const categoryPercent = (result: SharedResult, category: Category) => result.categories[category].total ? result.categories[category].passed / result.categories[category].total * 100 : 0
function syncScroll() {
  if (!track.value) return
  scrollLeft.value = track.value.scrollLeft
  viewportWidth.value = track.value.clientWidth
  cardWidth.value = track.value.querySelector('.result-card')?.getBoundingClientRect().width || 280
  maxScroll.value = Math.max(0, track.value.scrollWidth - track.value.clientWidth)
}
function slide(event: Event) { if (track.value) { track.value.scrollLeft = Number((event.target as HTMLInputElement).value); syncScroll() } }
watch(cards, async () => { if (!selected.value) selectedKey.value = ''; await nextTick(); syncScroll() })
onMounted(() => { syncScroll(); if (typeof ResizeObserver !== 'undefined' && track.value) { observer = new ResizeObserver(syncScroll); observer.observe(track.value) } })
onBeforeUnmount(() => observer?.disconnect())
</script>

<style scoped>
.result-card { width: 280px; max-width: 100%; }
</style>
