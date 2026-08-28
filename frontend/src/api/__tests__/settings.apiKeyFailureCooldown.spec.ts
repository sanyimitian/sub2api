import { beforeEach, describe, expect, it, vi } from "vitest";

const { get, put } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
}));

vi.mock("@/api/client", () => ({
  apiClient: { get, put },
}));

import {
  API_KEY_FAILURE_COOLDOWN_FAMILIES,
  createDefaultAPIKeyFailureCooldownSettings,
  getAPIKeyFailureCooldownSettings,
  updateAPIKeyFailureCooldownSettings,
  type APIKeyFailureCooldownSettings,
} from "@/api/admin/settings";

describe("admin API key failure cooldown settings API", () => {
  beforeEach(() => {
    get.mockReset();
    put.mockReset();
  });

  it("exposes every backend policy with independent default cooldown arrays", () => {
    const first = createDefaultAPIKeyFailureCooldownSettings();
    const second = createDefaultAPIKeyFailureCooldownSettings();

    expect(first).toEqual({
      version: 1,
      policies: {
        rate_limit: { enabled: true, cooldowns: [60, 300, 900], mode: "hold_last" },
        overload: { enabled: true, cooldowns: [60, 300], mode: "hold_last" },
        transient_upstream: { enabled: true, cooldowns: [30, 120, 600], mode: "hold_last" },
        temporary_forbidden: { enabled: true, cooldowns: [300], mode: "hold_last" },
        account_blocked: { enabled: true, cooldowns: [300], mode: "hold_last" },
        unauthorized: { enabled: true, cooldowns: [1800], mode: "hold_last" },
        quota_exhausted: { enabled: true, cooldowns: [3600], mode: "hold_last" },
        model_unsupported: { enabled: true, cooldowns: [1800], mode: "hold_last" },
        global_upstream: { enabled: true, cooldowns: [1800], mode: "hold_last" },
        unknown: { enabled: true, cooldowns: [60, 600, 1800], mode: "cycle" },
      },
    });
    expect(API_KEY_FAILURE_COOLDOWN_FAMILIES).toEqual(Object.keys(first.policies));

    first.policies.rate_limit.cooldowns.push(3600);
    expect(second.policies.rate_limit.cooldowns).toEqual([60, 300, 900]);
  });

  it("maps GET to the versioned endpoint and normalizes the returned ladders", async () => {
    const payload = createDefaultAPIKeyFailureCooldownSettings();
    payload.policies.rate_limit.cooldowns = [900, 60, 300, 60];
    get.mockResolvedValue({ data: payload });

    const result = await getAPIKeyFailureCooldownSettings();

    expect(get).toHaveBeenCalledWith("/admin/settings/api-key-failure-cooldown");
    expect(result.policies.rate_limit.cooldowns).toEqual([60, 300, 900]);
    expect(result).not.toBe(payload);
    expect(result.policies.rate_limit.cooldowns).not.toBe(
      payload.policies.rate_limit.cooldowns,
    );
  });

  it("maps PUT without sharing mutable policy arrays with the caller", async () => {
    const request = createDefaultAPIKeyFailureCooldownSettings();
    const response: APIKeyFailureCooldownSettings = {
      ...request,
      policies: {
        ...request.policies,
        unknown: {
          enabled: false,
          cooldowns: [1800, 60, 600, 60],
          mode: "cycle",
        },
      },
    };
    put.mockResolvedValue({ data: response });

    const result = await updateAPIKeyFailureCooldownSettings(request);

    expect(put).toHaveBeenCalledWith(
      "/admin/settings/api-key-failure-cooldown",
      expect.objectContaining({ version: 1 }),
    );
    const sent = put.mock.calls[0]?.[1] as APIKeyFailureCooldownSettings;
    expect(sent).toEqual(request);
    expect(sent).not.toBe(request);
    expect(sent.policies.rate_limit.cooldowns).not.toBe(
      request.policies.rate_limit.cooldowns,
    );
    expect(result.policies.unknown.cooldowns).toEqual([60, 600, 1800]);
    expect(result.policies.unknown.enabled).toBe(false);
  });
});
