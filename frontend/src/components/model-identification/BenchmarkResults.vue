<template>
  <section class="space-y-6 rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900 sm:p-6" data-testid="site-results-section" :aria-label="t('modelBenchmark.resultsTitle')">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="text-lg font-semibold">{{ t('modelBenchmark.resultsTitle') }}</h2>
      <button type="button" class="btn btn-secondary" :disabled="rows.some(row => row.loading)" @click="emit('refresh')">{{ t('modelBenchmark.refreshResults') }}</button>
    </header>
    <section v-for="row in rows" :key="row.kind" class="min-w-0 space-y-3 border-t border-gray-100 pt-5 dark:border-dark-800" :data-testid="`${row.kind}-result-row`" :aria-label="t(row.title)" :aria-busy="row.loading">
      <h3 class="font-semibold">{{ t(row.title) }}</h3>
      <div v-if="row.sources.length" class="flex flex-wrap gap-x-5 gap-y-2 text-xs text-gray-600 dark:text-gray-300">
        <span v-for="source in row.sources" :key="source.id" :data-display-id="source.id">
          {{ source.name }} · {{ t(source.source_type === 'local' ? 'modelBenchmark.localSource' : 'modelBenchmark.remoteSource') }}
          <span v-if="source.last_success_at"> · {{ t('modelBenchmark.lastFetch', { time: new Date(source.last_success_at).toLocaleString() }) }}</span>
          <span v-if="source.stale" class="text-amber-700 dark:text-amber-300"> · {{ t('modelBenchmark.sourceStale') }}</span>
          <span v-else-if="source.pending"> · {{ t('modelBenchmark.fetchingSource') }}</span>
        </span>
      </div>
      <p v-if="row.error" role="alert" class="text-sm text-red-700 dark:text-red-300">
        {{ t('modelBenchmark.loadError') }}
        <button type="button" class="ml-2 underline" :disabled="row.loading" @click="row.failedMore ? loadMore(row) : refreshRow(row)">{{ t('modelBenchmark.retry') }}</button>
      </p>
      <p v-if="row.loading && !row.items.length" role="status" class="py-4 text-sm text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.loading') }}</p>
      <p v-else-if="!row.items.length && !row.error" class="py-4 text-sm text-gray-600 dark:text-gray-300">{{ t(row.kind === 'site' ? 'modelBenchmark.noSharedResults' : 'modelBenchmark.noUserResults') }}</p>
      <BenchmarkResultGallery v-if="row.items.length" :items="row.items" :display-name="t(row.title)" :test-source="row.kind === 'site' ? 'admin' : 'user'" />
      <div v-if="row.items.length" class="flex flex-wrap items-center justify-between gap-3 text-sm">
        <span class="text-gray-600 dark:text-gray-300" role="status">{{ t('modelBenchmark.loadedResults', { count: row.items.reduce((sum, item) => sum + item.results.length, 0) }) }}</span>
        <button v-if="row.cursor" type="button" class="btn btn-secondary" :disabled="row.loading" :data-testid="`${row.kind}-load-more`" @click="loadMore(row)">{{ t(row.loading ? 'modelBenchmark.loading' : 'modelBenchmark.loadMoreResults') }}</button>
        <span v-else class="text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.allResultsLoaded') }}</span>
      </div>
    </section>
    <p class="text-xs text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.passRateHint') }}</p>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { benchmarks } from '@/api/admin/model-benchmarks'
import { userBenchmarks, type SharedDisplay, type SharedSchedule } from '@/api/model-benchmarks'
import BenchmarkResultGallery from './BenchmarkResultGallery.vue'

const props = defineProps<{ userMode: boolean }>()
const emit = defineEmits<{ refresh: [] }>()
const { t } = useI18n()
const controller = new AbortController()
interface Row {
  kind: 'site' | 'users'
  title: string
  items: SharedSchedule[]
  sources: SharedDisplay[]
  cursor: string | null
  pages: number
  loading: boolean
  error: boolean
  failedMore: boolean
}
const rows = reactive<Row[]>(['site', 'users'].map(kind => ({ kind: kind as Row['kind'], title: kind === 'site' ? 'modelBenchmark.showcase' : 'modelBenchmark.userResults', items: [], sources: [], cursor: null, pages: 1, loading: false, error: false, failedMore: false })))

async function fetchPage(row: Row, cursor?: string) {
  const api = props.userMode ? userBenchmarks : benchmarks
  if (row.kind === 'users') {
    const page = await api.results(controller.signal, cursor)
    return { items: page.items, sources: [] as SharedDisplay[], cursor: page.next_cursor ?? null }
  }
  const page = await api.showcase(controller.signal, cursor)
  return {
    items: page.items.flatMap(source => source.items.map(item => ({ ...item, id: `${source.id}:${item.id}`, name: `${source.name} · ${item.name}` }))),
    sources: page.items,
    cursor: page.next_cursor ?? null
  }
}

function mergeItems(items: SharedSchedule[]): SharedSchedule[] {
  const seen = new Set<string>()
  return items.map(item => ({ ...item, results: item.results.filter(result => {
    const key = `${item.id}:${result.id}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  }) })).filter(item => item.results.length)
}

async function refreshRow(row: Row) {
  if (row.loading) return
  row.loading = true
  try {
    // 重取已展开的页，保留浏览深度，并及时移除撤销公开的来源。
    let page = await fetchPage(row)
    if (controller.signal.aborted) return
    let items = page.items
    const sources = page.sources
    if (row.kind === 'site') {
      // 来源列表每页均完整返回；撤销展示立即生效，不等待历史页全部成功。
      const activeSources = new Set(sources.map(source => source.id))
      row.sources = sources
      row.items = row.items.filter(item => activeSources.has(item.id.split(':', 1)[0]))
    }
    let pages = 1
    while (page.cursor && pages < row.pages) {
      page = await fetchPage(row, page.cursor)
      items = items.concat(page.items)
      pages++
    }
    if (controller.signal.aborted) return
    Object.assign(row, { items: mergeItems(items), sources, cursor: page.cursor, pages, error: false, failedMore: false })
  } catch {
    if (!controller.signal.aborted) { row.error = true; row.failedMore = false }
  } finally { row.loading = false }
}

async function loadMore(row: Row) {
  if (row.loading || !row.cursor) return
  row.loading = true
  try {
    const page = await fetchPage(row, row.cursor)
    if (controller.signal.aborted) return
    row.items = mergeItems([...row.items, ...page.items])
    row.cursor = page.cursor
    row.pages++
    row.error = false
  } catch {
    if (!controller.signal.aborted) { row.error = true; row.failedMore = true }
  } finally { row.loading = false }
}

async function refresh() { await Promise.allSettled(rows.map(refreshRow)) }
defineExpose({ refresh })
onBeforeUnmount(() => controller.abort())
</script>
