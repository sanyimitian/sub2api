<template>
  <form class="space-y-4 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900" @submit.prevent="save">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="text-base font-semibold">{{ t('modelIdentification.sourceSettings') }}</h2>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="$emit('close')">{{ t('modelIdentification.cancel') }}</button>
    </div>
    <p class="max-w-prose text-sm text-gray-600 dark:text-gray-300">{{ t('modelIdentification.sourceHint') }}</p>
    <label class="block space-y-2 text-sm">
      <span>{{ t('modelIdentification.sourceURL') }}</span>
      <input v-model.trim="sourceURL" name="source_url" type="url" class="input" :disabled="loading || saving || loadFailed" placeholder="https://example.com/api/v1/model-identification/groups" maxlength="2048" />
    </label>
    <p class="text-xs text-gray-600 dark:text-gray-300">{{ t('modelIdentification.sourceEffect') }}</p>
    <label class="block space-y-2 text-sm">
      <span>{{ t('modelIdentification.shareURL') }}</span>
      <input :value="shareURL" class="input font-mono text-xs" readonly @focus="selectURL" />
    </label>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <button type="submit" class="btn btn-primary" :disabled="loading || saving || loadFailed">{{ t(saving ? 'modelIdentification.saving' : 'modelIdentification.save') }}</button>
  </form>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { modelIdentificationAPI } from '@/api/modelIdentification'
import { buildApiUrl } from '@/api/url'
import { extractApiErrorMessage } from '@/utils/apiError'
const { t } = useI18n()
const emit = defineEmits<{ saved: []; close: [] }>()
const sourceURL = ref('')
const savedSourceURL = ref('')
const loading = ref(true)
const loadFailed = ref(false)
const saving = ref(false)
const error = ref('')
const controller = new AbortController()
let disposed = false
const shareURL = computed(() => savedSourceURL.value
  ? savedSourceURL.value + '/groups'
  : new URL(buildApiUrl('/model-identification/groups'), window.location.origin).href)
function selectURL(event: FocusEvent) { (event.target as HTMLInputElement).select() }
onMounted(async () => {
  try {
    const source = await modelIdentificationAPI.source(controller.signal)
    if (!disposed) { sourceURL.value = source.source_url; savedSourceURL.value = source.source_url }
  } catch (err) {
    if (!disposed) { loadFailed.value = true; error.value = extractApiErrorMessage(err, t('modelIdentification.loadFailed')) }
  } finally { if (!disposed) loading.value = false }
})
onBeforeUnmount(() => { disposed = true; controller.abort() })
async function save() {
  if (loading.value || saving.value || loadFailed.value) return
  saving.value = true
  error.value = ''
  try {
    await modelIdentificationAPI.saveSource(sourceURL.value)
    if (!disposed) emit('saved')
  } catch (err) {
    if (!disposed) error.value = extractApiErrorMessage(err, t('modelIdentification.actionFailed'))
  } finally { if (!disposed) saving.value = false }
}
</script>
