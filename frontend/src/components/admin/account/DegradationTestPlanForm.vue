<template>
  <form class="space-y-4" @submit.prevent="submit">
    <div class="grid gap-4 sm:grid-cols-2">
      <div>
        <label :for="`${formId}-model`" class="mb-1 block text-sm font-medium">{{ t('admin.degradationTests.model') }}</label>
        <input :id="`${formId}-model`" v-model="form.model_id" :list="`${formId}-models`" class="input w-full" required maxlength="100" :placeholder="t('admin.degradationTests.modelPlaceholder')">
        <datalist :id="`${formId}-models`">
          <option v-for="model in modelOptions" :key="String(model.value)" :value="String(model.value)">{{ model.label }}</option>
        </datalist>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.degradationTests.modelHelp') }}</p>
      </div>
      <div>
        <label :for="`${formId}-effort`" class="mb-1 block text-sm font-medium">{{ t('admin.degradationTests.reasoningEffort') }}</label>
        <select :id="`${formId}-effort`" v-model="form.reasoning_effort" class="input w-full" data-testid="reasoning-effort">
          <option v-for="effort in reasoningEfforts" :key="effort" :value="effort">{{ t(`admin.degradationTests.effort.${effort || 'default'}`) }}</option>
        </select>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.degradationTests.reasoningEffortHelp') }}</p>
      </div>
      <div>
        <label :for="`${formId}-mode`" class="mb-1 block text-sm font-medium">{{ t('admin.degradationTests.scheduleType') }}</label>
        <select :id="`${formId}-mode`" v-model="scheduleMode" class="input w-full">
          <option value="interval">{{ t('admin.degradationTests.interval') }}</option>
          <option value="cron">{{ t('admin.degradationTests.cronExpression') }}</option>
        </select>
      </div>
      <div v-if="scheduleMode === 'interval'" class="sm:col-span-2">
        <label :for="`${formId}-interval`" class="mb-1 block text-sm font-medium">{{ t('admin.degradationTests.runEvery') }}</label>
        <div class="flex gap-2">
          <input :id="`${formId}-interval`" v-model.number="intervalValue" type="number" min="1" step="1" :max="intervalMaximum" class="input min-w-0 flex-1" required>
          <select v-model="intervalUnit" class="input w-32" :aria-label="t('admin.degradationTests.intervalUnit')">
            <option value="m">{{ t('admin.degradationTests.minutes') }}</option>
            <option value="h">{{ t('admin.degradationTests.hours') }}</option>
            <option value="d">{{ t('admin.degradationTests.days') }}</option>
          </select>
        </div>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.degradationTests.intervalHelp') }}</p>
      </div>
      <div v-else class="sm:col-span-2">
        <label :for="`${formId}-cron`" class="mb-1 block text-sm font-medium">{{ t('admin.degradationTests.cronExpression') }}</label>
        <input :id="`${formId}-cron`" v-model="form.cron_expression" class="input w-full font-mono" placeholder="*/30 * * * *" required maxlength="100">
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.degradationTests.cronHelp') }}</p>
      </div>
      <div class="sm:col-span-2">
        <div class="mb-1 flex items-center justify-between gap-2">
          <label :for="`${formId}-prompt`" class="text-sm font-medium">{{ t('admin.degradationTests.prompt') }}</label>
          <button type="button" class="text-xs text-primary-600 hover:underline" @click="form.prompt = DEFAULT_DEGRADATION_TEST_PROMPT">{{ t('admin.degradationTests.resetPrompt') }}</button>
        </div>
        <textarea :id="`${formId}-prompt`" v-model="form.prompt" rows="5" required maxlength="20000" class="input w-full resize-y" :placeholder="DEFAULT_DEGRADATION_TEST_PROMPT" />
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.degradationTests.promptHelp') }}</p>
      </div>
      <div>
        <label :for="`${formId}-retention`" class="mb-1 block text-sm font-medium">{{ t('admin.degradationTests.maxResults') }}</label>
        <input :id="`${formId}-retention`" class="input w-full bg-gray-50 dark:bg-dark-800" value="24" type="number" readonly>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.degradationTests.retentionHelp') }}</p>
      </div>
      <div class="space-y-3 self-center text-sm">
        <label class="flex items-center gap-2"><Toggle v-model="form.enabled" />{{ t('admin.degradationTests.enabled') }}</label>
      </div>
    </div>
    <p v-if="validationError" role="alert" class="text-sm text-red-600">{{ validationError }}</p>
    <div class="flex justify-end gap-2">
      <button type="button" class="btn btn-secondary" :disabled="busy" @click="emit('cancel')">{{ t('common.cancel') }}</button>
      <button type="submit" class="btn btn-primary" :disabled="busy || !form.model_id.trim() || !form.prompt.trim()">{{ busy ? t('common.loading') : t('common.save') }}</button>
    </div>
  </form>
</template>

<script setup lang="ts">
import { computed, getCurrentInstance, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import type { SelectOption } from '@/components/common/Select.vue'
import type { DegradationTestPlan, DegradationTestReasoningEffort } from '@/types'
import { DEFAULT_DEGRADATION_TEST_PROMPT } from '@/utils/degradationTestPreview'

export interface DegradationTestPlanFormValue {
  model_id: string
  reasoning_effort: DegradationTestReasoningEffort
  cron_expression: string
  prompt: string
  max_results: number
  enabled: boolean
}

const props = defineProps<{ initial?: DegradationTestPlan; modelOptions: SelectOption[]; busy?: boolean }>()
const emit = defineEmits<{ submit: [value: DegradationTestPlanFormValue]; cancel: [] }>()
const { t } = useI18n()
const reasoningEfforts: DegradationTestReasoningEffort[] = ['', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh']
const formId = `degradation-test-${getCurrentInstance()?.uid ?? 'form'}`
const initialInterval = props.initial?.cron_expression.match(/^@every\s+(\d+)(m|h)$/)
const scheduleMode = ref<'interval' | 'cron'>(!props.initial || initialInterval ? 'interval' : 'cron')
const intervalValue = ref(initialInterval ? Number(initialInterval[1]) : 30)
const intervalUnit = ref(initialInterval?.[2] ?? 'm')
const intervalMaximum = computed(() => intervalUnit.value === 'd' ? 30 : intervalUnit.value === 'h' ? 720 : 43200)
const validationError = ref('')
const form = reactive<DegradationTestPlanFormValue>({
  model_id: props.initial?.model_id ?? '',
  reasoning_effort: props.initial?.reasoning_effort ?? '',
  cron_expression: props.initial?.cron_expression ?? '*/30 * * * *',
  prompt: props.initial ? (props.initial.prompt ?? '') : DEFAULT_DEGRADATION_TEST_PROMPT,
  max_results: 24,
  enabled: props.initial?.enabled ?? true
})

function submit() {
  validationError.value = ''
  let expression = form.cron_expression.trim()
  if (scheduleMode.value === 'interval') {
    const value = Number(intervalValue.value)
    if (!Number.isInteger(value) || value < 1 || value > intervalMaximum.value) {
      validationError.value = t('admin.degradationTests.invalidInterval')
      return
    }
    expression = `@every ${intervalUnit.value === 'd' ? value * 24 : value}${intervalUnit.value === 'd' ? 'h' : intervalUnit.value}`
  }
  if (!form.model_id.trim() || !form.prompt.trim() || !expression) {
    validationError.value = t('admin.degradationTests.invalidPlan')
    return
  }
  emit('submit', { ...form, max_results: 24, model_id: form.model_id.trim(), cron_expression: expression })
}
</script>
