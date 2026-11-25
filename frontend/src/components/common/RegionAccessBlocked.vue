<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

const props = withDefaults(defineProps<{
  reason?: 'MAINLAND_ACCESS_DENIED' | 'REGION_LOOKUP_UNAVAILABLE'
  retrying?: boolean
  siteName?: string
  preview?: boolean
}>(), {
  reason: 'MAINLAND_ACCESS_DENIED',
  retrying: false,
  siteName: '',
  preview: false,
})

const emit = defineEmits<{ retry: [] }>()
const { t } = useI18n()
const titleId = useId()
const unavailable = computed(() => props.reason === 'REGION_LOOKUP_UNAVAILABLE')
</script>

<template>
  <section class="region-access" :class="{ 'region-access-preview': preview }" :aria-label="t('regionAccess.accessCheck')">
    <header v-if="!preview" class="region-access-header">
      <span class="region-access-brand">{{ siteName || t('regionAccess.accessCheck') }}</span>
      <span v-if="siteName">{{ t('regionAccess.accessCheck') }}</span>
    </header>
    <div class="region-access-stage">
      <article class="region-access-notice" :aria-labelledby="titleId">
        <div class="region-access-notice-head">
          <span class="region-access-icon"><Icon :name="unavailable ? 'exclamationCircle' : 'lock'" size="lg" aria-hidden="true" /></span>
          <span class="region-access-status">{{ t('regionAccess.restricted') }}</span>
        </div>
        <h1 :id="titleId">{{ unavailable ? t('regionAccess.unavailableTitle') : t('regionAccess.blockedTitle') }}</h1>
        <p class="region-access-intro">{{ unavailable ? t('regionAccess.unavailableIntro') : t('regionAccess.blockedIntro') }}</p>
        <div class="region-access-reason">
          <p>{{ unavailable ? t('regionAccess.unavailableReason') : t('regionAccess.blockedReason') }}</p>
          <p v-if="!unavailable" class="region-access-denied">{{ t('regionAccess.denied') }}</p>
        </div>
        <details v-if="!unavailable" class="region-access-regulations">
          <summary>{{ t('regionAccess.regulations') }}<Icon name="chevronDown" size="xs" aria-hidden="true" /></summary>
          <p>{{ t('regionAccess.regulationDetail') }}</p>
          <a href="https://www.cac.gov.cn/2023-07/13/c_1690898327029107.htm" target="_blank" rel="noopener noreferrer">
            {{ t('regionAccess.regulationSource') }}<Icon name="externalLink" size="xs" aria-hidden="true" />
          </a>
        </details>
        <button type="button" class="region-access-retry" :disabled="retrying" @click="emit('retry')">
          <Icon name="refresh" size="sm" aria-hidden="true" :class="{ 'region-access-spinning': retrying }" />
          {{ retrying ? t('regionAccess.retrying') : t('regionAccess.retry') }}
        </button>
        <p class="region-access-help">{{ t('regionAccess.contact') }}</p>
        <p class="region-access-attribution">{{ t('regionAccess.dataSource') }}<a href="https://db-ip.com" target="_blank" rel="noopener noreferrer">{{ t('regionAccess.attribution') }}</a></p>
      </article>
    </div>
  </section>
</template>

<style scoped>
.region-access {
  --region-surface: #ffffff;
  --region-stage: #e9edf2;
  --region-text: #182336;
  --region-muted: #576579;
  --region-line: #e3e8ef;
  --region-blue: #2d74b8;
  --region-warning: #9a5415;
  --region-warning-bg: #fff5e8;
  min-height: 100dvh;
  background: var(--region-stage);
  color: var(--region-text);
}
:global(.dark) .region-access {
  --region-surface: #1e293b;
  --region-stage: #0f172a;
  --region-text: #edf2f9;
  --region-muted: #b2bfd1;
  --region-line: #3a475d;
  --region-blue: #abd4f8;
  --region-warning: #f2c389;
  --region-warning-bg: #3e3024;
}
.region-access-header { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 1.1rem 1.5rem; border-bottom: 1px solid var(--region-line); background: var(--region-surface); color: var(--region-muted); font-size: .75rem; }
.region-access-brand { color: var(--region-text); font-size: 1.1rem; font-weight: 600; overflow-wrap: anywhere; }
.region-access-stage { display: grid; place-items: center; min-height: calc(100dvh - 4rem); padding: 2rem 1rem; }
.region-access-notice { width: min(100%, 31rem); padding: 2rem; border-radius: .875rem; background: var(--region-surface); box-shadow: 0 12px 40px #26354818; }
.region-access-notice-head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-bottom: 1.3rem; }
.region-access-icon { display: grid; place-items: center; width: 2.75rem; height: 2.75rem; border-radius: .75rem; color: var(--region-warning); background: var(--region-warning-bg); }
.region-access-status { color: var(--region-warning); font-size: .75rem; }
.region-access h1 { margin: 0 0 .6rem; font-size: 1.45rem; font-weight: 650; line-height: 1.4; letter-spacing: -.025em; text-wrap: balance; }
.region-access-intro { color: var(--region-muted); font-size: .8125rem; line-height: 1.7; }
.region-access-reason { margin-top: 1.4rem; padding-top: 1.25rem; border-top: 1px solid var(--region-line); font-size: .875rem; line-height: 1.85; }
.region-access-denied { margin-top: .65rem; font-weight: 500; }
.region-access-regulations { margin-top: 1.1rem; font-size: .75rem; line-height: 1.8; color: var(--region-muted); }
.region-access-regulations summary { display: flex; align-items: center; gap: .4rem; color: var(--region-blue); width: fit-content; cursor: pointer; list-style: none; }
.region-access-regulations summary::-webkit-details-marker { display: none; }
.region-access-regulations[open] summary svg { transform: rotate(180deg); }
.region-access-regulations p { margin-top: .6rem; }
.region-access-regulations a { display: inline-flex; align-items: center; gap: .25rem; margin-top: .5rem; }
.region-access a { color: var(--region-blue); text-decoration: underline; text-underline-offset: 3px; }
.region-access-retry { margin-top: 1.4rem; display: flex; align-items: center; justify-content: center; gap: .5rem; width: 100%; min-height: 2.75rem; padding: .6rem 1rem; border-radius: .5rem; color: #ffffff; background: #2d74b8; font-size: .8125rem; font-weight: 500; transition: background-color 160ms ease-out; }
.region-access-retry:hover:not(:disabled) { background: #265e96; }
.region-access-retry:disabled { cursor: wait; opacity: .7; }
.region-access-help, .region-access-attribution { margin-top: .75rem; color: var(--region-muted); font-size: .75rem; line-height: 1.7; text-align: center; }
.region-access-attribution { margin-top: .5rem; }
.region-access-preview { min-height: 0; background: transparent; }
.region-access-preview .region-access-stage { min-height: 0; padding: 0; }
.region-access-preview .region-access-notice { box-shadow: none; padding: 0; width: 100%; }
.region-access-spinning { animation: region-spin 1s linear infinite; }
@keyframes region-spin { to { transform: rotate(360deg); } }
@media (max-width: 420px) {
  .region-access-stage { padding: 1.25rem .75rem; }
  .region-access-notice { padding: 1.5rem 1.25rem; }
  .region-access h1 { font-size: 1.25rem; }
}
@media (prefers-reduced-motion: reduce) {
  .region-access-retry { transition: none; }
  .region-access-spinning { animation: none; }
}
</style>
