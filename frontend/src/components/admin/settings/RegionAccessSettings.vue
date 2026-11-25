<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getRegionAccessSettings, updateRegionAccessSettings } from '@/api/admin/regionAccess'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import RegionAccessBlocked from '@/components/common/RegionAccessBlocked.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpBlocked, isStepUpCancelled, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'

const { t } = useI18n()
const stepUp = useStepUp()
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const savedEnabled = ref(false)
const draftEnabled = ref(false)
const error = ref('')
const notice = ref('')
const preview = ref(false)
const previewRetried = ref(false)
const dirty = computed(() => loaded.value && savedEnabled.value !== draftEnabled.value)
const stateDescription = computed(() => dirty.value
  ? (draftEnabled.value ? t('regionAccess.pendingEnabled') : t('regionAccess.pendingDisabled'))
  : (savedEnabled.value ? t('regionAccess.savedEnabled') : t('regionAccess.savedDisabled')))

watch(draftEnabled, () => { notice.value = '' })
watch(preview, () => { previewRetried.value = false })

async function load() {
  loading.value = true
  loaded.value = false
  error.value = ''
  try {
    const settings = await getRegionAccessSettings()
    savedEnabled.value = settings.enabled
    draftEnabled.value = settings.enabled
    loaded.value = true
  } catch {
    error.value = t('regionAccess.loadFailed')
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!dirty.value || saving.value || loading.value) return
  saving.value = true
  error.value = ''
  notice.value = ''
  const enabled = draftEnabled.value
  try {
    const settings = await stepUp.run(() => updateRegionAccessSettings({ enabled }))
    savedEnabled.value = settings.enabled
    draftEnabled.value = settings.enabled
    notice.value = t('regionAccess.saved')
  } catch (cause) {
    if (isStepUpBlocked(cause)) {
      error.value = stepUpBlockReason(cause) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
        ? t('stepUp.adminApiKeyForbidden')
        : t('stepUp.notEnabled')
    } else if (!isStepUpCancelled(cause)) {
      error.value = t('regionAccess.saveFailed')
    }
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <section class="card" :aria-label="t('regionAccess.title')" :aria-busy="loading || saving">
    <header class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('regionAccess.title') }}</h2>
      <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('regionAccess.description') }}</p>
    </header>
    <div class="space-y-5 p-6">
      <p v-if="loading" role="status" class="text-sm text-gray-600 dark:text-gray-400">{{ t('regionAccess.loading') }}</p>
      <div v-if="error" class="flex flex-wrap items-center gap-3">
        <p role="alert" class="text-sm text-red-700 dark:text-red-300">{{ error }}</p>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || saving" @click="load">{{ t('regionAccess.reload') }}</button>
      </div>
      <div v-if="loaded" class="space-y-5">
        <div class="flex items-center justify-between gap-5">
          <div>
            <label for="region-access-enabled" class="font-medium text-gray-900 dark:text-white">{{ t('regionAccess.enabledLabel') }}</label>
            <p id="region-access-enabled-hint" class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('regionAccess.enabledHint') }}</p>
          </div>
          <Toggle id="region-access-enabled" v-model="draftEnabled" :disabled="saving" :aria-label="t('regionAccess.enabledLabel')" aria-describedby="region-access-enabled-hint" />
        </div>
        <dl class="space-y-4 border-t border-gray-100 pt-5 text-sm dark:border-dark-700">
          <div class="grid grid-cols-[5rem_minmax(0,1fr)] gap-4"><dt class="text-gray-600 dark:text-gray-400">{{ t('regionAccess.scopeLabel') }}</dt><dd>{{ t('regionAccess.scope') }}</dd></div>
          <div class="grid grid-cols-[5rem_minmax(0,1fr)] gap-4"><dt class="text-gray-600 dark:text-gray-400">{{ t('regionAccess.coverageLabel') }}</dt><dd>{{ t('regionAccess.coverage') }}</dd></div>
          <div class="grid grid-cols-[5rem_minmax(0,1fr)] gap-4"><dt class="text-gray-600 dark:text-gray-400">{{ t('regionAccess.adminLabel') }}</dt><dd>{{ t('regionAccess.adminHint') }}</dd></div>
          <div class="grid grid-cols-[5rem_minmax(0,1fr)] gap-4"><dt class="text-gray-600 dark:text-gray-400">{{ t('regionAccess.unknownLabel') }}</dt><dd>{{ t('regionAccess.unknownHint') }}</dd></div>
        </dl>
        <p class="flex items-start gap-2 text-sm text-primary-700 dark:text-primary-300"><Icon name="shield" size="sm" class="mt-0.5 flex-shrink-0" aria-hidden="true" />{{ stateDescription }}</p>
      </div>
      <p class="text-xs leading-6 text-gray-600 dark:text-gray-400">{{ t('regionAccess.dataSource') }}<a href="https://db-ip.com" target="_blank" rel="noopener noreferrer" class="text-primary-700 underline underline-offset-2 dark:text-primary-300">{{ t('regionAccess.attribution') }}</a> · {{ t('regionAccess.dataVersion') }}</p>
      <p v-if="notice" role="status" class="text-sm text-green-700 dark:text-green-300">{{ notice }}</p>
    </div>
    <footer class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 px-6 py-4 dark:border-dark-700">
      <button type="button" class="btn btn-secondary btn-sm" data-test="region-preview" @click="preview = true"><Icon name="eye" size="sm" class="mr-1.5" aria-hidden="true" />{{ t('regionAccess.preview') }}</button>
      <div class="flex flex-wrap items-center gap-3">
        <span v-if="dirty" class="text-xs text-amber-700 dark:text-amber-300">{{ t('regionAccess.unsaved') }}</span>
        <button type="button" class="btn btn-primary btn-sm" data-test="region-save" :disabled="!dirty || saving || loading" @click="save">{{ saving ? t('regionAccess.saving') : t('regionAccess.save') }}</button>
      </div>
    </footer>
    <BaseDialog :show="preview" :title="t('regionAccess.previewTitle')" width="normal" @close="preview = false">
      <p class="mb-6 text-xs text-gray-600 dark:text-gray-400">{{ t('regionAccess.previewHint') }}</p>
      <RegionAccessBlocked preview @retry="previewRetried = true" />
      <p v-if="previewRetried" role="status" class="mt-3 text-center text-xs text-gray-600 dark:text-gray-400">{{ t('regionAccess.previewRetry') }}</p>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>
