import { describe, expect, it } from "vitest";
import { planToday, rewardPresentation, surfaceLabel } from "@/lib/product-finish";

describe("奖励未知不是成功", () => {
  it("status unknown 不成功，也不用成功语气", () => {
    const view = rewardPresentation({ status: "unknown" });
    expect(view.success).toBe(false);
    expect(view.tone).toBe("unknown");
    expect(view.className).toBe("tk-unknown");
    expect(view.className).not.toBe("tk-ok");
  });

  it("待核实和空状态都不是已发放", () => {
    for (const body of [{ status: "pending_verification", grant_enabled: false }, {}, { status: "" }]) {
      const view = rewardPresentation(body);
      expect(view.success).toBe(false);
      expect(view.className).not.toBe("tk-ok");
    }
  });
});

describe("商家今天的下一步", () => {
  it("没有门店时第一件是登记门店，奖励未知不是完成", () => {
    const items = planToday({
      stores: 0,
      active: [],
      paused: [],
      ended: [],
      drafts: [],
      materialGap: "unknown",
      pendingLeads: null,
      rewardKnown: false,
      redemptionKnown: false,
    });
    expect(items[0]).toMatchObject({ id: "stores", href: "/work/stores", tone: "empty" });
    expect(items.some((item) => item.href === "/work/rewards" && item.tone === "unknown")).toBe(true);
    expect(items.some((item) => item.tone === "confirmed")).toBe(false);
  });

  it("进行中的活动、暂停、结束、素材和待同步留资各走真实页面", () => {
    const items = planToday({
      stores: 1,
      active: [{ id: "cmp_live", title: "到店送一杯" }],
      paused: [{ id: "cmp_pause", title: "暂停中" }],
      ended: [{ id: "cmp_end", title: "已结束场" }],
      drafts: [{ id: "cmp_draft", title: "草稿场" }],
      materialGap: "needs_material",
      pendingLeads: 2,
      rewardKnown: false,
      redemptionKnown: false,
    });
    expect(items.map((item) => item.href)).toEqual(expect.arrayContaining([
      "/work/campaigns/cmp_draft",
      "/work/campaigns?state=paused",
      "/work/campaigns/cmp_live",
      "/work/campaigns?state=ended",
      "/work/materials",
      "/work/analytics",
      "/work/rewards",
    ]));
    expect(items.find((item) => item.id === "paused")?.tone).toBe("paused");
    expect(items.find((item) => item.id === "ended")?.tone).toBe("ended");
    expect(items.find((item) => item.id === "rewards")?.tone).toBe("unknown");
  });
});

describe("页面状态文案", () => {
  it("空、加载、错误、过期、暂停、结束各自不同", () => {
    const labels = ["empty", "loading", "error", "expired", "paused", "ended"].map((state) => surfaceLabel(state as "empty"));
    expect(new Set(labels).size).toBe(labels.length);
    expect(surfaceLabel("expired")).toContain("过期");
    expect(surfaceLabel("paused")).toContain("暂停");
    expect(surfaceLabel("ended")).toContain("结束");
    expect(surfaceLabel("error")).not.toContain("成功");
    expect(surfaceLabel("empty")).not.toContain("0");
  });
});
