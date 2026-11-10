<template>
  <AppLayout>
    <div class="min-w-0 space-y-6 text-gray-900 dark:text-gray-100">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div class="space-y-2">
          <h1 class="text-2xl font-bold">{{ t('modelIdentification.title') }}</h1>
          <p class="max-w-prose text-sm text-gray-600 dark:text-gray-300">
            {{ t('modelIdentification.description') }}
          </p>
        </div>
        <div class="flex flex-wrap gap-2">
          <button
            class="btn btn-secondary"
            :aria-expanded="preferencesOpen"
            @click="preferencesOpen = !preferencesOpen"
          >
            <Icon name="filter" class="h-4 w-4" />{{ t('modelIdentification.showGroups') }}</button
          ><button v-if="auth.isAdmin" class="btn btn-primary" @click="openForm(null)">
            {{ t('modelIdentification.configure') }}
          </button>
        </div>
      </header>
      <div
        class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 pb-3 dark:border-dark-700"
      >
        <div class="flex flex-wrap items-center gap-2">
          <span
            class="rounded-lg bg-primary-50 px-3 py-2 text-sm font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
            >{{ t('modelIdentification.degradation') }}</span
          ><button
            disabled
            class="cursor-not-allowed px-3 py-2 text-sm text-gray-500 dark:text-gray-400"
          >
            {{ t('modelIdentification.dilution') }}
            <span class="ml-1 text-xs">{{ t('modelIdentification.developing') }}</span>
          </button>
        </div>
        <span class="text-xs text-gray-600 dark:text-gray-300">{{
          t('modelIdentification.shared')
        }}</span>
      </div>
      <IdentificationPlanForm
        v-if="formOpen && auth.isAdmin"
        :key="editing?.plan.id ?? 'new'"
        ref="planForm"
        :plan="editing"
        @close="formOpen = false"
        @saved="saved"
      />
      <section
        v-if="preferencesOpen"
        class="space-y-4 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900"
        :aria-label="t('modelIdentification.showGroups')"
      >
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-sm font-semibold">
            {{ t('modelIdentification.selectedCount', { count: visibleGroups.length }) }}
          </h2>
          <div class="flex gap-3">
            <button
              class="text-sm text-primary-700 dark:text-primary-300"
              @click="setSelection(null)"
            >
              {{ t('modelIdentification.selectAll') }}</button
            ><button
              class="text-sm text-primary-700 dark:text-primary-300"
              @click="setSelection([])"
            >
              {{ t('modelIdentification.selectNone') }}
            </button>
          </div>
        </div>
        <div class="flex flex-wrap gap-x-6 gap-y-3">
          <label
            v-for="group in groups"
            :key="group.group_id"
            class="flex min-w-0 items-center gap-2 text-sm"
            ><input
              type="checkbox"
              class="accent-primary-600"
              :checked="isSelected(group.group_id)"
              @change="toggleGroup(group.group_id)"
            /><span class="break-words">{{ group.name }}</span></label
          >
        </div>
        <p class="text-xs text-gray-600 dark:text-gray-300">
          {{ t('modelIdentification.preferences') }}
        </p>
        <p v-if="preferenceError" role="status" class="text-xs text-amber-700 dark:text-amber-300">
          {{ t('modelIdentification.preferenceError') }}
        </p>
      </section>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div
          class="flex max-w-full gap-1 overflow-x-auto rounded-lg bg-gray-100 p-1 dark:bg-dark-800"
          :aria-label="t('modelIdentification.showGroups')"
        >
          <button
            class="shrink-0 rounded-md px-3 py-1.5 text-xs"
            :class="activeGroup === null ? 'bg-white font-semibold dark:bg-dark-600' : ''"
            :aria-pressed="activeGroup === null"
            @click="activeGroup = null"
          >
            {{ t('modelIdentification.allGroups') }}</button
          ><button
            v-for="group in visibleGroups"
            :key="group.group_id"
            class="max-w-64 shrink-0 truncate rounded-md px-3 py-1.5 text-xs"
            :class="activeGroup === group.group_id ? 'bg-white font-semibold dark:bg-dark-600' : ''"
            :title="group.name"
            :aria-pressed="activeGroup === group.group_id"
            @click="activeGroup = group.group_id"
          >
            {{ group.name }}
          </button>
        </div>
        <div class="flex flex-wrap items-center gap-3 text-xs text-gray-600 dark:text-gray-300">
          <span>{{ t('modelIdentification.retention') }}</span
          ><button
            class="btn btn-secondary !p-2"
            :disabled="loading"
            :aria-label="t('modelIdentification.refresh')"
            :title="t('modelIdentification.refresh')"
            @click="refresh(true)"
          >
            <Icon name="refresh" class="h-4 w-4" />
          </button>
        </div>
      </div>
      <div
        v-if="error"
        role="alert"
        class="flex items-center gap-3 rounded-lg bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300"
      >
        <span>{{ error }}</span
        ><button class="underline" @click="refresh(true)">
          {{ t('modelIdentification.retry') }}
        </button>
      </div>
      <div
        v-if="initialLoading"
        :aria-label="t('modelIdentification.loading')"
        role="status"
        class="space-y-5"
      >
        <div
          v-for="i in 2"
          :key="i"
          class="h-72 rounded-xl bg-gray-200 motion-safe:animate-pulse dark:bg-dark-800"
        />
      </div>
      <section
        v-else-if="!groups.length && !error"
        class="space-y-3 rounded-xl border border-dashed border-gray-300 px-6 py-16 text-center dark:border-dark-600"
      >
        <h2 class="text-lg font-semibold">{{ t('modelIdentification.emptyTitle') }}</h2>
        <p class="mx-auto max-w-prose text-sm text-gray-600 dark:text-gray-300">
          {{ t(auth.isAdmin ? 'modelIdentification.emptyAdmin' : 'modelIdentification.emptyUser') }}
        </p>
        <button v-if="auth.isAdmin" class="btn btn-primary" @click="openForm(null)">
          {{ t('modelIdentification.add') }}
        </button>
      </section>
      <section v-else-if="!visibleGroups.length && !error" class="py-12 text-center">
        <h2 class="font-semibold">{{ t('modelIdentification.hiddenTitle') }}</h2>
        <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">
          {{ t('modelIdentification.hiddenDescription') }}
        </p>
      </section>
      <section
        v-for="group in displayedGroups"
        :key="group.group_id"
        class="min-w-0 space-y-4 border-b border-gray-200 pb-7 last:border-0 dark:border-dark-700"
        :aria-labelledby="'identification-group-' + group.group_id"
      >
        <header class="flex flex-wrap items-center justify-between gap-3">
          <div class="min-w-0 space-y-1">
            <h2
              :id="'identification-group-' + group.group_id"
              class="break-words text-base font-semibold"
            >
              {{ group.name }}
            </h2>
            <div class="flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-600 dark:text-gray-300">
              <span>{{ group.platform }}</span
              ><span data-testid="group-reasoning-effort">{{ t('modelIdentification.effort') }}: {{
                formatIdentificationEffort(group.plan.reasoning_effort, t)
              }}</span
              ><span>{{ t('modelIdentification.count', { count: group.result_count }) }}</span
              ><span>{{
                group.plan.enabled
                  ? t('modelIdentification.every', {
                      minutes: intervalMinutes(group.plan.cron_expression)
                    })
                  : t('modelIdentification.paused')
              }}</span
              ><span
                v-if="running(group)"
                class="font-medium text-primary-700 dark:text-primary-300"
                role="status"
                >{{ t('modelIdentification.running') }}</span
              ><time v-else-if="group.plan.last_run_at" :datetime="group.plan.last_run_at">{{
                t('modelIdentification.last', { time: formatDateTime(group.plan.last_run_at) })
              }}</time>
            </div>
          </div>
          <div v-if="auth.isAdmin" class="flex flex-wrap gap-2">
            <button
              class="btn btn-secondary !px-3 !py-1.5 text-xs"
              :disabled="busy.has(group.plan.id) || running(group)"
              @click="run(group)"
            >
              {{ t('modelIdentification.run') }}</button
            ><button class="btn btn-secondary !px-3 !py-1.5 text-xs" @click="openForm(group)">
              {{ t('modelIdentification.edit') }}</button
            ><button
              class="btn btn-secondary !px-3 !py-1.5 text-xs"
              :disabled="busy.has(group.plan.id) || running(group)"
              @click="deleting = group"
            >
              {{ t('modelIdentification.delete') }}
            </button>
          </div>
        </header>
        <p
          v-if="group.plan.enabled && group.plan.next_run_at && auth.isAdmin"
          class="text-xs text-gray-600 dark:text-gray-300"
        >
          {{ t('modelIdentification.next', { time: formatDateTime(group.plan.next_run_at) }) }}
        </p>
        <div
          v-if="resultErrors[group.plan.id]"
          role="alert"
          class="flex gap-3 text-sm text-red-600 dark:text-red-400"
        >
          <span>{{ resultErrors[group.plan.id] }}</span
          ><button class="underline" @click="loadResults([group], true)">
            {{ t('modelIdentification.retry') }}
          </button>
        </div>
        <IdentificationGallery
          v-if="results[group.plan.id]?.length"
          :results="results[group.plan.id]"
          :group-name="group.name"
        />
        <div
          v-else-if="fetching.has(group.plan.id)"
          role="status"
          :aria-label="t('modelIdentification.loading')"
          class="h-64 rounded-xl bg-gray-200 motion-safe:animate-pulse dark:bg-dark-800"
        />
        <div
          v-else-if="!resultErrors[group.plan.id]"
          class="rounded-xl bg-gray-100 px-6 py-12 text-center dark:bg-dark-900"
        >
          <p class="text-sm font-medium">{{ t('modelIdentification.noResults') }}</p>
          <p class="mt-2 text-xs text-gray-600 dark:text-gray-300">
            {{ t('modelIdentification.waiting') }}
          </p>
        </div>
      </section>
      <p class="text-xs text-gray-600 dark:text-gray-300">{{ t('modelIdentification.caveat') }}</p>
    </div>
    <BaseDialog
      :show="deleting !== null"
      :title="t('modelIdentification.delete')"
      width="narrow"
      @close="deleting = null"
      ><p class="text-sm">{{ t('modelIdentification.deleteConfirm') }}</p>
      <p v-if="deleting" class="mt-3 break-words font-medium">{{ deleting.name }}</p>
      <template #footer
        ><button class="btn btn-secondary" @click="deleting = null">
          {{ t('modelIdentification.cancel') }}</button
        ><button
          class="btn btn-primary"
          :disabled="!!deleting && busy.has(deleting.plan.id)"
          @click="remove"
        >
          {{ t('modelIdentification.confirmDelete') }}
        </button></template
      ></BaseDialog
    >
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import IdentificationGallery from '@/components/model-identification/IdentificationGallery.vue'
import IdentificationPlanForm from '@/components/model-identification/IdentificationPlanForm.vue'
import { modelIdentificationAPI, type IdentificationGroup } from '@/api/modelIdentification'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'
import { formatIdentificationEffort } from '@/utils/modelIdentification'
import type { DegradationTestResult } from '@/types'

const { t } = useI18n()
const auth = useAuthStore()
const app = useAppStore()
const groups = ref<IdentificationGroup[]>([])
const results = ref<Record<number, DegradationTestResult[]>>({})
const resultErrors = ref<Record<number, string>>({})
const revisions = new Map<number, string>()
const fetching = ref(new Set<number>())
const busy = ref(new Set<number>())
const loading = ref(false)
const initialLoading = ref(true)
const error = ref('')
const preferencesOpen = ref(false)
const selection = ref<number[] | null>(null)
const preferenceError = ref(false)
const activeGroup = ref<number | null>(null)
const formOpen = ref(false)
const editing = ref<IdentificationGroup | null>(null)
const deleting = ref<IdentificationGroup | null>(null)
const planForm = ref<InstanceType<typeof IdentificationPlanForm> | null>(null)
const now = ref(Date.now())
const controller = new AbortController()
let disposed = false
let timer: ReturnType<typeof setInterval> | undefined
const storageKey = computed(() => 'model-identification:targets:v2:' + auth.user?.id)
watch(
  storageKey,
  () => {
    selection.value = null
    try {
      const raw = localStorage.getItem(storageKey.value)
      if (raw) {
        const value: unknown = JSON.parse(raw)
        if (Array.isArray(value) && value.every((id) => Number.isSafeInteger(id) && id > 0))
          selection.value = value
      }
    } catch {
      preferenceError.value = true
    }
  },
  { immediate: true }
)
const visibleGroups = computed(() => groups.value.filter((group) => isSelected(group.group_id)))
const displayedGroups = computed(() =>
  visibleGroups.value.filter(
    (group) => activeGroup.value === null || group.group_id === activeGroup.value
  )
)
function isSelected(id: number) {
  return selection.value === null || selection.value.includes(id)
}
function setSelection(value: number[] | null) {
  selection.value = value
  try {
    localStorage.setItem(storageKey.value, JSON.stringify(value))
    preferenceError.value = false
  } catch {
    preferenceError.value = true
  }
}
function toggleGroup(id: number) {
  const current = selection.value ?? groups.value.map((group) => group.group_id)
  setSelection(isSelected(id) ? current.filter((value) => value !== id) : [...current, id])
}
watch(visibleGroups, (value) => {
  if (activeGroup.value !== null && !value.some((group) => group.group_id === activeGroup.value))
    activeGroup.value = null
})
watch(displayedGroups, (value) => {
  void loadResults(value)
})
function intervalMinutes(value: string) {
  return Number(value.match(/^@every ([0-9]+)m$/)?.[1] ?? 0)
}
function running(group: IdentificationGroup) {
  return !!group.plan.running_until && Date.parse(group.plan.running_until) > now.value
}
async function loadResults(targets: IdentificationGroup[], force = false) {
  const queue = targets.filter(
    (group) =>
      !fetching.value.has(group.plan.id) &&
      (force || revisions.get(group.plan.id) !== group.latest_result_id + ':' + group.result_count)
  )
  for (const group of queue) fetching.value.add(group.plan.id)
  await Promise.all(
    Array.from({ length: Math.min(3, queue.length) }, async () => {
      while (queue.length && !disposed) {
        const group = queue.shift()!
        const id = group.plan.id
        try {
          const value = await modelIdentificationAPI.results(id, controller.signal)
          if (disposed) return
          results.value[id] = value
          revisions.set(id, group.latest_result_id + ':' + group.result_count)
          delete resultErrors.value[id]
        } catch (err) {
          if (!disposed)
            resultErrors.value[id] = extractApiErrorMessage(
              err,
              t('modelIdentification.loadFailed')
            )
        } finally {
          fetching.value.delete(id)
        }
      }
    })
  )
}
async function refresh(force = false) {
  if (loading.value || disposed) return
  loading.value = true
  try {
    const value = await modelIdentificationAPI.groups(controller.signal)
    if (disposed) return
    groups.value = value
    now.value = Date.now()
    error.value = ''
    const ids = new Set(value.map((group) => group.plan.id))
    for (const key of Object.keys(results.value).map(Number))
      if (!ids.has(key)) {
        delete results.value[key]
        revisions.delete(key)
      }
    await loadResults(displayedGroups.value, force)
  } catch (err) {
    if (!disposed) error.value = extractApiErrorMessage(err, t('modelIdentification.loadFailed'))
  } finally {
    loading.value = false
    initialLoading.value = false
  }
}
async function openForm(group: IdentificationGroup | null) {
  editing.value = group
  formOpen.value = true
  await nextTick()
  planForm.value?.$el.scrollIntoView?.({ block: 'nearest' })
}
async function saved() {
  formOpen.value = false
  app.showSuccess(t('modelIdentification.saved'))
  await refresh()
}
async function run(group: IdentificationGroup) {
  if (busy.value.has(group.plan.id)) return
  busy.value.add(group.plan.id)
  try {
    const plan = await modelIdentificationAPI.run(group.plan.id)
    group.plan = { ...plan, group_id: group.group_id }
    now.value = Date.now()
    app.showSuccess(t('modelIdentification.started'))
  } catch (err) {
    app.showError(extractApiErrorMessage(err, t('modelIdentification.actionFailed')))
  } finally {
    busy.value.delete(group.plan.id)
  }
}
async function remove() {
  const group = deleting.value
  if (!group || busy.value.has(group.plan.id)) return
  busy.value.add(group.plan.id)
  try {
    await modelIdentificationAPI.delete(group.plan.id)
    deleting.value = null
    app.showSuccess(t('modelIdentification.deleted'))
    await refresh()
  } catch (err) {
    app.showError(extractApiErrorMessage(err, t('modelIdentification.actionFailed')))
  } finally {
    busy.value.delete(group.plan.id)
  }
}
function onVisible() {
  if (!document.hidden) void refresh()
}
onMounted(() => {
  void refresh()
  timer = setInterval(() => {
    if (!document.hidden) void refresh()
  }, 15000)
  document.addEventListener('visibilitychange', onVisible)
})
onBeforeUnmount(() => {
  disposed = true
  controller.abort()
  if (timer) clearInterval(timer)
  document.removeEventListener('visibilitychange', onVisible)
})
</script>
