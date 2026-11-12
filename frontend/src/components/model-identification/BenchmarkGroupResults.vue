<template>
  <div class="min-w-0 space-y-6">
    <nav class="flex max-w-full w-fit gap-1 overflow-x-auto rounded-lg bg-gray-100 p-1 dark:bg-dark-800" :aria-label="t('modelBenchmark.browseGroups')">
      <button type="button" class="shrink-0 rounded-md px-3 py-1.5 text-xs" :class="activeGroup === null ? 'bg-white font-semibold dark:bg-dark-600' : ''" :aria-pressed="activeGroup === null" data-testid="all-result-groups" @click="activeGroup = null">{{ t('modelBenchmark.allGroups') }}</button>
      <button v-for="group in groups" :key="group.key" type="button" class="max-w-64 shrink-0 truncate rounded-md px-3 py-1.5 text-xs" :class="activeGroup === group.key ? 'bg-white font-semibold dark:bg-dark-600' : ''" :title="group.name" :aria-pressed="activeGroup === group.key" :data-result-group-filter="group.key" @click="activeGroup = group.key">{{ group.name }}</button>
    </nav>
    <section v-for="group in displayedGroups" :key="group.key" class="min-w-0 space-y-4 border-b border-gray-200 pb-7 last:border-0 last:pb-0 dark:border-dark-700" :aria-label="group.name" :data-result-group="group.key">
      <header class="min-w-0 space-y-1">
        <h4 class="break-words text-base font-semibold">{{ group.name }}</h4>
        <div class="flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-600 dark:text-gray-300" data-testid="result-group-summary">
          <span class="break-all">{{ group.models.join(' / ') }}</span>
          <span>{{ t('modelBenchmark.effort') }}: {{ group.efforts.map(effort => t(`modelBenchmark.efforts.${effort}`)).join(' / ') }}</span>
          <span>{{ t('modelBenchmark.loadedResults', { count: group.count }) }}</span>
          <time :datetime="group.latest">{{ t('modelBenchmark.latestTest', { time: formatDateTime(group.latest) }) }}</time>
        </div>
      </header>
      <BenchmarkResultGallery :items="group.items" :display-name="group.name" test-source="admin" />
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SharedDisplay, SharedSchedule } from '@/api/model-benchmarks'
import type { Effort } from '@/api/admin/model-benchmarks'
import { formatDateTime } from '@/utils/format'
import BenchmarkResultGallery from './BenchmarkResultGallery.vue'

const props = defineProps<{ items: SharedSchedule[]; sources: SharedDisplay[] }>()
const { t } = useI18n()
const activeGroup = ref<string | null>(null)
interface ResultGroup {
  key: string
  name: string
  items: SharedSchedule[]
  models: string[]
  efforts: Effort[]
  count: number
  latest: string
}
const groups = computed(() => {
  const sources = new Map(props.sources.map(source => [source.id, source]))
  const grouped = new Map<string, ResultGroup>()
  const seen = new Map<string, Set<string>>()
  const results = props.items.flatMap(schedule => schedule.results.map(result => ({ schedule, result })))
    .sort((a, b) => Date.parse(b.result.finished_at || b.result.created_at) - Date.parse(a.result.finished_at || a.result.created_at))
  for (const { schedule, result } of results) {
    const sourceID = schedule.id.split(':', 1)[0]
    const source = sources.get(sourceID)
    // Local schedules may share a group; IDs from other sites are only unique within that source.
    // Historical results use their saved group, even when a schedule's target has since changed.
    const key = JSON.stringify(result.group_id
      ? [source?.source_type === 'local' ? 'local' : sourceID, result.group_id]
      : [sourceID, 'legacy'])
    let group = grouped.get(key)
    if (!group) {
      const groupName = result.group_name || t('modelBenchmark.ungroupedResults')
      const name = source && (source.source_type === 'remote' || !result.group_id) ? `${source.name} · ${groupName}` : groupName
      group = { key, name, items: [], models: [], efforts: [], count: 0, latest: result.finished_at || result.created_at }
      grouped.set(key, group)
      seen.set(key, new Set())
    }
    // A local result can appear in more than one display configuration.
    const resultKey = source?.source_type === 'local' ? result.id : JSON.stringify([schedule.id, result.id])
    if (seen.get(key)!.has(resultKey)) continue
    seen.get(key)!.add(resultKey)
    let item = group.items.find(item => item.id === schedule.id)
    if (!item) {
      item = { ...schedule, results: [] }
      group.items.push(item)
    }
    item.results.push(result)
    if (!group.models.includes(result.model)) group.models.push(result.model)
    if (!group.efforts.includes(result.effort)) group.efforts.push(result.effort)
    group.count++
  }
  return [...grouped.values()]
})
const displayedGroups = computed(() => groups.value.filter(group => activeGroup.value === null || group.key === activeGroup.value))
watch(groups, value => {
  if (activeGroup.value !== null && !value.some(group => group.key === activeGroup.value)) activeGroup.value = null
})
</script>
