import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createDefaultAPIKeyFailureCooldownSettings } from "@/api/admin/settings";
import APIKeyFailureCooldownSettings from "../APIKeyFailureCooldownSettings.vue";

const { getSettings, updateSettings, showError, showSuccess } = vi.hoisted(() => ({
  getSettings: vi.fn(),
  updateSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock("@/api", () => ({
  adminAPI: {
    settings: {
      getAPIKeyFailureCooldownSettings: getSettings,
      updateAPIKeyFailureCooldownSettings: updateSettings,
    },
  },
}));

vi.mock("@/stores", () => ({
  useAppStore: () => ({ showError, showSuccess }),
}));

vi.mock("@/utils/apiError", () => ({
  extractApiErrorMessage: (error: unknown, fallback: string) =>
    error instanceof Error ? error.message : fallback,
}));

vi.mock("vue-i18n", async () => {
  const actual = await vi.importActual<typeof import("vue-i18n")>("vue-i18n");
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  };
});

describe("APIKeyFailureCooldownSettings", () => {
  beforeEach(() => {
    getSettings.mockReset();
    updateSettings.mockReset();
    showError.mockReset();
    showSuccess.mockReset();
    getSettings.mockResolvedValue(createDefaultAPIKeyFailureCooldownSettings());
    updateSettings.mockImplementation(async (settings) => settings);
  });

  it("loads and displays every default policy with its fixed scope", async () => {
    const wrapper = mount(APIKeyFailureCooldownSettings);
    await flushPromises();

    expect(getSettings).toHaveBeenCalledTimes(1);
    expect(wrapper.findAll('[data-testid^="api-key-cooldown-policy-"]')).toHaveLength(10);
    expect(
      (wrapper.get('[data-testid="api-key-cooldown-rate_limit-cooldowns"]')
        .element as HTMLInputElement).value,
    ).toBe("60, 300, 900");
    expect(
      (wrapper.get('[data-testid="api-key-cooldown-unknown-mode"]')
        .element as HTMLSelectElement).value,
    ).toBe("cycle");
    expect(wrapper.get('[data-testid="api-key-cooldown-overload-scope"]').text()).toContain(
      "admin.settings.apiKeyFailureCooldown.scope.accountModel",
    );
    expect(wrapper.get('[data-testid="api-key-cooldown-rate_limit-scope"]').text()).toContain(
      "admin.settings.apiKeyFailureCooldown.scope.account",
    );
  });

  it("edits a policy and saves the complete versioned configuration", async () => {
    const wrapper = mount(APIKeyFailureCooldownSettings);
    await flushPromises();

    await wrapper
      .get('[data-testid="api-key-cooldown-rate_limit-cooldowns"]')
      .setValue("30, 120, 600");
    await wrapper
      .get('[data-testid="api-key-cooldown-rate_limit-mode"]')
      .setValue("cycle");
    await wrapper.get('[data-testid="api-key-failure-cooldown-save"]').trigger("click");
    await flushPromises();

    expect(updateSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        version: 1,
        policies: expect.objectContaining({
          rate_limit: {
            enabled: true,
            cooldowns: [30, 120, 600],
            mode: "cycle",
          },
        }),
      }),
    );
    expect(Object.keys(updateSettings.mock.calls[0][0].policies)).toHaveLength(10);
    expect(showSuccess).toHaveBeenCalledWith(
      "admin.settings.apiKeyFailureCooldown.saved",
    );
  });

  it.each(["", "60, 0, 300", "60, later, 300", "60.5, 300"])(
    "blocks an invalid cooldown ladder: %s",
    async (draft) => {
      const wrapper = mount(APIKeyFailureCooldownSettings);
      await flushPromises();

      const input = wrapper.get(
        '[data-testid="api-key-cooldown-rate_limit-cooldowns"]',
      );
      await input.setValue(draft);
      await wrapper.get('[data-testid="api-key-failure-cooldown-save"]').trigger("click");
      await flushPromises();

      expect(updateSettings).not.toHaveBeenCalled();
      expect(input.attributes("aria-invalid")).toBe("true");
      expect(
        wrapper.get('[data-testid="api-key-cooldown-rate_limit-error"]').text(),
      ).toBe("admin.settings.apiKeyFailureCooldown.validation.positiveIntegers");
    },
  );

  it("shows a server error and keeps the edited values when save fails", async () => {
    updateSettings.mockRejectedValue(new Error("server rejected cooldown policy"));
    const wrapper = mount(APIKeyFailureCooldownSettings);
    await flushPromises();

    const input = wrapper.get(
      '[data-testid="api-key-cooldown-unknown-cooldowns"]',
    );
    await input.setValue("90, 900, 2700");
    await wrapper.get('[data-testid="api-key-failure-cooldown-save"]').trigger("click");
    await flushPromises();

    expect(showError).toHaveBeenCalledWith("server rejected cooldown policy");
    expect((input.element as HTMLInputElement).value).toBe("90, 900, 2700");
  });

  it("reloads the latest server configuration and discards unsaved drafts", async () => {
    const initial = createDefaultAPIKeyFailureCooldownSettings();
    const latest = createDefaultAPIKeyFailureCooldownSettings();
    latest.policies.unknown.cooldowns = [120, 1200, 3600];
    getSettings.mockReset();
    getSettings
      .mockResolvedValueOnce(initial)
      .mockResolvedValueOnce(latest);
    const wrapper = mount(APIKeyFailureCooldownSettings);
    await flushPromises();

    const input = wrapper.get(
      '[data-testid="api-key-cooldown-unknown-cooldowns"]',
    );
    await input.setValue("9, 99");
    await wrapper
      .get('[data-testid="api-key-failure-cooldown-reload"]')
      .trigger("click");
    await flushPromises();

    expect(getSettings).toHaveBeenCalledTimes(2);
    const reloadedInput = wrapper.get(
      '[data-testid="api-key-cooldown-unknown-cooldowns"]',
    );
    expect((reloadedInput.element as HTMLInputElement).value).toBe(
      "120, 1200, 3600",
    );
  });
});
