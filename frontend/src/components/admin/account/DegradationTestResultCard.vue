<template>
  <article class="rounded-xl border border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-900">
    <div class="flex flex-wrap items-center justify-between gap-2 text-xs">
      <div class="flex items-center gap-2">
        <span class="rounded-full px-2 py-1 font-medium" :class="result.status === 'success' ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400' : result.status === 'running' ? 'bg-blue-100 text-blue-700' : 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'">
          {{ t(`admin.degradationTests.${result.status === 'success' ? 'success' : result.status === 'running' ? 'running' : 'failed'}`) }}
        </span>
        <span class="text-gray-500">{{ result.latency_ms }} ms</span>
      </div>
      <time class="text-gray-500">{{ formatDateTime(result.started_at) }}</time>
    </div>
    <p v-if="result.model_id" class="mt-2 break-all text-xs text-gray-500">{{ t('admin.degradationTests.model') }}: {{ result.model_id }}</p>
    <p class="mt-1 text-xs text-gray-500" data-testid="result-reasoning-effort">{{ t('admin.degradationTests.reasoningEffortSnapshot') }}: {{ t(`admin.degradationTests.effort.${result.reasoning_effort || 'default'}`) }}</p>
    <pre v-if="result.error_message" role="alert" class="mt-3 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-red-50 p-3 text-xs text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ result.error_message }}</pre>
    <details v-if="result.prompt" class="mt-3 text-xs text-gray-500">
      <summary class="select-none">{{ t('admin.degradationTests.promptSnapshot') }}</summary>
      <pre class="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-words">{{ result.prompt }}</pre>
    </details>
    <div v-if="html" class="mt-3 flex flex-wrap gap-2">
      <button class="btn btn-primary text-xs" data-testid="preview" @click="previewOpen = !previewOpen">{{ t(`admin.degradationTests.${previewOpen ? 'hidePreview' : 'preview'}`) }}</button>
      <button class="btn btn-secondary text-xs" @click="downloadHtml">{{ t('admin.degradationTests.downloadHtml') }}</button>
    </div>
    <iframe v-if="previewOpen && previewDocument" :srcdoc="previewDocument" sandbox="allow-scripts" referrerpolicy="no-referrer" :title="t('admin.degradationTests.preview')" class="mt-3 h-[460px] w-full rounded-lg border border-gray-200 bg-white" />
    <details v-if="result.response_text" class="mt-3 text-xs">
      <summary class="select-none text-primary-600">{{ t('admin.degradationTests.viewResponse') }}</summary>
      <pre class="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-white p-3 font-mono text-gray-700 dark:bg-dark-800 dark:text-gray-300">{{ result.response_text }}</pre>
    </details>
  </article>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DegradationTestResult } from '@/types'
import { formatDateTime } from '@/utils/format'
import { extractDegradationTestHtml, buildDegradationTestPreview } from '@/utils/degradationTestPreview'

const props = defineProps<{ result: DegradationTestResult }>()
const { t } = useI18n()
const previewOpen = ref(false)
const html = computed(() => extractDegradationTestHtml(props.result.response_text || ''))
const previewDocument = computed(() => previewOpen.value && html.value ? buildDegradationTestPreview(html.value) : '')

function downloadHtml() {
  if (!html.value) return
  const url = URL.createObjectURL(new Blob([html.value], { type: 'text/html;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url
  link.download = `model-test-${props.result.id}.html`
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
</script>
