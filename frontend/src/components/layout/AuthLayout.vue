<template>
  <div v-if="variant === 'synapse'" class="synapse-auth">
    <div class="synapse-auth-media" aria-hidden="true">
      <video :src="synapseVideo" poster="/auth-scene.jpg" autoplay muted loop playsinline preload="metadata" />
      <div class="synapse-auth-media-shade" />
    </div>
    <div class="synapse-auth-panel">
      <router-link to="/" class="synapse-auth-brand" aria-label="CodeBot home">
        <img :src="siteLogo || '/logo.svg'" alt="" />
        <span>CodeBot</span>
      </router-link>
      <main class="synapse-auth-main">
        <div class="synapse-auth-form"><slot /></div>
        <div class="synapse-auth-switch"><slot name="footer" /></div>
      </main>
      <p class="synapse-auth-copyright">&copy; {{ currentYear }} CodeBot. All rights reserved.</p>
    </div>
  </div>
  <div v-else class="relative flex min-h-screen items-center justify-center overflow-hidden p-4">
    <!-- Background -->
    <div
      class="absolute inset-0 bg-gradient-to-br from-gray-50 via-primary-50/30 to-gray-100 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950"
    ></div>

    <!-- Decorative Elements -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <!-- Gradient Orbs -->
      <div
        class="absolute -right-40 -top-40 h-80 w-80 rounded-full bg-primary-400/20 blur-3xl"
      ></div>
      <div
        class="absolute -bottom-40 -left-40 h-80 w-80 rounded-full bg-primary-500/15 blur-3xl"
      ></div>
      <div
        class="absolute left-1/2 top-1/2 h-96 w-96 -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary-300/10 blur-3xl"
      ></div>

      <!-- Grid Pattern -->
      <div
        class="absolute inset-0 bg-[linear-gradient(rgba(86,166,240,0.045)_1px,transparent_1px),linear-gradient(90deg,rgba(86,166,240,0.045)_1px,transparent_1px)] bg-[size:64px_64px]"
      ></div>
    </div>

    <!-- Content Container -->
    <div class="relative z-10 w-full max-w-md">
      <!-- Logo/Brand -->
      <div class="mb-8 text-center">
        <!-- Custom Logo or Default Logo -->
        <template v-if="settingsLoaded">
          <div
            class="mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl shadow-lg shadow-primary-500/30"
          >
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <h1 class="text-gradient mb-2 text-3xl font-bold">
            {{ siteName }}
          </h1>
          <p class="text-sm text-gray-500 dark:text-dark-400">
            {{ siteSubtitle }}
          </p>
        </template>
      </div>

      <!-- Card Container -->
      <div class="card-glass rounded-2xl p-8 shadow-glass">
        <slot />
      </div>

      <!-- Footer Links -->
      <div class="mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <!-- Copyright -->
      <div class="mt-8 text-center text-xs text-gray-400 dark:text-dark-500">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'

withDefaults(defineProps<{ variant?: 'default' | 'synapse' }>(), { variant: 'default' })

const synapseVideo = 'https://d8j0ntlcm91z4.cloudfront.net/user_38xzZboKViGWJOttwIXH07lWA1P/hf_20260622_080203_fd7f4f85-3a86-4837-8192-85e7bfe68e75.mp4'
const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Codebot')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'Subscription to API Conversion Platform')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.synapse-auth { display: grid; grid-template-columns: minmax(0, 1fr) minmax(460px, 1fr); min-height: 100vh; min-height: 100dvh; background: #0c0b0f; color: #f8f7fa; color-scheme: dark; font-family: Inter, system-ui, sans-serif; }
.synapse-auth-media { position: sticky; top: 0; height: 100vh; height: 100dvh; min-height: 480px; overflow: hidden; background: #08080a url('/auth-scene.jpg') center / cover no-repeat; }
.synapse-auth-media video { display: block; width: 100%; height: 100%; object-fit: cover; }
.synapse-auth-media-shade { position: absolute; inset: 0; background: linear-gradient(90deg, transparent 55%, rgb(12 11 15 / .35)); pointer-events: none; }
.synapse-auth-panel { display: flex; min-width: 0; min-height: 100dvh; flex-direction: column; align-items: center; padding: 34px clamp(28px, 5vw, 88px) 26px; border-left: 1px solid rgb(255 255 255 / .08); }
.synapse-auth-brand { display: inline-flex; width: 100%; max-width: 480px; align-items: center; gap: 10px; color: #fff; font-size: 20px; font-weight: 700; text-decoration: none; }
.synapse-auth-brand img { width: 28px; height: 28px; border-radius: 6px; object-fit: contain; }
.synapse-auth-main { display: flex; width: 100%; max-width: 480px; flex: 1; flex-direction: column; justify-content: center; padding: 48px 0 28px; }
.synapse-auth-form { width: 100%; }
.synapse-auth-switch { min-height: 24px; margin-top: 28px; color: rgb(255 255 255 / .55); text-align: center; }
.synapse-auth-copyright { width: 100%; max-width: 480px; margin: 0; color: rgb(255 255 255 / .35); font-size: 12px; }
.synapse-auth-form :deep(h2) { color: #fff; font-size: 30px; letter-spacing: 0; }
.synapse-auth-form :deep(h2 + p) { color: rgb(255 255 255 / .55); }
.synapse-auth-form :deep(.input-label) { color: rgb(255 255 255 / .78); }
.synapse-auth-form :deep(.input-hint) { color: rgb(255 255 255 / .48); }
.synapse-auth-form :deep(.input) { min-height: 48px; border: 1px solid rgb(255 255 255 / .18); background: #19191f; color: #fff; box-shadow: none; }
.synapse-auth-form :deep(.input::placeholder) { color: rgb(255 255 255 / .42); }
.synapse-auth-form :deep(.input:focus) { border-color: #ed93ad; outline: 2px solid rgb(237 147 173 / .15); outline-offset: 0; }
.synapse-auth-form :deep(.input-error), .synapse-auth-form :deep(.input-error:focus) { border-color: #f87171; outline-color: rgb(248 113 113 / .18); }
.synapse-auth-form :deep(.input:disabled) { background: #121217; }
.synapse-auth-form :deep(.btn-primary) { min-height: 48px; border: 0; border-radius: 8px; background: #fff; color: #111; box-shadow: none; }
.synapse-auth-form :deep(.btn-primary:hover) { background: #e8e6ea; box-shadow: none; }
.synapse-auth-form :deep(.btn-primary:focus-visible) { outline: 2px solid #fff; outline-offset: 3px; }
.synapse-auth-form :deep(.btn-secondary) { min-height: 46px; border-color: rgb(255 255 255 / .18); background: rgb(255 255 255 / .06); color: #fff; box-shadow: none; }
.synapse-auth-form :deep(.btn-secondary:hover) { background: rgb(255 255 255 / .12); }
.synapse-auth-form :deep(.btn-secondary svg[fill="currentColor"]) { color: #fff; }
.synapse-auth-form :deep(a), .synapse-auth-switch :deep(a) { color: #f2a0b9; }
.synapse-auth-form :deep(a:hover), .synapse-auth-switch :deep(a:hover) { color: #ffd0de; }
.synapse-auth-switch :deep(p) { color: rgb(255 255 255 / .56); }
@media (max-width: 900px) {
  .synapse-auth { grid-template-columns: minmax(0, 1fr) minmax(360px, 1fr); }
  .synapse-auth-panel { padding-right: 30px; padding-left: 30px; }
}
@media (max-width: 700px) {
  .synapse-auth { display: flex; flex-direction: column; }
  .synapse-auth-media { position: relative; width: 100%; height: clamp(180px, 30vh, 280px); min-height: 0; }
  .synapse-auth-media-shade { background: linear-gradient(to bottom, transparent 35%, #0c0b0f); }
  .synapse-auth-panel { min-height: calc(100dvh - 180px); margin-top: -28px; padding: 0 24px 28px; border: 0; position: relative; }
  .synapse-auth-main { flex: 0 0 auto; padding: 40px 0; }
  .synapse-auth-copyright { margin-top: auto; }
}
@media (prefers-reduced-motion: reduce) { .synapse-auth-media video { display: none; } }
.text-gradient {
  @apply bg-gradient-to-r from-primary-600 to-primary-500 bg-clip-text text-transparent;
}
</style>
