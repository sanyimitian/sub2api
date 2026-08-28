import { beforeEach, describe, expect, it, vi } from "vitest";

const { get, put, post } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  post: vi.fn(),
}));

vi.mock("@/api/client", () => ({ apiClient: { get, put, post } }));

import {
  defaultChannelMonitorCooldownSettings,
  getChannelMonitorCooldownSettings,
  resetChannelMonitorCooldownSettings,
  updateChannelMonitorCooldownSettings,
} from "@/api/admin/settings";

describe("admin channel monitor cooldown settings API", () => {
  beforeEach(() => {
    get.mockReset();
    put.mockReset();
    post.mockReset();
  });

  it("提供与服务端一致的默认值", () => {
    expect(defaultChannelMonitorCooldownSettings()).toEqual({
      version: 1,
      cooldown_minutes: [2, 5, 30, 60, 120],
      slow_response_threshold_seconds: 12,
      priority_increment: 1,
      max_priority_increase: 3,
      priority_auto_recovery_seconds: 3600,
    });
  });

  it("使用独立端点读取、保存和恢复默认值", async () => {
    const settings = defaultChannelMonitorCooldownSettings();
    get.mockResolvedValueOnce({ data: settings });
    put.mockResolvedValueOnce({ data: settings });
    post.mockResolvedValueOnce({ data: settings });

    await expect(getChannelMonitorCooldownSettings()).resolves.toEqual(settings);
    await expect(updateChannelMonitorCooldownSettings(settings)).resolves.toEqual(settings);
    await expect(resetChannelMonitorCooldownSettings()).resolves.toEqual(settings);

    expect(get).toHaveBeenCalledWith("/admin/settings/channel-monitor-cooldown");
    expect(put).toHaveBeenCalledWith("/admin/settings/channel-monitor-cooldown", settings);
    expect(post).toHaveBeenCalledWith("/admin/settings/channel-monitor-cooldown/reset");
  });
});
