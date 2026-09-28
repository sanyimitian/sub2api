<template>
  <div class="relative h-screen overflow-hidden text-white">
    <!-- 3D 全屏背景：固定铺满，沉在内容之下 -->
    <canvas ref="canvasRef" class="fixed inset-0 -z-10 h-full w-full" />

    <div
      v-if="webglError"
      class="absolute inset-0 z-20 flex items-center justify-center bg-black px-6 text-center text-sm text-white/80"
    >
      {{ t('home.hub.webglError') }}
    </div>

    <header class="pointer-events-none absolute inset-x-0 top-0 z-20">
      <nav class="pointer-events-auto mx-auto flex max-w-6xl items-center justify-between gap-4 px-5 py-4 sm:px-8">
        <router-link to="/home" class="flex min-w-0 items-center gap-3">
          <img
            v-if="siteLogo"
            :src="siteLogo"
            alt=""
            class="h-9 w-9 shrink-0 rounded-lg object-contain"
          />
          <span class="truncate text-sm font-medium tracking-wide text-white/90">{{ siteName }}</span>
        </router-link>
        <div class="flex shrink-0 items-center gap-2">
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-white/70 transition hover:bg-white/10 hover:text-white"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <div class="[&>div>button]:text-white/70 [&>div>button:hover]:bg-white/10 [&>div>button:hover]:text-white">
            <LocaleSwitcher />
          </div>
          <button
            type="button"
            class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-white/70 transition hover:bg-white/10 hover:text-white"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex h-9 shrink-0 items-center rounded-full bg-white px-4 text-sm font-medium text-black transition hover:bg-white/85"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <!-- 左右分栏：左文案垂直居中，右半留给月球 -->
    <main class="pointer-events-none relative z-10 grid h-full grid-cols-1 lg:grid-cols-2">
      <section
        class="flex h-full flex-col justify-center pl-[6vw] pr-6 pt-20 pb-28 sm:pl-[8vw] sm:pr-10 lg:pl-[8vw] lg:pr-8"
      >
        <div class="w-full max-w-[560px] font-sans">
          <p class="inline-flex items-center gap-2 rounded-full border border-white/10 bg-white/[0.06] px-3 py-1.5 text-[12px] leading-none text-white/55">
            <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-white/70" />
            {{ t('home.hub.badge') }}
          </p>
          <h1 class="mt-6 text-[42px] font-bold leading-[1.2] tracking-tight sm:text-[48px] xl:text-[52px]">
            <span class="block whitespace-nowrap text-white">{{ t('home.hub.titleLine1') }}</span>
            <span class="block whitespace-nowrap text-zinc-300">{{ t('home.hub.titleLine2') }}</span>
            <span class="block whitespace-nowrap text-zinc-300">{{ t('home.hub.titleLine3') }}</span>
          </h1>
          <p class="mt-6 text-[15px] leading-[1.6] text-zinc-400">
            {{ t('home.hub.lead') }}
          </p>
          <p class="mt-4 text-[15px] leading-[1.6] text-zinc-400">
            {{ t('home.hub.detail') }}
          </p>
          <div class="pointer-events-auto mt-6 flex flex-col items-start">
            <router-link
              :to="rechargePath"
              class="group inline-flex items-center gap-2 rounded-full bg-white px-6 py-3 text-sm font-bold text-black no-underline transition hover:bg-zinc-200"
            >
              {{ t('home.hub.recharge') }}
              <svg class="h-4 w-4 transition-transform duration-200 group-hover:translate-x-1" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
            </router-link>
            <div class="mt-5 flex items-center gap-8">
              <router-link
                :to="consolePath"
                class="group inline-flex items-center gap-1.5 text-sm text-white/70 no-underline transition hover:text-white"
              >
                {{ t('home.hub.console') }}
                <svg class="h-3.5 w-3.5 transition-transform duration-200 group-hover:translate-x-1" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                  <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
                </svg>
              </router-link>
              <router-link
                to="/model-plaza"
                class="group inline-flex items-center gap-1.5 text-sm text-white/70 no-underline transition hover:text-white"
              >
                {{ t('home.hub.pricing') }}
                <svg class="h-3.5 w-3.5 transition-transform duration-200 group-hover:translate-x-1" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                  <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
                </svg>
              </router-link>
            </div>
          </div>
        </div>
      </section>
      <section class="hidden h-full lg:block" aria-hidden="true" />
    </main>

    <div class="pointer-events-none absolute inset-x-0 bottom-0 z-20 flex justify-center px-6 pb-6">
      <div class="flex flex-wrap items-center justify-center gap-x-4 gap-y-2 text-[13px] text-white/60">
        <span class="inline-flex items-center gap-1.5">
          <i class="h-2 w-2 rounded-full bg-gradient-to-r from-violet-500 to-blue-500" />
          {{ t('home.hub.center') }}
        </span>
        <span
          v-for="node in nodes"
          :key="node.name"
          class="inline-flex items-center gap-1.5"
        >
          <i class="h-2 w-2 rounded-full" :style="{ backgroundColor: node.color }" />
          {{ node.name }}
        </span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore, useAuthStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import { HUB_NODES } from './hub/hubPhysics'
import { createHubView, type HubView } from './hub/hubScene'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const canvasRef = ref<HTMLCanvasElement | null>(null)
const webglError = ref(false)
const nodes = HUB_NODES
const isDark = ref(document.documentElement.classList.contains('dark'))

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', {
    allowRelative: true,
    allowDataUrl: true,
  }),
)
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))
const rechargePath = computed(() => (isAuthenticated.value ? '/purchase' : '/login'))
const consolePath = computed(() => (isAuthenticated.value ? dashboardPath.value : '/login'))

let view: HubView | null = null
let frameId = 0

function onPointerMove(event: PointerEvent) {
  const x = event.clientX / Math.max(window.innerWidth, 1) * 2 - 1
  const y = event.clientY / Math.max(window.innerHeight, 1) * 2 - 1
  view?.setPointer(x, y)
}

function onWheel(event: WheelEvent) {
  event.preventDefault()
  view?.addDistance(event.deltaY * 0.004)
}

function onWindowWheel(event: WheelEvent) {
  // canvas 在 -z-10，滚轮挂在 window 上，避免被上层挡掉。
  if (event.target instanceof HTMLElement && event.target.closest('button, a, input')) return
  onWheel(event)
}

function resize() {
  const canvas = canvasRef.value
  if (!canvas) return
  view?.resize(canvas.clientWidth, canvas.clientHeight)
}

function frame(now: number) {
  frameId = window.requestAnimationFrame(frame)
  view?.render(now)
}

onMounted(() => {
  const canvas = canvasRef.value
  if (!canvas) return
  view = createHubView(canvas)
  if (!view) {
    webglError.value = true
    return
  }
  resize()
  window.addEventListener('resize', resize)
  window.addEventListener('pointermove', onPointerMove)
  window.addEventListener('wheel', onWindowWheel, { passive: false })
  frameId = window.requestAnimationFrame(frame)
})

onBeforeUnmount(() => {
  window.cancelAnimationFrame(frameId)
  window.removeEventListener('resize', resize)
  window.removeEventListener('pointermove', onPointerMove)
  window.removeEventListener('wheel', onWindowWheel)
  view?.dispose()
  view = null
})
</script>
