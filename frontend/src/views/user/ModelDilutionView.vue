<template>
  <component
    :is="auth.user ? AppLayout : 'main'"
    :class="!auth.user ? 'min-h-screen bg-gray-50 p-4 md:p-8 dark:bg-dark-950' : ''"
  >
    <div class="min-w-0 space-y-6 text-gray-900 dark:text-gray-100">
      <header class="flex flex-wrap items-center justify-between gap-4">
        <h1 class="text-2xl font-bold">{{ t('modelIdentification.title') }}</h1>
        <RouterLink v-if="!auth.user" to="/login" class="btn btn-secondary">
          {{ t('modelIdentification.login') }}
        </RouterLink>
      </header>
      <div class="border-b border-gray-200 pb-3 dark:border-dark-700">
        <IdentificationTabs active="dilution" />
      </div>
      <section
        class="max-w-3xl space-y-4 rounded-xl border border-gray-200 bg-white p-5 text-sm leading-7 text-gray-700 md:p-6 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-200"
        :aria-label="t('modelIdentification.dilution')"
      >
        <p>{{ t('modelIdentification.dilutionBackground') }}</p>
        <p>{{ t('modelIdentification.dilutionMethod') }}</p>
        <p class="text-gray-600 dark:text-gray-300">{{ t('modelIdentification.dilutionCaveat') }}</p>
      </section>
    </div>
  </component>
</template>

<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import IdentificationTabs from '@/components/model-identification/IdentificationTabs.vue'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const auth = useAuthStore()
</script>
