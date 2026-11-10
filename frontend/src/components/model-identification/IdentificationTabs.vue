<template>
  <nav class="flex flex-wrap items-center gap-2" :aria-label="t('modelIdentification.title')">
    <RouterLink
      v-for="tab in tabs"
      :key="tab"
      :to="`/model-identification/${tab}`"
      class="rounded-lg px-3 py-2 text-sm"
      :class="active === tab
        ? 'bg-primary-50 font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
        : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-800'"
      :aria-current="active === tab ? 'page' : undefined"
    >
      {{ t(`modelIdentification.${tab}`) }}
      <span v-if="tab === 'dilution'" class="ml-1 text-xs">{{ t('modelIdentification.developing') }}</span>
    </RouterLink>
  </nav>
</template>

<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'

defineProps<{ active: 'degradation' | 'dilution' }>()
const tabs = ['degradation', 'dilution'] as const
const { t } = useI18n()
</script>
