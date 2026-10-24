<template>
  <article class="flex min-w-0 flex-col overflow-hidden rounded-2xl border bg-white shadow-sm transition-shadow hover:shadow-md dark:bg-dark-800" :class="borderClass" :data-testid="`intelligence-card-${item.plan.id}`">
    <div class="flex items-start justify-between gap-3 p-4">
      <div class="min-w-0">
        <p class="break-all text-sm font-semibold text-gray-900 dark:text-gray-100">{{ item.account_name || `#${item.plan.account_id}` }}</p>
        <p class="mt-1 break-all font-mono text-xs text-gray-500" data-testid="tested-model">{{ item.latest_result?.model_id || item.plan.model_id }}</p>
      </div>
      <span class="shrink-0 rounded-full px-2 py-1 text-xs font-medium" :class="badgeClass" data-testid="latest-status">{{ t(`admin.intelligenceStatus.${status}`) }}</span>
    </div>

    <div class="preview-window relative mx-4 overflow-hidden rounded-xl border border-gray-100 bg-gray-50 dark:border-dark-700 dark:bg-dark-900">
      <iframe v-if="preview" :srcdoc="preview" sandbox="allow-scripts" referrerpolicy="no-referrer" loading="lazy" tabindex="-1" :title="t('admin.intelligenceStatus.previewTitle', { model: item.latest_result?.model_id || item.plan.model_id })" class="preview-frame pointer-events-none absolute left-0 top-0 border-0 bg-white" />
      <div v-else class="flex h-full items-center justify-center p-4 text-center text-xs text-gray-500 dark:text-gray-400">
        <pre v-if="item.latest_result?.status === 'failed'" class="max-h-full overflow-auto whitespace-pre-wrap break-words text-left font-mono text-red-600 dark:text-red-400" data-testid="latest-error">{{ item.latest_result.error_message || t('admin.intelligenceStatus.failedWithoutMessage') }}</pre>
        <p v-else-if="!item.latest_result">{{ t('admin.intelligenceStatus.notTestedHelp') }}</p>
        <pre v-else class="max-h-full overflow-auto whitespace-pre-wrap break-words text-left font-mono">{{ item.latest_result.response_text || t('admin.intelligenceStatus.noPreview') }}</pre>
      </div>
    </div>

    <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2 px-4 py-4 text-xs">
      <dt class="text-gray-500">{{ t('admin.intelligenceStatus.testTime') }}</dt>
      <dd class="text-right text-gray-700 dark:text-gray-300"><time v-if="item.latest_result" :datetime="item.latest_result.started_at">{{ formatDateTime(item.latest_result.started_at) }}</time><span v-else>—</span></dd>
      <dt class="text-gray-500">{{ t('admin.intelligenceStatus.reasoningEffort') }}</dt>
      <dd class="break-words text-right text-gray-700 dark:text-gray-300">{{ t(`admin.degradationTests.effort.${effort || 'default'}`) }}</dd>
      <dt class="text-gray-500">{{ t('admin.intelligenceStatus.schedule') }}</dt>
      <dd class="break-words text-right text-gray-700 dark:text-gray-300">{{ item.plan.enabled ? t('admin.intelligenceStatus.enabled') : t('admin.intelligenceStatus.disabled') }} · {{ item.plan.cron_expression }}</dd>
      <template v-if="item.latest_result?.model_id && item.latest_result.model_id !== item.plan.model_id">
        <dt class="text-gray-500">{{ t('admin.intelligenceStatus.currentModel') }}</dt>
        <dd class="break-all text-right text-gray-700 dark:text-gray-300">{{ item.plan.model_id }}</dd>
      </template>
      <template v-if="item.latest_result">
        <dt class="text-gray-500">{{ t('admin.intelligenceStatus.duration') }}</dt>
        <dd class="text-right tabular-nums text-gray-700 dark:text-gray-300">{{ (item.latest_result.latency_ms / 1000).toFixed(1) }} s</dd>
      </template>
    </dl>
    <p v-if="error" role="alert" class="mx-4 mb-3 whitespace-pre-wrap break-words text-xs text-red-600 dark:text-red-400">{{ error }}</p>
    <div class="mt-auto flex gap-2 border-t border-gray-100 p-4 dark:border-dark-700">
      <button type="button" class="btn btn-primary min-w-0 flex-1 gap-1.5 text-xs" :data-testid="`run-status-${item.plan.id}`" :disabled="running" @click="emit('run')"><Icon :name="running ? 'refresh' : 'play'" size="sm" :class="running ? 'animate-spin' : ''" aria-hidden="true" />{{ t(`admin.intelligenceStatus.${running ? 'running' : 'runNow'}`) }}</button>
      <button type="button" class="btn btn-secondary min-w-0 flex-1 gap-1.5 text-xs" :data-testid="`history-status-${item.plan.id}`" @click="emit('history')"><Icon name="clock" size="sm" aria-hidden="true" />{{ t('admin.intelligenceStatus.history') }}</button>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { DegradationTestStatus } from '@/types'
import { formatDateTime } from '@/utils/format'
import { buildDegradationTestPreview, extractDegradationTestHtml } from '@/utils/degradationTestPreview'

const props = defineProps<{ item: DegradationTestStatus; running?: boolean; error?: string }>()
const emit = defineEmits<{ run: []; history: [] }>()
const { t } = useI18n()
const status = computed(() => props.running ? 'running' : props.item.latest_result?.status === 'success' ? 'success' : props.item.latest_result ? 'failed' : 'notTested')
const effort = computed(() => props.item.latest_result ? (props.item.latest_result.reasoning_effort ?? '') : (props.item.plan.reasoning_effort ?? ''))
const borderClass = computed(() => status.value === 'success' ? 'border-emerald-200 dark:border-emerald-900/60' : status.value === 'failed' ? 'border-red-200 dark:border-red-900/60' : 'border-gray-200 dark:border-dark-700')
const badgeClass = computed(() => status.value === 'success' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400' : status.value === 'failed' ? 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-400' : status.value === 'running' ? 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400')
// Depend on source text/status, not polling object identity, to keep animations playing.
const responseText = computed(() => props.item.latest_result?.status === 'success' ? props.item.latest_result.response_text : '')
const preview = computed(() => {
  const html = extractDegradationTestHtml(responseText.value || '')
  return html ? buildDegradationTestPreview(html) : ''
})
</script>

<style scoped>
.preview-window { height: 200px; }
.preview-frame { width: 250%; height: 500px; transform: scale(0.4); transform-origin: top left; }
</style>
