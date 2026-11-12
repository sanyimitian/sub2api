<template>
  <section class="space-y-6 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900" :aria-label="t('modelBenchmark.sourceSettings')">
    <form class="max-w-2xl space-y-4" @submit.prevent="save">
      <h2 class="text-lg font-semibold">{{ t('modelBenchmark.sourceSettings') }}</h2>
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.sourceHint') }}</p>
      <label class="block space-y-2 text-sm">
        <span>{{ t('modelBenchmark.mainSiteURL') }}</span>
        <input v-model.trim="sourceURL" name="source_url" type="url" maxlength="2048" class="input w-full" placeholder="https://example.com" :disabled="loading || saving || !source" />
      </label>
      <label v-if="sourceURL" class="block space-y-2 text-sm">
        <span>{{ t('modelBenchmark.siteToken') }}</span>
        <input v-model.trim="token" name="site_token" type="password" maxlength="97" autocomplete="new-password" spellcheck="false" class="input w-full" :disabled="loading || saving" :required="!source?.has_token || sourceURL !== source.source_url" />
        <span class="block text-xs text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.siteTokenHint') }}</span>
      </label>
      <button type="submit" class="btn btn-primary" :disabled="loading || saving || !source">{{ t(saving ? 'modelIdentification.saving' : 'common.save') }}</button>
    </form>
    <p v-if="error" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ error }} <button v-if="!source" type="button" class="underline" :disabled="loading" @click="load">{{ t('modelBenchmark.retry') }}</button></p>
    <p v-if="loading" role="status" class="text-sm">{{ t('modelBenchmark.loading') }}</p>
    <section v-if="source && !source.source_url" class="space-y-4 border-t border-gray-200 pt-5 dark:border-dark-700" :aria-label="t('modelBenchmark.connectedSites')">
      <h2 class="text-lg font-semibold">{{ t('modelBenchmark.connectedSites') }}</h2>
      <p class="max-w-2xl text-sm text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.connectedSitesHint') }}</p>
      <label class="block max-w-2xl space-y-2 text-sm"><span>{{ t('modelBenchmark.mainSiteAddress') }}</span><input :value="shareURL" class="input w-full" readonly @focus="selectText" /></label>
      <form class="flex max-w-2xl flex-wrap items-end gap-3" @submit.prevent="create">
        <label class="min-w-0 flex-1 space-y-2 text-sm"><span>{{ t('modelBenchmark.siteName') }}</span><input v-model.trim="siteName" name="site_name" required maxlength="100" class="input w-full" :disabled="saving" /></label>
        <button type="submit" class="btn btn-secondary" :disabled="saving || !siteName || !!issuedToken">{{ t('modelBenchmark.createSiteToken') }}</button>
      </form>
      <div v-if="issuedToken" class="max-w-2xl space-y-3 rounded-lg bg-amber-50 p-4 dark:bg-amber-950/30" role="status">
        <label class="block space-y-2 text-sm"><span class="text-amber-900 dark:text-amber-200">{{ t('modelBenchmark.tokenIssuedOnce') }}</span><textarea :value="issuedToken" readonly rows="3" class="input w-full break-all font-mono text-sm" :aria-label="t('modelBenchmark.siteToken')" @focus="selectText" /></label>
        <button type="button" class="btn btn-secondary" @click="issuedToken = ''">{{ t('modelBenchmark.tokenSaved') }}</button>
      </div>
      <p v-if="!sites.length && !loading" class="text-sm text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.noConnectedSites') }}</p>
      <ul v-else class="divide-y divide-gray-100 dark:divide-dark-800">
        <li v-for="site in sites" :key="site.id" class="flex flex-wrap items-center justify-between gap-3 py-3">
          <div class="min-w-0"><p class="break-words text-sm font-medium">{{ site.name }}</p><p class="break-all text-xs text-gray-600 dark:text-gray-300">{{ site.id }}</p></div>
          <button type="button" class="text-sm text-red-700 underline dark:text-red-300" :disabled="saving" @click="revoking = site">{{ t('modelBenchmark.revokeSite') }}</button>
        </li>
      </ul>
    </section>
    <ConfirmDialog :show="!!revoking" :title="t('modelBenchmark.revokeSite')" :message="t('modelBenchmark.revokeSiteHint', { name: revoking?.name })" danger @cancel="revoking = null" @confirm="revoke" />
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { benchmarks, type BenchmarkSource, type BenchmarkSite } from '@/api/admin/model-benchmarks'
import { buildApiUrl } from '@/api/url'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

const { t } = useI18n()
const app = useAppStore()
const emit = defineEmits<{ saved: [source: BenchmarkSource] }>()
const source = ref<BenchmarkSource | null>(null)
const sourceURL = ref('')
const token = ref('')
const sites = ref<BenchmarkSite[]>([])
const siteName = ref('')
const issuedToken = ref('')
const revoking = ref<BenchmarkSite | null>(null)
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const controller = new AbortController()
const shareURL = computed(() => new URL(buildApiUrl('/model-benchmarks/federation'), window.location.origin).href)
function selectText(event: FocusEvent) { (event.target as HTMLInputElement | HTMLTextAreaElement).select() }
async function load() {
  loading.value = true; error.value = ''
  try {
    source.value = await benchmarks.source(controller.signal)
    sourceURL.value = source.value.source_url
    sites.value = source.value.source_url ? [] : (await benchmarks.sites(controller.signal)).items
  } catch (err) { if (!controller.signal.aborted) error.value = extractApiErrorMessage(err, t('modelBenchmark.loadError')) }
  finally { loading.value = false }
}
async function save() {
  if (saving.value || !source.value) return
  saving.value = true; error.value = ''
  try {
    source.value = await benchmarks.saveSource(sourceURL.value, token.value, controller.signal)
    sourceURL.value = source.value.source_url; token.value = ''; issuedToken.value = ''
    emit('saved', source.value)
    app.showSuccess(t('modelBenchmark.sourceSaved'))
    sites.value = source.value.source_url ? [] : (await benchmarks.sites(controller.signal)).items
  } catch (err) { if (!controller.signal.aborted) error.value = extractApiErrorMessage(err, t('modelBenchmark.actionError')) }
  finally { saving.value = false }
}
async function create() {
  if (saving.value || issuedToken.value) return
  saving.value = true; error.value = ''
  try {
    const result = await benchmarks.createSite(siteName.value, controller.signal)
    issuedToken.value = result.token; sites.value.push(result.site); siteName.value = ''
  } catch (err) { if (!controller.signal.aborted) error.value = extractApiErrorMessage(err, t('modelBenchmark.actionError')) }
  finally { saving.value = false }
}
async function revoke() {
  if (!revoking.value || saving.value) return
  const id = revoking.value.id
  saving.value = true; error.value = ''
  try {
    await benchmarks.revokeSite(id, controller.signal)
    sites.value = sites.value.filter(site => site.id !== id)
    if (issuedToken.value.startsWith(id + '.')) issuedToken.value = ''
    revoking.value = null
  } catch (err) { if (!controller.signal.aborted) error.value = extractApiErrorMessage(err, t('modelBenchmark.actionError')) }
  finally { saving.value = false }
}
onMounted(load)
onBeforeUnmount(() => { controller.abort(); token.value = ''; issuedToken.value = '' })
</script>
