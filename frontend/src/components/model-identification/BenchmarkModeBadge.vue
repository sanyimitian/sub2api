<template>
  <span class="inline-block rounded bg-gray-100 px-2 py-0.5 text-xs font-normal text-gray-600 dark:bg-dark-700 dark:text-gray-300" data-testid="evaluation-mode" :title="t(mode === 'claude_code' ? 'modelBenchmark.claudeCodeHint' : 'modelBenchmark.protocol')">{{ t(`modelBenchmark.modes.${mode || 'responses'}`) }}</span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { EvaluationMode } from '@/api/admin/model-benchmarks'
defineProps<{ mode?: EvaluationMode }>()
const { t } = useI18n()
</script>
