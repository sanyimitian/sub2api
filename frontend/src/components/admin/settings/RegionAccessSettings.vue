<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getRegionAccessSettings, updateRegionAccessSettings } from '@/api/admin/regionAccess'
import Toggle from '@/components/common/Toggle.vue'
import { availableLocales, type LocaleCode } from '@/i18n'
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
const savedSiteWide = ref(false)
const draftSiteWide = ref(false)
const savedLanguage = ref<LocaleCode>('zh')
const draftLanguage = ref<LocaleCode>('zh')
const error = ref('')
const notice = ref('')
const preview = ref(false)
const previewRetried = ref(false)
const regionDirty = computed(() => savedEnabled.value !== draftEnabled.value || savedSiteWide.value !== draftSiteWide.value)
const dirty = computed(() => loaded.value && (regionDirty.value || savedLanguage.value !== draftLanguage.value))
const stateDescription = computed(() => {
  if (regionDirty.value) {
    if (!draftEnabled.value) return t('regionAccess.pendingDisabled')
    return t(draftSiteWide.value ? 'regionAccess.pendingSiteWide' : 'regionAccess.pendingEnabled')
  }
  if (!savedEnabled.value) return t('regionAccess.savedDisabled')
  return t(savedSiteWide.value ? 'regionAccess.savedSiteWide' : 'regionAccess.savedEnabled')
})

watch([draftEnabled, draftSiteWide, draftLanguage], () => { notice.value = '' })
watch(preview, () => { previewRetried.value = false })

async function load() {
  loading.value = true
  loaded.value = false
  error.value = ''
  try {
    const settings = await getRegionAccessSettings()
    savedEnabled.value = settings.enabled
    draftEnabled.value = settings.enabled
    savedSiteWide.value = settings.site_wide
    draftSiteWide.value = settings.site_wide
    savedLanguage.value = settings.default_language
    draftLanguage.value = settings.default_language
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
  const site_wide = draftSiteWide.value
  const default_language = draftLanguage.value
  try {
    const settings = await stepUp.run(() => updateRegionAccessSettings({ enabled, site_wide, default_language }))
    savedEnabled.value = settings.enabled
    draftEnabled.value = settings.enabled
    savedSiteWide.value = settings.site_wide
    draftSiteWide.value = settings.site_wide
    savedLanguage.value = settings.default_language
    draftLanguage.value = settings.default_language
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
        <div class="flex items-start justify-between gap-5 border-t border-gray-100 pt-5 dark:border-dark-700">
          <div>
            <label for="region-access-site-wide" class="font-medium text-gray-900 dark:text-white">{{ t('regionAccess.siteWideLabel') }}</label>
            <p id="region-access-site-wide-hint" class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('regionAccess.siteWideHint') }}</p>
            <p v-if="draftEnabled && draftSiteWide" class="mt-2 text-sm text-amber-800 dark:text-amber-200">{{ t('regionAccess.siteWideAdminWarning') }}</p>
          </div>
          <Toggle id="region-access-site-wide" v-model="draftSiteWide" :disabled="saving || !draftEnabled" :aria-label="t('regionAccess.siteWideLabel')" aria-describedby="region-access-site-wide-hint" />
        </div>
        <div class="border-t border-gray-100 pt-5 dark:border-dark-700">
          <label for="region-access-default-language" class="font-medium text-gray-900 dark:text-white">{{ t('regionAccess.defaultLanguage') }}</label>
          <p id="region-access-default-language-hint" class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('regionAccess.defaultLanguageHint') }}</p>
          <select id="region-access-default-language" v-model="draftLanguage" class="input mt-3 w-full sm:max-w-xs" :disabled="saving" aria-describedby="region-access-default-language-hint">
            <option v-for="language in availableLocales" :key="language.code" :value="language.code">{{ language.name }}</option>
          </select>
        </div>
        <dl class="space-y-4 border-t border-gray-100 pt-5 text-sm dark:border-dark-700">
          <div class="grid grid-cols-[5rem_minmax(0,1fr)] gap-4"><dt class="text-gray-600 dark:text-gray-400">{{ t('regionAccess.scopeLabel') }}</dt><dd>{{ t('regionAccess.scope') }}</dd></div>
          <div class="grid grid-cols-[5rem_minmax(0,1fr)] gap-4"><dt class="text-gray-600 dark:text-gray-400">{{ t('regionAccess.coverageLabel') }}</dt><dd>{{ t(draftSiteWide ? 'regionAccess.siteWideCoverage' : 'regionAccess.coverage') }}</dd></div>
          <div class="grid grid-cols-[5rem_minmax(0,1fr)] gap-4"><dt class="text-gray-600 dark:text-gray-400">{{ t('regionAccess.adminLabel') }}</dt><dd>{{ t(draftSiteWide ? 'regionAccess.siteWideAdminHint' : 'regionAccess.adminHint') }}</dd></div>
          <div class="grid grid-cols-[5rem_minmax(0,1fr)] gap-4"><dt class="text-gray-600 dark:text-gray-400">{{ t('regionAccess.unknownLabel') }}</dt><dd>{{ t(draftSiteWide ? 'regionAccess.siteWideUnknownHint' : 'regionAccess.unknownHint') }}</dd></div>
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
      <RegionAccessBlocked preview :site-wide="draftSiteWide" @retry="previewRetried = true" />
      <p v-if="previewRetried" role="status" class="mt-3 text-center text-xs text-gray-600 dark:text-gray-400">{{ t('regionAccess.previewRetry') }}</p>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>
