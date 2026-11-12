<template>
  <div class="space-y-3" data-testid="benchmark-group-target">
    <label class="block space-y-1 text-sm">
      <span>{{ t('modelBenchmark.group') }}</span>
      <select :value="modelValue.group_id || ''" name="benchmark_group_id" required class="input w-full" :disabled="loadingGroups || disabled" @change="selectGroup(Number(($event.target as HTMLSelectElement).value))">
        <option value="" disabled>{{ t('modelBenchmark.selectGroup') }}</option>
        <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
      </select>
    </label>
    <div class="grid gap-4 sm:grid-cols-2">
      <label class="block space-y-1 text-sm">
        <span>{{ t('modelBenchmark.model') }}</span>
        <select :value="modelValue.model" name="benchmark_group_model" required class="input w-full" :disabled="loadingModels || !modelValue.group_id || disabled" @change="update({ model: ($event.target as HTMLSelectElement).value })">
          <option value="" disabled>{{ t('modelBenchmark.selectGroupModel') }}</option>
          <option v-for="model in models" :key="model" :value="model">{{ model }}</option>
        </select>
      </label>
      <label class="block space-y-1 text-sm">
        <span>{{ t('modelBenchmark.effort') }}</span>
        <select :value="modelValue.effort" name="benchmark_group_effort" class="input w-full" :disabled="disabled" @change="update({ effort: ($event.target as HTMLSelectElement).value as Effort })">
          <option v-for="effort in efforts" :key="effort" :value="effort">{{ t(`modelBenchmark.efforts.${effort}`) }}</option>
        </select>
      </label>
    </div>
    <p v-if="loadingGroups || loadingModels" role="status" class="text-xs text-gray-500">{{ t('modelBenchmark.loading') }}</p>
    <p v-else-if="error" role="alert" class="text-sm text-red-600">{{ error }} <button type="button" class="underline" @click="loadGroups">{{ t('modelBenchmark.retry') }}</button></p>
    <p v-else-if="!groups.length" class="text-sm text-amber-700">{{ t('modelBenchmark.noGroups') }}</p>
    <p v-else-if="modelValue.group_id && !models.length" class="text-sm text-amber-700">{{ t('modelBenchmark.noGroupModels') }}</p>
    <p class="text-xs leading-5 text-gray-500">{{ t('modelBenchmark.groupTestHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { benchmarks, type BenchmarkGroup, type Effort, type ScheduleTarget } from '@/api/admin/model-benchmarks'

const props = defineProps<{ modelValue: ScheduleTarget; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: ScheduleTarget]; ready: [value: boolean] }>()
const { t } = useI18n()
const groups = ref<BenchmarkGroup[]>([])
const models = ref<string[]>([])
const loadingGroups = ref(false)
const loadingModels = ref(false)
const error = ref('')
const efforts: Effort[] = ['low', 'medium', 'high', 'xhigh']
const controller = new AbortController()
let modelRequest: AbortController | undefined
let generation = 0
function update(value: Partial<ScheduleTarget>) { emit('update:modelValue', { ...props.modelValue, ...value }) }
function selectGroup(group_id: number) {
  emit('ready', false)
  update({ group_id, model: '' })
}
async function loadGroups() {
  loadingGroups.value = true
  error.value = ''
  try {
    const result = await benchmarks.groups(controller.signal)
    if (controller.signal.aborted) return
    groups.value = result.items
    if (props.modelValue.group_id && !groups.value.some(g => g.id === props.modelValue.group_id)) update({ group_id: 0, model: '' })
    await loadModels(props.modelValue.group_id)
  } catch {
    if (!controller.signal.aborted) { error.value = t('modelBenchmark.groupLoadError'); emit('ready', false) }
  } finally { loadingGroups.value = false }
}
async function loadModels(groupID: number) {
  const current = ++generation
  modelRequest?.abort()
  models.value = []
  error.value = ''
  emit('ready', false)
  loadingModels.value = false
  if (!groupID || !groups.value.some(g => g.id === groupID)) return
  modelRequest = new AbortController()
  loadingModels.value = true
  try {
    const result = await benchmarks.groupModels(groupID, modelRequest.signal)
    if (current !== generation || controller.signal.aborted) return
    models.value = result.models
    if (!models.value.includes(props.modelValue.model)) update({ model: '' })
  } catch {
    if (current === generation && !controller.signal.aborted) { error.value = t('modelBenchmark.groupLoadError'); update({ model: '' }) }
  } finally { if (current === generation) loadingModels.value = false }
}
watch(() => props.modelValue.group_id, id => { void loadModels(id) })
watch(() => [props.modelValue.model, props.modelValue.group_id, loadingGroups.value, loadingModels.value, models.value, error.value], () => {
  emit('ready', !loadingGroups.value && !loadingModels.value && !error.value && groups.value.some(g => g.id === props.modelValue.group_id) && models.value.includes(props.modelValue.model))
})
onMounted(loadGroups)
onBeforeUnmount(() => { ++generation; controller.abort(); modelRequest?.abort() })
</script>
