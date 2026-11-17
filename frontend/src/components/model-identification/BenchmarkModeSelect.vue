<template>
  <label class="block space-y-1 text-sm">
    <span>{{ t('modelBenchmark.evaluationMode') }}</span>
    <select :value="modelValue || 'responses'" name="evaluation_mode" class="input w-full" :disabled="disabled" @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value as EvaluationMode)">
      <option value="responses">{{ t('modelBenchmark.modes.responses') }}</option>
      <option value="claude_code">{{ t('modelBenchmark.modes.claude_code') }}</option>
    </select>
    <span class="block text-xs leading-5 text-gray-500">{{ t(modelValue === 'claude_code' ? 'modelBenchmark.claudeCodeHint' : 'modelBenchmark.protocol') }}</span>
  </label>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { EvaluationMode } from '@/api/admin/model-benchmarks'
defineProps<{ modelValue?: EvaluationMode; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: EvaluationMode] }>()
const { t } = useI18n()
</script>
