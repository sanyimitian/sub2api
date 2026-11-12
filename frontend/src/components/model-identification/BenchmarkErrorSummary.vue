<template>
  <div v-if="lines.length" class="space-y-1 rounded-lg bg-amber-50 p-2 text-xs leading-5 text-amber-800 dark:bg-amber-900/20 dark:text-amber-200" data-testid="benchmark-error-summary">
    <p class="font-medium">{{ t('modelBenchmark.errorSummaryTitle') }}</p>
    <p v-for="(line, index) in lines" :key="index">{{ line }}</p>
    <p v-if="result.status === 'failed'">{{ t('modelBenchmark.errorScoreUnavailable') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { BenchmarkRun } from '@/api/admin/model-benchmarks'

const props = defineProps<{
  result: Pick<BenchmarkRun, 'status'> & Partial<Pick<BenchmarkRun, 'api_errors' | 'api_error_details' | 'grading_errors' | 'incomplete_responses'>>
}>()
const { t } = useI18n()
const count = (value: number | undefined) => typeof value === 'number' && Number.isInteger(value) && value > 0 && value <= 100 ? value : 0
const lines = computed(() => {
  const result = props.result
  const messages: string[] = []
  let remaining = count(result.api_errors)
  for (const detail of Array.isArray(result.api_error_details) ? result.api_error_details.slice(0, 100) : []) {
    if (!detail || !remaining) continue
    const affected = Math.min(count(detail.count), remaining)
    if (!affected) continue
    let key = 'apiErrorUnknown'
    const status = detail.http_status
    if (detail.code === 'http_error' && typeof status === 'number' && Number.isInteger(status) && status >= 300 && status <= 599) {
      key = [408, 504, 524].includes(status) ? 'apiErrorTimeout'
        : status === 429 ? 'apiErrorRateLimit'
          : [401, 403].includes(status) ? 'apiErrorAuth' : 'apiErrorHttp'
    } else if (detail.code === 'request_error') key = 'apiErrorRequest'
    else if (detail.code === 'response_error') key = 'apiErrorResponse'
    else if (detail.code === 'cancelled') key = 'apiErrorCancelled'
    messages.push(t(`modelBenchmark.${key}`, { count: affected, status }))
    remaining -= affected
  }
  if (remaining) messages.push(t('modelBenchmark.apiErrorUnknown', { count: remaining }))
  if (count(result.grading_errors)) messages.push(t('modelBenchmark.gradingErrorSummary', { count: result.grading_errors }))
  if (count(result.incomplete_responses)) messages.push(t('modelBenchmark.incompleteErrorSummary', { count: result.incomplete_responses }))
  return messages
})
</script>
