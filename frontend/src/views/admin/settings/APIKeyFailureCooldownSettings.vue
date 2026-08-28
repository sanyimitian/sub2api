<template>
  <section class="card" data-testid="api-key-failure-cooldown-settings">
    <header
      class="flex items-start justify-between gap-4 border-b border-gray-100 px-4 py-4 sm:px-6 dark:border-dark-700"
    >
      <div class="min-w-0">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ t("admin.settings.apiKeyFailureCooldown.title") }}
        </h2>
        <p class="mt-1 max-w-3xl text-sm text-gray-600 dark:text-gray-300">
          {{ t("admin.settings.apiKeyFailureCooldown.description") }}
        </p>
      </div>
      <button
        type="button"
        class="btn btn-secondary btn-sm shrink-0 px-2"
        :aria-label="t('admin.settings.apiKeyFailureCooldown.reload')"
        :title="t('admin.settings.apiKeyFailureCooldown.reload')"
        :disabled="loading || saving"
        data-testid="api-key-failure-cooldown-reload"
        @click="loadSettings"
      >
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
      </button>
    </header>

    <div v-if="loading" class="space-y-3 p-4 sm:p-6" aria-busy="true">
      <div
        v-for="index in 4"
        :key="index"
        class="h-14 animate-pulse rounded-md bg-gray-100 dark:bg-dark-700"
      />
    </div>

    <template v-else>
      <div
        class="border-b border-gray-100 bg-gray-50 px-4 py-3 text-sm text-gray-700 sm:px-6 dark:border-dark-700 dark:bg-dark-800/50 dark:text-gray-300"
      >
        <div class="flex items-start gap-2">
          <Icon name="infoCircle" size="sm" class="mt-0.5 shrink-0 text-primary-600 dark:text-primary-400" />
          <p class="max-w-3xl">
            {{ t("admin.settings.apiKeyFailureCooldown.scopeNotice") }}
          </p>
        </div>
      </div>

      <div
        class="hidden grid-cols-[minmax(0,1.35fr)_minmax(12rem,1fr)_10rem_3rem] gap-4 border-b border-gray-100 px-6 py-2 text-xs font-medium text-gray-500 lg:grid dark:border-dark-700 dark:text-gray-400"
        aria-hidden="true"
      >
        <span>{{ t("admin.settings.apiKeyFailureCooldown.columns.failure") }}</span>
        <span>{{ t("admin.settings.apiKeyFailureCooldown.columns.cooldowns") }}</span>
        <span>{{ t("admin.settings.apiKeyFailureCooldown.columns.mode") }}</span>
        <span class="text-right">{{ t("admin.settings.apiKeyFailureCooldown.columns.enabled") }}</span>
      </div>

      <div class="divide-y divide-gray-100 dark:divide-dark-700">
        <div
          v-for="item in policyItems"
          :key="item.family"
          class="grid gap-4 px-4 py-4 lg:grid-cols-[minmax(0,1.35fr)_minmax(12rem,1fr)_10rem_3rem] lg:items-center sm:px-6"
          :data-testid="`api-key-cooldown-policy-${item.family}`"
        >
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <label
                class="text-sm font-medium text-gray-900 dark:text-white"
                :for="`api-key-cooldown-${item.family}-cooldowns`"
              >
                {{ t(`admin.settings.apiKeyFailureCooldown.families.${item.family}.title`) }}
              </label>
              <span
                class="rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300"
                :data-testid="`api-key-cooldown-${item.family}-scope`"
              >
                {{ t(`admin.settings.apiKeyFailureCooldown.scope.${item.scope}`) }}
              </span>
            </div>
            <p class="mt-1 text-xs text-gray-600 dark:text-gray-400">
              {{ t(`admin.settings.apiKeyFailureCooldown.families.${item.family}.description`) }}
            </p>
          </div>

          <div>
            <label
              class="mb-1 block text-xs font-medium text-gray-600 lg:sr-only dark:text-gray-400"
              :for="`api-key-cooldown-${item.family}-cooldowns`"
            >
              {{ t("admin.settings.apiKeyFailureCooldown.columns.cooldowns") }}
            </label>
            <div class="flex items-center gap-2">
              <input
                :id="`api-key-cooldown-${item.family}-cooldowns`"
                v-model="cooldownDrafts[item.family]"
                type="text"
                inputmode="numeric"
                class="input min-w-0 flex-1"
                :class="validationAttempted && validationErrors[item.family] ? 'border-red-500 focus:border-red-500 focus:ring-red-500' : ''"
                :placeholder="t('admin.settings.apiKeyFailureCooldown.cooldownsPlaceholder')"
                :aria-invalid="validationAttempted && Boolean(validationErrors[item.family])"
                :aria-describedby="validationAttempted && validationErrors[item.family] ? `api-key-cooldown-${item.family}-error` : undefined"
                :data-testid="`api-key-cooldown-${item.family}-cooldowns`"
              />
              <span class="shrink-0 text-xs text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.apiKeyFailureCooldown.seconds") }}
              </span>
            </div>
            <p
              v-if="validationAttempted && validationErrors[item.family]"
              :id="`api-key-cooldown-${item.family}-error`"
              class="mt-1.5 text-xs text-red-600 dark:text-red-400"
              :data-testid="`api-key-cooldown-${item.family}-error`"
            >
              {{ t("admin.settings.apiKeyFailureCooldown.validation.positiveIntegers") }}
            </p>
          </div>

          <div>
            <label
              class="mb-1 block text-xs font-medium text-gray-600 lg:sr-only dark:text-gray-400"
              :for="`api-key-cooldown-${item.family}-mode`"
            >
              {{ t("admin.settings.apiKeyFailureCooldown.columns.mode") }}
            </label>
            <select
              :id="`api-key-cooldown-${item.family}-mode`"
              v-model="form.policies[item.family].mode"
              class="input w-full"
              :data-testid="`api-key-cooldown-${item.family}-mode`"
            >
              <option value="hold_last">
                {{ t("admin.settings.apiKeyFailureCooldown.modes.holdLast") }}
              </option>
              <option value="cycle">
                {{ t("admin.settings.apiKeyFailureCooldown.modes.cycle") }}
              </option>
            </select>
          </div>

          <div class="flex items-center justify-between gap-3 lg:justify-end">
            <span class="text-xs font-medium text-gray-600 lg:sr-only dark:text-gray-400">
              {{ t("admin.settings.apiKeyFailureCooldown.columns.enabled") }}
            </span>
            <Toggle
              v-model="form.policies[item.family].enabled"
              :aria-label="t(`admin.settings.apiKeyFailureCooldown.families.${item.family}.toggle`)"
              :data-testid="`api-key-cooldown-${item.family}-enabled`"
            />
          </div>
        </div>
      </div>

      <footer
        class="flex items-center justify-end border-t border-gray-100 px-4 py-4 sm:px-6 dark:border-dark-700"
      >
        <button
          type="button"
          class="btn btn-primary btn-sm"
          :disabled="saving || (validationAttempted && hasValidationErrors)"
          data-testid="api-key-failure-cooldown-save"
          @click="saveSettings"
        >
          <Icon
            :name="saving ? 'refresh' : 'check'"
            size="sm"
            class="mr-1.5"
            :class="saving ? 'animate-spin' : ''"
          />
          {{ saving ? t("common.saving") : t("common.save") }}
        </button>
      </footer>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from "vue";
import { useI18n } from "vue-i18n";

import { adminAPI } from "@/api";
import {
  API_KEY_FAILURE_COOLDOWN_FAMILIES,
  createDefaultAPIKeyFailureCooldownSettings,
  type APIKeyFailureCooldownSettings,
  type APIKeyFailureFamily,
} from "@/api/admin/settings";
import Toggle from "@/components/common/Toggle.vue";
import Icon from "@/components/icons/Icon.vue";
import { useAppStore } from "@/stores";
import { extractApiErrorMessage } from "@/utils/apiError";

const { t } = useI18n();
const appStore = useAppStore();

const loading = ref(true);
const saving = ref(false);
const validationAttempted = ref(false);
const form = reactive<APIKeyFailureCooldownSettings>(
  createDefaultAPIKeyFailureCooldownSettings(),
);
const cooldownDrafts = reactive<Record<APIKeyFailureFamily, string>>(
  Object.fromEntries(
    API_KEY_FAILURE_COOLDOWN_FAMILIES.map((family) => [
      family,
      form.policies[family].cooldowns.join(", "),
    ]),
  ) as Record<APIKeyFailureFamily, string>,
);

const modelScopedFamilies = new Set<APIKeyFailureFamily>([
  "overload",
  "model_unsupported",
]);
const policyItems = API_KEY_FAILURE_COOLDOWN_FAMILIES.map((family) => ({
  family,
  scope: modelScopedFamilies.has(family) ? "accountModel" : "account",
}));

function parseCooldownDraft(draft: string): number[] | null {
  const tokens = draft.split(",").map((value) => value.trim());
  if (
    tokens.length === 0 ||
    tokens.some((value) => !/^[1-9]\d*$/.test(value))
  ) {
    return null;
  }

  const cooldowns = tokens.map(Number);
  if (cooldowns.some((seconds) => !Number.isSafeInteger(seconds))) {
    return null;
  }
  return [...new Set(cooldowns)].sort((left, right) => left - right);
}

const validationErrors = computed<Partial<Record<APIKeyFailureFamily, true>>>(() => {
  const errors: Partial<Record<APIKeyFailureFamily, true>> = {};
  for (const family of API_KEY_FAILURE_COOLDOWN_FAMILIES) {
    if (!parseCooldownDraft(cooldownDrafts[family])) {
      errors[family] = true;
    }
  }
  return errors;
});
const hasValidationErrors = computed(
  () => Object.keys(validationErrors.value).length > 0,
);

function applySettings(settings: APIKeyFailureCooldownSettings): void {
  form.version = settings.version;
  for (const family of API_KEY_FAILURE_COOLDOWN_FAMILIES) {
    const policy = settings.policies[family];
    form.policies[family] = {
      ...policy,
      cooldowns: [...policy.cooldowns],
    };
    cooldownDrafts[family] = policy.cooldowns.join(", ");
  }
  validationAttempted.value = false;
}

function buildPayload(): APIKeyFailureCooldownSettings {
  const payload = createDefaultAPIKeyFailureCooldownSettings();
  for (const family of API_KEY_FAILURE_COOLDOWN_FAMILIES) {
    payload.policies[family] = {
      ...form.policies[family],
      cooldowns: parseCooldownDraft(cooldownDrafts[family]) ?? [],
    };
  }
  return payload;
}

async function loadSettings(): Promise<void> {
  loading.value = true;
  try {
    applySettings(await adminAPI.settings.getAPIKeyFailureCooldownSettings());
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.apiKeyFailureCooldown.loadFailed"),
      ),
    );
  } finally {
    loading.value = false;
  }
}

async function saveSettings(): Promise<void> {
  validationAttempted.value = true;
  if (hasValidationErrors.value) {
    return;
  }

  saving.value = true;
  try {
    const updated = await adminAPI.settings.updateAPIKeyFailureCooldownSettings(
      buildPayload(),
    );
    applySettings(updated);
    appStore.showSuccess(t("admin.settings.apiKeyFailureCooldown.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.apiKeyFailureCooldown.saveFailed"),
      ),
    );
  } finally {
    saving.value = false;
  }
}

void loadSettings();
</script>
