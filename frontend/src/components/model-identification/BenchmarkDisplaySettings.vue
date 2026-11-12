<template>
  <div class="space-y-6">
    <section class="display-card space-y-3">
      <h2 class="text-lg font-semibold">{{ t('modelBenchmark.publicApi') }}</h2>
      <p class="text-sm text-gray-500">{{ t('modelBenchmark.publicApiHint') }}</p>
      <label class="block space-y-1 text-sm"><span>GET</span><input :value="publicUrl" readonly class="input w-full font-mono text-xs" aria-label="Public results URL" @focus="($event.target as HTMLInputElement).select()" /></label>
    </section>
    <section class="display-card space-y-4">
      <div class="flex flex-wrap justify-between gap-3"><h2 class="text-lg font-semibold">{{ t('modelBenchmark.displaySettings') }}</h2><button class="btn btn-primary" :disabled="busy" @click="openEditor()">{{ t('modelBenchmark.addDisplay') }}</button></div>
      <p class="text-sm text-gray-500">{{ t('modelBenchmark.displayHint') }}</p>
      <p v-if="loadError" role="alert" class="text-sm text-red-600">{{ loadError }} <button class="underline" @click="reload">{{ t('modelBenchmark.retry') }}</button></p>
      <p v-if="!items.length && !loading" class="py-6 text-center text-sm text-gray-500">{{ t('modelBenchmark.noDisplays') }}</p>
      <article v-for="item in items" :key="item.id" class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700" :data-display-config="item.id">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0"><h3 class="break-words font-semibold">{{ item.name }}</h3><p class="mt-1 text-xs text-gray-500">{{ t(item.source_type === 'local' ? 'modelBenchmark.localSource' : 'modelBenchmark.remoteSource') }} · {{ t(item.enabled ? 'modelBenchmark.scheduleEnabled' : 'modelBenchmark.schedulePaused') }}</p></div>
          <div class="flex flex-wrap gap-3 text-sm">
            <button class="text-primary-600" :disabled="busy" @click="openEditor(item)">{{ t('common.edit') }}</button>
            <button class="text-primary-600" :disabled="busy" @click="toggle(item)">{{ t(item.enabled ? 'modelBenchmark.pauseSchedule' : 'modelBenchmark.resumeSchedule') }}</button>
            <button v-if="item.source_type === 'remote'" class="text-primary-600" :disabled="busy || !item.enabled || item.fetching" @click="refresh(item)">{{ t(item.fetching ? 'modelBenchmark.fetchingSource' : 'modelBenchmark.refreshSource') }}</button>
            <button class="text-red-600" :disabled="busy" @click="deleting = item">{{ t('common.delete') }}</button>
          </div>
        </div>
        <template v-if="item.source_type === 'local'">
          <p class="text-sm">{{ schedules.find(s => s.id === item.schedule_id)?.name || t('modelBenchmark.sourceUnavailable') }}</p>
          <p class="text-xs text-gray-500">{{ t('modelBenchmark.resultPaginationHint') }}</p>
          <p v-if="!schedules.some(s => s.id === item.schedule_id && s.public_results)" class="text-xs text-amber-700">{{ t('modelBenchmark.sourceUnavailable') }}</p>
        </template>
        <template v-else>
          <p class="break-all text-xs text-gray-500">{{ item.remote_url }}</p>
          <p class="text-xs text-gray-500">{{ t('modelBenchmark.refreshEvery', { minutes: item.refresh_minutes }) }} · {{ t('modelBenchmark.lastFetch', { time: formatDate(item.last_success_at) }) }}</p>
          <p v-if="item.enabled" class="text-xs text-gray-500">{{ t('modelBenchmark.nextFetch', { time: formatDate(item.next_fetch_at) }) }}</p>
          <p v-if="item.last_error" role="status" class="text-xs text-amber-700">{{ item.last_error }} · {{ t('modelBenchmark.cachedResultsHint') }}</p>
        </template>
      </article>
    </section>
    <BaseDialog :show="editing" :title="t(editId ? 'modelBenchmark.editDisplay' : 'modelBenchmark.addDisplay')" @close="editing = false">
      <form class="space-y-4" @submit.prevent="save">
        <label class="block space-y-1 text-sm"><span>{{ t('modelBenchmark.displayName') }}</span><input v-model.trim="form.name" name="display_name" required maxlength="100" class="input w-full" /></label>
        <label class="block space-y-1 text-sm"><span>{{ t('modelBenchmark.dataSource') }}</span><select v-model="form.source_type" name="source_type" class="input w-full"><option value="local">{{ t('modelBenchmark.localSource') }}</option><option value="remote">{{ t('modelBenchmark.remoteSource') }}</option></select></label>
        <template v-if="form.source_type === 'local'">
          <label class="block space-y-1 text-sm"><span>{{ t('modelBenchmark.selectPublicSchedule') }}</span><select v-model="form.schedule_id" name="display_schedule" class="input w-full" required><option :value="null" disabled>{{ t('modelBenchmark.selectPublicSchedule') }}</option><option v-for="schedule in publicSchedules" :key="schedule.id" :value="schedule.id">{{ schedule.name }} · {{ schedule.model }}</option></select></label>
          <p v-if="!publicSchedules.length" class="text-xs text-amber-700">{{ t('modelBenchmark.noPublicSchedules') }}</p>
          <p class="text-xs text-gray-500">{{ t('modelBenchmark.resultPaginationHint') }}</p>
        </template>
        <template v-else>
          <label class="block space-y-1 text-sm"><span>{{ t('modelBenchmark.feedUrl') }}</span><input v-model.trim="form.remote_url" name="remote_url" type="url" required maxlength="2048" placeholder="https://example.com/api/v1/model-benchmarks/public-results" class="input w-full" /></label>
          <label class="block space-y-1 text-sm"><span>{{ t('modelBenchmark.refreshMinutes') }}</span><input v-model.number="form.refresh_minutes" name="refresh_minutes" type="number" min="1" max="10080" step="1" required class="input w-full" /></label>
          <p class="text-xs text-gray-500">{{ t('modelBenchmark.remoteRefreshHint') }}</p>
        </template>
        <label class="flex items-center gap-2 text-sm"><input v-model="form.enabled" type="checkbox" />{{ t('modelBenchmark.displayEnabled') }}</label>
        <button type="submit" class="btn btn-primary" :disabled="busy || !form.name || (form.source_type === 'local' && !form.schedule_id)">{{ t('common.save') }}</button>
      </form>
    </BaseDialog>
    <ConfirmDialog :show="!!deleting" :title="t('modelBenchmark.deleteDisplay')" :message="t('modelBenchmark.deleteDisplayHint')" danger @cancel="deleting = null" @confirm="remove" />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { benchmarks, type BenchmarkSchedule, type DisplayConfig, type DisplayInput } from '@/api/admin/model-benchmarks'
import { buildApiUrl } from '@/api/url'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const app = useAppStore()
const publicUrl = new URL(buildApiUrl('/model-benchmarks/public-results'), window.location.origin).href
const items = ref<DisplayConfig[]>([])
const schedules = ref<BenchmarkSchedule[]>([])
const publicSchedules = computed(() => schedules.value.filter(s => s.public_results))
const loading = ref(false)
const busy = ref(false)
const loadError = ref('')
const editing = ref(false)
const editId = ref('')
const deleting = ref<DisplayConfig | null>(null)
const defaults = (): DisplayInput => ({ name: '', source_type: 'local', schedule_id: null, remote_url: '', refresh_minutes: 60, enabled: true, max_results: 20 })
const form = reactive<DisplayInput>(defaults())
const controller = new AbortController()
let timer: ReturnType<typeof setTimeout> | undefined
let revision = 0
const formatDate = (value: string | null) => value ? new Date(value).toLocaleString() : '—'

async function reload() {
  const current = ++revision
  loading.value = true
  const [configs, plans] = await Promise.allSettled([benchmarks.displays(controller.signal), benchmarks.schedules(controller.signal)])
  if (controller.signal.aborted || current !== revision) return
  if (configs.status === 'fulfilled') items.value = configs.value.items
  if (plans.status === 'fulfilled') schedules.value = plans.value.items
  loadError.value = configs.status === 'rejected' || plans.status === 'rejected' ? t('modelBenchmark.loadError') : ''
  loading.value = false
}
function openEditor(item?: DisplayConfig) {
  editId.value = item?.id || ''
  Object.assign(form, item ? { name: item.name, source_type: item.source_type, schedule_id: item.schedule_id, remote_url: item.remote_url, refresh_minutes: item.refresh_minutes, enabled: item.enabled, max_results: item.max_results } : defaults())
  editing.value = true
}
async function action(work: () => Promise<unknown>) {
  if (busy.value) return
  busy.value = true
  ++revision
  try { await work() } catch (error) {
    const e = error as { response?: { data?: { message?: string } } }
    if (!controller.signal.aborted) app.showError(e.response?.data?.message || t('modelBenchmark.actionError'))
  } finally {
    if (!controller.signal.aborted) { await reload(); busy.value = false }
  }
}
async function save() {
  await action(async () => {
    const input = { ...form, schedule_id: form.source_type === 'local' ? form.schedule_id : null, remote_url: form.source_type === 'remote' ? form.remote_url : '' }
    if (editId.value) await benchmarks.updateDisplay(editId.value, input, controller.signal)
    else await benchmarks.createDisplay(input, controller.signal)
    editing.value = false
    app.showSuccess(t('modelBenchmark.displaySaved'))
  })
}
async function toggle(item: DisplayConfig) { await action(() => benchmarks.updateDisplay(item.id, { enabled: !item.enabled }, controller.signal)) }
async function refresh(item: DisplayConfig) { await action(async () => { await benchmarks.refreshDisplay(item.id, controller.signal); app.showSuccess(t('modelBenchmark.fetchQueued')) }) }
async function remove() {
  if (!deleting.value) return
  const id = deleting.value.id
  await action(async () => { await benchmarks.deleteDisplay(id, controller.signal); deleting.value = null })
}
async function poll() {
  if (!busy.value && !loading.value) await reload()
  if (!controller.signal.aborted) timer = setTimeout(poll, 5000)
}
onMounted(poll)
onBeforeUnmount(() => { controller.abort(); clearTimeout(timer); ++revision })
</script>

<style scoped>
.display-card { @apply min-w-0 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900; }
</style>
