<template>
  <AppLayout>
    <div class="-m-4 md:-m-6 lg:-m-8">
      <PublicTransitView :admin-preview="true" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import PublicTransitView from '@/views/public/PublicTransitView.vue'
</script>
