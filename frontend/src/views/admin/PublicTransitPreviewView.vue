<template>
  <AppLayout>
    <main class="mx-auto max-w-7xl space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-200 pb-5 dark:border-dark-800">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('publicTransit.exportConfig.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('publicTransit.exportConfig.description') }}</p>
        </div>
        <div class="flex items-center gap-2">
          <button type="button" class="btn btn-secondary" :disabled="loading" :title="t('publicTransit.exportConfig.refresh')" @click="load">
            <Icon name="refresh" size="sm" />
            {{ t('publicTransit.exportConfig.refresh') }}
          </button>
          <button type="button" class="btn btn-primary" :disabled="loading || saving || !dirty || !!validationError" @click="save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </header>

      <p v-if="error" role="alert" class="border-l-2 border-red-500 bg-red-50 px-4 py-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
      <p v-if="validationError" role="alert" class="border-l-2 border-red-500 bg-red-50 px-4 py-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ validationError }}</p>

      <section class="grid gap-6 lg:grid-cols-[minmax(360px,440px)_minmax(0,1fr)]">
        <div>
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('publicTransit.exportConfig.sections') }}</h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('publicTransit.exportConfig.required') }}</p>
          <div class="mt-4 divide-y divide-gray-200 border-y border-gray-200 dark:divide-dark-800 dark:border-dark-800">
            <label v-for="item in sectionRows" :key="item.key" class="flex cursor-pointer items-center justify-between gap-3 py-3 text-sm">
              <span class="text-gray-800 dark:text-gray-200">{{ t(`publicTransit.exportConfig.fields.${item.key}`) }}</span>
              <input v-model="sections[item.key]" type="checkbox" class="h-4 w-4 shrink-0 accent-primary-600" :disabled="loading || saving" />
            </label>
          </div>
          <p v-if="dirty" class="mt-3 text-xs text-amber-700 dark:text-amber-300">{{ t('publicTransit.exportConfig.unsaved') }}</p>

          <div class="mt-8 border-t border-gray-200 pt-6 dark:border-dark-800">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('publicTransit.exportConfig.metricsTitle') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('publicTransit.exportConfig.metricsHint') }}</p>
            <input v-model="metricSearch" type="search" class="input mt-4 w-full text-sm" :placeholder="t('publicTransit.exportConfig.searchMetric')" :aria-label="t('publicTransit.exportConfig.searchMetric')" />
            <div v-for="group in metricGroups" :key="group.key" class="mt-4 border-t border-gray-200 dark:border-dark-800">
              <details :open="metricSearch !== ''">
                <summary class="cursor-pointer py-3 text-sm font-semibold text-gray-800 dark:text-gray-200">
                  {{ t(`publicTransit.exportConfig.metricGroups.${group.key}`) }} ({{ group.fields.length }})
                </summary>
                <div class="divide-y divide-gray-100 dark:divide-dark-800">
                  <div v-for="field in group.fields" :key="field.path" class="py-3">
                    <label class="flex items-start gap-2 text-sm text-gray-800 dark:text-gray-200">
                      <input v-model="metricRules[field.path].enabled" type="checkbox" class="mt-0.5 h-4 w-4 shrink-0 accent-primary-600" :disabled="loading || saving" />
                      <span class="min-w-0 break-all font-mono text-xs leading-5">{{ field.path }}</span>
                    </label>
                    <div class="mt-1 pl-6 text-xs text-gray-500 dark:text-dark-300">
                      {{ t('publicTransit.exportConfig.realValue') }}: {{ originalMetricValue(field.path) }} · {{ field.unit }}
                    </div>
                    <div class="mt-2 grid grid-cols-2 gap-2 pl-6">
                      <label class="text-xs text-gray-500 dark:text-dark-300">
                        {{ t('publicTransit.exportConfig.minimum') }}
                        <input v-model.number="metricRules[field.path].min" type="number" :step="field.kind === 'integer' ? 1 : 'any'" min="0" class="input mt-1 w-full text-sm" :disabled="loading || saving || !metricRules[field.path].enabled" />
                      </label>
                      <label class="text-xs text-gray-500 dark:text-dark-300">
                        {{ t('publicTransit.exportConfig.maximum') }}
                        <input v-model.number="metricRules[field.path].max" type="number" :step="field.kind === 'integer' ? 1 : 'any'" min="0" class="input mt-1 w-full text-sm" :disabled="loading || saving || !metricRules[field.path].enabled" />
                      </label>
                    </div>
                    <p v-if="metricRules[field.path].enabled && (metricRules[field.path].min === '' || metricRules[field.path].max === '')" class="mt-1 pl-6 text-xs text-gray-500 dark:text-dark-300">{{ t('publicTransit.exportConfig.incompleteRange') }}</p>
                  </div>
                </div>
              </details>
            </div>
          </div>
        </div>

        <div class="min-w-0">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="inline-flex gap-1 border-b border-gray-200 dark:border-dark-800" role="tablist" :aria-label="t('publicTransit.exportConfig.title')">
              <button v-for="view in views" :key="view" type="button" role="tab" :aria-selected="tab === view" class="px-3 py-2 text-sm" :class="tab === view ? 'border-b-2 border-primary-600 font-medium text-primary-700 dark:text-primary-300' : 'text-gray-500'" @click="tab = view">
                {{ t(`publicTransit.exportConfig.tabs.${view}`) }}
              </button>
            </div>
            <div class="flex items-center gap-2">
              <select v-model="range" class="input h-9 w-24 text-sm" :aria-label="t('publicTransit.passiveRange')" @change="loadPreview">
                <option v-for="option in ranges" :key="option" :value="option">{{ option }}</option>
              </select>
              <button v-if="tab !== 'visual'" type="button" class="btn btn-secondary" :disabled="!preview" :title="t('publicTransit.exportConfig.copy')" @click="copyJSON">
                <Icon name="copy" size="sm" />
              </button>
            </div>
          </div>
          <p class="my-3 text-xs text-gray-500 dark:text-dark-300">{{ t(`publicTransit.exportConfig.hints.${tab}`) }}</p>
          <div v-if="tab === 'visual'" class="-mx-4 border-t border-gray-200 dark:border-dark-800 lg:-mx-6">
            <PublicTransitView :admin-preview="true" />
          </div>
          <pre v-else class="max-h-[70vh] min-h-80 overflow-auto border border-gray-200 bg-gray-50 p-4 text-xs leading-5 text-gray-800 dark:border-dark-800 dark:bg-dark-950 dark:text-gray-200">{{ displayJSON }}</pre>
        </div>
      </section>
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import PublicTransitView from '@/views/public/PublicTransitView.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore } from '@/stores/app'
import {
  getPublicTransitExportPreview,
  getPublicTransitExportMetrics,
  getPublicTransitExportSections,
  updatePublicTransitExportSections,
  type PublicTransitExportPreview,
  type PublicTransitExportSections,
  type PublicTransitMetricField,
  type PublicTransitMetricRange,
} from '@/api/admin/publicTransitExport'

type View = 'public' | 'full' | 'visual'
type Range = '90m' | '24h' | '7d' | '30d'
const views: View[] = ['public', 'full', 'visual']
const ranges: Range[] = ['90m', '24h', '7d', '30d']
type SectionKey = Exclude<keyof PublicTransitExportSections, 'metrics'>
const sectionRows: { key: SectionKey }[] = [
  { key: 'billing' }, { key: 'groups' }, { key: 'monitoring' }, { key: 'passive_monitoring' },
  { key: 'cache' }, { key: 'disclosure' }, { key: 'limits' }, { key: 'completeness' },
]
const defaults: PublicTransitExportSections = {
  billing: true, groups: true, monitoring: true, passive_monitoring: true,
  cache: true, disclosure: true, limits: true, completeness: true,
}
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const appStore = useAppStore()
const sections = ref<PublicTransitExportSections>({ ...defaults })
const savedSections = ref<PublicTransitExportSections>({ ...defaults })
type EditableMetricRange = { enabled: boolean; min: number | ''; max: number | '' }
const metricFields = ref<PublicTransitMetricField[]>([])
const metricRules = ref<Record<string, EditableMetricRange>>({})
const savedMetricRules = ref<Record<string, EditableMetricRange>>({})
const metricSearch = ref('')
const preview = ref<PublicTransitExportPreview | null>(null)
const tab = ref<View>('public')
const range = ref<Range>('24h')
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const dirty = computed(() => JSON.stringify(sections.value) !== JSON.stringify(savedSections.value) || JSON.stringify(metricRules.value) !== JSON.stringify(savedMetricRules.value))
const validationError = computed(() => {
  for (const field of metricFields.value) {
    const rule = metricRules.value[field.path]
    if (!rule?.enabled || rule.min === '' || rule.max === '') continue
    const maximum = field.unit === '0-1' ? 1 : field.unit === '%' || field.unit === '0-100' ? 100 : 1e15
    if (!Number.isFinite(rule.min) || !Number.isFinite(rule.max) || rule.min < 0 || rule.min >= rule.max || rule.max > maximum ||
      (field.kind === 'integer' && (!Number.isInteger(rule.min) || !Number.isInteger(rule.max)))) {
      return `${field.path}: ${t('publicTransit.exportConfig.invalidMetricRange')}`
    }
  }
  return ''
})
const displayJSON = computed(() => JSON.stringify(tab.value === 'full' ? preview.value?.full : preview.value?.public, null, 2) || '')
const metricGroups = computed(() => {
  const search = metricSearch.value.trim().toLowerCase()
  return (['monitoring', 'passive_monitoring', 'groups', 'cache'] as const).map(key => ({
    key,
    fields: metricFields.value.filter(field => field.path.startsWith(`${key}.`) && (!search || field.path.toLowerCase().includes(search))),
  })).filter(group => group.fields.length > 0)
})

function originalMetricValue(path: string): string {
  let values: unknown[] = [preview.value?.full]
  for (const part of path.split('.')) {
    values = values.flatMap(value => part === '*' && Array.isArray(value)
      ? value
      : value && typeof value === 'object' && !Array.isArray(value)
        ? [(value as Record<string, unknown>)[part]]
        : [])
  }
  const numbers = values.filter((value): value is number => typeof value === 'number')
  return numbers.length ? `${numbers[0]}${numbers.length > 1 ? ` (+${numbers.length - 1})` : ''}` : t('publicTransit.exportConfig.noSample')
}

function editableRules(fields: PublicTransitMetricField[], saved: Record<string, PublicTransitMetricRange> = {}): Record<string, EditableMetricRange> {
  return Object.fromEntries(fields.map(field => [field.path, {
    enabled: saved[field.path]?.enabled ?? false,
    min: saved[field.path]?.min ?? '',
    max: saved[field.path]?.max ?? '',
  }]))
}

async function loadPreview() {
  try {
    preview.value = await getPublicTransitExportPreview(range.value)
    error.value = ''
  } catch (reason: any) {
    preview.value = null
    error.value = reason?.response?.data?.message || t('publicTransit.exportConfig.previewFailed')
  }
}

async function load() {
  loading.value = true
  try {
    const [loaded, fields] = await Promise.all([getPublicTransitExportSections(), getPublicTransitExportMetrics()])
    metricFields.value = fields
    metricRules.value = editableRules(fields, loaded.metrics)
    savedMetricRules.value = structuredClone(metricRules.value)
    sections.value = { ...loaded, metrics: undefined }
    savedSections.value = { ...sections.value }
    await loadPreview()
  } catch (reason: any) {
    error.value = reason?.response?.data?.message || t('publicTransit.exportConfig.loadFailed')
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!dirty.value || saving.value) return
  saving.value = true
  try {
    const metrics = Object.fromEntries(Object.entries(metricRules.value)
      .filter(([, rule]) => rule.enabled || rule.min !== '' || rule.max !== '')
      .map(([path, rule]) => [path, { enabled: rule.enabled, min: rule.min === '' ? null : rule.min, max: rule.max === '' ? null : rule.max }]))
    const saved = await updatePublicTransitExportSections({ ...sections.value, metrics })
    sections.value = { ...saved, metrics: undefined }
    savedSections.value = { ...sections.value }
    metricRules.value = editableRules(metricFields.value, saved.metrics)
    savedMetricRules.value = structuredClone(metricRules.value)
    await loadPreview()
    appStore.showSuccess(t('publicTransit.exportConfig.saved'))
  } catch (reason: any) {
    appStore.showError(reason?.response?.data?.message || t('publicTransit.exportConfig.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function copyJSON() {
  await copyToClipboard(displayJSON.value)
}

onMounted(load)
</script>
