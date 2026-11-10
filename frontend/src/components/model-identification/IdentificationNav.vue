<template>
  <div>
    <button
      type="button"
      class="sidebar-link mb-1 w-full"
      :class="{ 'sidebar-link-active': active, 'sidebar-link-collapsed': collapsed }"
      :aria-label="t('modelIdentification.title')"
      :aria-expanded="expanded && !collapsed"
      :title="collapsed ? t('modelIdentification.title') : undefined"
      @click="toggle"
    >
      <Icon name="shield" class="h-5 w-5 shrink-0" />
      <template v-if="!collapsed"
        ><span class="min-w-0 flex-1 text-left">{{ t('modelIdentification.title') }}</span
        ><Icon
          name="chevronDown"
          class="h-4 w-4 transition-transform motion-reduce:transition-none"
          :class="{ 'rotate-180': expanded }"
      /></template>
    </button>
    <div
      v-if="expanded && !collapsed"
      class="mb-1 ml-4 border-l border-gray-200 pl-2 dark:border-dark-600"
    >
      <RouterLink
        to="/model-identification/degradation"
        class="sidebar-link mb-1 text-sm"
        :class="{ 'sidebar-link-active': route.path === '/model-identification/degradation' }"
        @click="emit('navigate', '/model-identification/degradation')"
        >{{ t('modelIdentification.degradation') }}</RouterLink
      >
      <RouterLink
        to="/model-identification/dilution"
        class="sidebar-link mb-1 text-sm"
        :class="{ 'sidebar-link-active': route.path === '/model-identification/dilution' }"
        @click="emit('navigate', '/model-identification/dilution')"
      >
        {{ t('modelIdentification.dilution') }}
        <span class="ml-auto text-xs">{{ t('modelIdentification.developing') }}</span>
      </RouterLink>
    </div>
  </div>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
const props = defineProps<{ collapsed: boolean }>()
const emit = defineEmits<{ expand: []; navigate: [path: string] }>()
const route = useRoute()
const { t } = useI18n()
const active = computed(() => route.path.startsWith('/model-identification'))
const override = ref<boolean | null>(null)
const expanded = computed(() => override.value ?? active.value)
function toggle() {
  if (props.collapsed) {
    emit('expand')
    override.value = true
  } else {
    override.value = !expanded.value
  }
}
</script>
