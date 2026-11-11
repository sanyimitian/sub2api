<template>
  <div class="space-y-3">
    <label class="block space-y-1 text-sm">
      <span>{{ t('modelBenchmark.scheduleName') }}</span>
      <input :value="modelValue.name" name="schedule_name" maxlength="100" required class="input w-full" @input="update('name', ($event.target as HTMLInputElement).value)" />
    </label>
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="block space-y-1 text-sm">
        <span>{{ t('modelBenchmark.frequency') }}</span>
        <select v-model="preset" name="schedule_preset" class="input w-full" @change="applyPreset">
          <option value="interval">{{ t('modelBenchmark.interval') }}</option>
          <option value="0 9 * * *">{{ t('modelBenchmark.daily') }}</option>
          <option value="0 * * * *">{{ t('modelBenchmark.hourly') }}</option>
          <option value="0 9 * * 1">{{ t('modelBenchmark.weekly') }}</option>
          <option value="custom">{{ t('modelBenchmark.customCron') }}</option>
        </select>
      </label>
      <div v-if="preset === 'interval'" class="space-y-1 text-sm">
        <label :for="intervalId">{{ t('modelBenchmark.runEvery') }}</label>
        <div class="flex gap-2">
          <input :id="intervalId" v-model.number="intervalValue" name="schedule_interval" type="number" min="1" :max="intervalUnit === 'h' ? 720 : 43200" step="1" required class="input min-w-0 flex-1" @input="applyInterval" />
          <select v-model="intervalUnit" name="schedule_interval_unit" :aria-label="t('modelBenchmark.intervalUnit')" class="input w-24" @change="applyInterval">
            <option value="m">{{ t('modelBenchmark.minutes') }}</option>
            <option value="h">{{ t('modelBenchmark.hours') }}</option>
          </select>
        </div>
      </div>
      <label v-else class="block space-y-1 text-sm">
        <span>{{ t('modelBenchmark.cron') }}</span>
        <input :value="modelValue.cron_expression" name="cron_expression" required maxlength="100" class="input w-full font-mono" placeholder="0 9 * * *" @input="update('cron_expression', ($event.target as HTMLInputElement).value)" />
      </label>
    </div>
    <p class="text-xs leading-5 text-gray-500">{{ preset === 'interval' ? t('modelBenchmark.intervalHint') : t('modelBenchmark.cronHint', { timezone }) }}</p>
    <label class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="modelValue.enabled" @change="update('enabled', ($event.target as HTMLInputElement).checked)" />{{ t('modelBenchmark.scheduleEnabled') }}</label>
    <label class="flex items-center gap-2 text-sm"><input name="public_results" type="checkbox" :checked="modelValue.public_results" @change="update('public_results', ($event.target as HTMLInputElement).checked)" />{{ t('modelBenchmark.publicResults') }}</label>
    <p class="text-xs text-gray-500">{{ t('modelBenchmark.publicResultsHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { getCurrentInstance, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ScheduleSettings } from '@/api/admin/model-benchmarks'
const props = defineProps<{ modelValue: ScheduleSettings; timezone: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: ScheduleSettings] }>()
const { t } = useI18n()
const preset = ref('0 9 * * *')
const intervalId = `benchmark-interval-${getCurrentInstance()?.uid}`
const intervalValue = ref<number | string>(30)
const intervalUnit = ref('m')
let lastEmitted = ''
const presets = ['0 9 * * *', '0 * * * *', '0 9 * * 1']
function update<K extends keyof ScheduleSettings>(key: K, value: ScheduleSettings[K]) {
  if (key === 'cron_expression') lastEmitted = String(value)
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}
function applyInterval() { update('cron_expression', `@every ${intervalValue.value}${intervalUnit.value}`) }
function applyPreset() {
  if (preset.value === 'interval') applyInterval()
  else if (preset.value !== 'custom') update('cron_expression', preset.value)
  else if (props.modelValue.cron_expression.startsWith('@every')) update('cron_expression', '0 9 * * *')
}
watch(() => props.modelValue.cron_expression, value => {
  if (value === lastEmitted) return
  const match = value.match(/^@every\s+(\d+)(m|h)$/)
  if (match) {
    preset.value = 'interval'
    intervalValue.value = Number(match[1])
    intervalUnit.value = match[2]!
  } else preset.value = presets.includes(value) ? value : 'custom'
}, { immediate: true })
</script>
