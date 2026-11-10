<template>
  <form
    class="space-y-5 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900"
    @submit.prevent="save"
  >
    <div class="flex items-center justify-between gap-4">
      <h2 class="text-base font-semibold">
        {{ t(plan ? 'modelIdentification.edit' : 'modelIdentification.add') }}
      </h2>
      <button type="button" class="btn btn-secondary" :disabled="saving || loading" @click="$emit('close')">
        {{ t('modelIdentification.cancel') }}
      </button>
    </div>
    <p class="max-w-prose text-sm text-gray-600 dark:text-gray-300">
      {{ t('modelIdentification.adminHint') }}
    </p>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <label class="space-y-2 text-sm">
        <span>{{ t('modelIdentification.group') }}</span>
        <select v-model.number="form.display_group_id" name="display_group_id" required class="input" :disabled="saving || loading">
          <option :value="0" disabled>{{ t('modelIdentification.chooseGroup') }}</option>
          <option v-if="form.display_group_id && !availableGroups.some(group => group.id === form.display_group_id)" :value="form.display_group_id" disabled>{{ t('modelIdentification.unavailableGroup') }}</option>
          <option v-for="group in availableGroups" :key="group.id" :value="group.id">{{ group.name }}</option>
        </select>
        <span class="block text-xs text-gray-600 dark:text-gray-300">{{ t('modelIdentification.groupHint') }}</span>
        <span v-if="!loading && !availableGroups.length" class="block text-xs text-amber-700 dark:text-amber-300">{{ t('modelIdentification.noGroups') }}</span>
      </label>
      <label class="space-y-2 text-sm sm:col-span-2">
        <span>{{ t('modelIdentification.endpoint') }}</span>
        <input v-model.trim="form.endpoint" name="endpoint" type="url" required maxlength="2048" placeholder="https://api.example.com" class="input" :disabled="saving || loading" autocomplete="off" />
        <span class="block text-xs text-gray-600 dark:text-gray-300">{{ t('modelIdentification.endpointHint') }}</span>
      </label>
      <label class="space-y-2 text-sm sm:col-span-2">
        <span>{{ t('modelIdentification.apiKey') }}</span>
        <div class="flex gap-2">
          <input v-model.trim="form.api_key" name="api_key" :type="showKey ? 'text' : 'password'" :required="!plan || !hasStoredKey || endpointChanged" maxlength="8192" class="input min-w-0 flex-1" :disabled="saving || loading" autocomplete="new-password" :placeholder="plan ? t('modelIdentification.keepKey') : ''" />
          <button type="button" class="btn btn-secondary" :aria-pressed="showKey" :disabled="loading || saving" @click="toggleKey">{{ t(showKey ? 'modelIdentification.hideKey' : 'modelIdentification.showKey') }}</button>
        </div>
        <span class="block text-xs text-gray-600 dark:text-gray-300">{{ t(endpointChanged ? 'modelIdentification.endpointChanged' : 'modelIdentification.keyHint') }}</span>
      </label>
      <label class="space-y-2 text-sm">
        <span>{{ t('modelIdentification.protocol') }}</span>
        <select v-model="form.protocol" name="protocol" class="input" :disabled="saving || loading">
          <option value="chat_completions">{{ t('modelIdentification.protocolChat') }}</option>
          <option value="responses">{{ t('modelIdentification.protocolResponses') }}</option>
          <option value="anthropic">{{ t('modelIdentification.protocolAnthropic') }}</option>
          <option value="gemini">{{ t('modelIdentification.protocolGemini') }}</option>
        </select>
      </label>
      <label class="space-y-2 text-sm sm:col-span-2">
        <span>{{ t('modelIdentification.model') }}</span>
        <input v-model.trim="form.model_id" name="model_id" required maxlength="100" class="input" :disabled="saving || loading" />
        <span class="block text-xs text-gray-600 dark:text-gray-300">{{ t('modelIdentification.modelHint') }}</span>
      </label>
      <label class="space-y-2 text-sm"
        ><span>{{ t('modelIdentification.interval') }}</span
        ><input
          v-model.number="form.interval_minutes"
          :aria-label="t('modelIdentification.interval')"
          type="number"
          required
          min="1"
          max="43200"
          step="1"
          class="input"
          :disabled="saving || loading"
      /></label>
      <label class="space-y-2 text-sm"
        ><span>{{ t('modelIdentification.timeout') }}</span
        ><input
          v-model.number="form.timeout_seconds"
          :aria-label="t('modelIdentification.timeout')"
          type="number"
          required
          min="60"
          max="1800"
          step="1"
          class="input"
          :disabled="saving || loading"
      /></label>
      <label class="space-y-2 text-sm"
        ><span>{{ t('modelIdentification.effort') }}</span
        ><select v-model="form.reasoning_effort" name="reasoning_effort" class="input" :disabled="saving || loading">
          <option v-for="option in efforts" :key="option.value" :value="option.value" :disabled="!supportsEffort && !!option.value">
            {{ t(option.label) }}
          </option>
        </select><span v-if="!supportsEffort" class="block text-xs text-gray-600 dark:text-gray-300">{{ t('modelIdentification.effortHint') }}</span></label
      >
    </div>
    <details class="text-sm text-gray-600 dark:text-gray-300">
      <summary>{{ t('modelIdentification.prompt') }}</summary>
      <p class="mt-2">{{ t('modelIdentification.promptText') }}</p>
    </details>
    <div class="flex flex-wrap items-center justify-between gap-4">
      <label class="flex items-center gap-2 text-sm"
        ><input
          v-model="form.enabled"
          type="checkbox"
          class="accent-primary-600"
          :disabled="saving || loading"
        />{{ t('modelIdentification.scheduled') }}</label
      ><button
        type="submit"
        class="btn btn-primary"
        :disabled="saving || loading || loadFailed || !availableGroups.some(group => group.id === form.display_group_id)"
      >
        {{ t(saving ? 'modelIdentification.saving' : 'modelIdentification.save') }}
      </button>
    </div>
  </form>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { modelIdentificationAPI, type IdentificationGroup } from '@/api/modelIdentification'
import { getAllIncludingInactive } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import { identificationEffortOptions as efforts } from '@/utils/modelIdentification'

const props = defineProps<{ plan: IdentificationGroup | null }>()
const emit = defineEmits<{ saved: []; close: [] }>()
const { t } = useI18n()
const loading = ref(true)
const availableGroups = ref<AdminGroup[]>([])
const loadFailed = ref(false)
const saving = ref(false)
const error = ref('')
const showKey = ref(false)
const storedKey = ref('')
const originalEndpoint = ref('')
const hasStoredKey = computed(() => !!storedKey.value)
const controller = new AbortController()
let disposed = false
const p = props.plan?.plan
const interval = p?.cron_expression.match(/^@every ([0-9]+)m$/)?.[1]
const form = reactive({
  display_group_id: props.plan?.display_group_id ?? 0,
  endpoint: '',
  api_key: '',
  protocol: props.plan?.platform ?? 'chat_completions',
  model_id: p?.model_id ?? '',
  interval_minutes: Number(interval ?? 30),
  timeout_seconds: p?.timeout_seconds ?? 600,
  reasoning_effort: p?.reasoning_effort ?? '',
  enabled: p?.enabled ?? true
})
const endpointChanged = computed(() => !!props.plan && form.endpoint.replace(/\/+$/, '') !== originalEndpoint.value)
const supportsEffort = computed(() => ['chat_completions', 'responses'].includes(form.protocol))
function toggleKey() {
  if (!showKey.value && !form.api_key && !endpointChanged.value) form.api_key = storedKey.value
  showKey.value = !showKey.value
}
onMounted(async () => {
  try {
    const [groups, config] = await Promise.all([
      getAllIncludingInactive(),
      p ? modelIdentificationAPI.config(p.id, controller.signal) : Promise.resolve(null)
    ])
    if (disposed) return
    availableGroups.value = groups
    if (config) {
      form.display_group_id = config.display_group_id ?? 0
      form.endpoint = config.endpoint
      form.protocol = config.protocol
      originalEndpoint.value = config.endpoint
      storedKey.value = config.api_key
    }
  } catch (err) {
    if (!disposed) {
      loadFailed.value = true
      error.value = extractApiErrorMessage(err, t('modelIdentification.loadFailed'))
    }
  } finally {
    if (!disposed) loading.value = false
  }
})
onBeforeUnmount(() => {
  disposed = true
  controller.abort()
  storedKey.value = ''
  form.api_key = ''
})
async function save() {
  if (saving.value || loading.value || loadFailed.value) return
  error.value = ''
  if (!availableGroups.value.some(group => group.id === form.display_group_id)) {
    error.value = t('modelIdentification.chooseGroup')
    return
  }
  if (!supportsEffort.value && form.reasoning_effort) {
    error.value = t('modelIdentification.effortHint')
    return
  }
  saving.value = true
  try {
    await modelIdentificationAPI.save(p?.id ?? null, { ...form })
    if (!disposed) emit('saved')
  } catch (err) {
    if (!disposed) error.value = extractApiErrorMessage(err, t('modelIdentification.actionFailed'))
  } finally {
    if (!disposed) saving.value = false
  }
}
</script>
