import { describe, expect, it } from "vitest";
import type { VisibleApp } from "@/lib/eco-nav/model";
import {
  assembleTodos,
  enterLeadsPlan,
  offerContentTools,
  resolveWorkingFor,
  salesReceptionLine,
  visibleSections,
  workbenchBilling,
  workbenchColumns,
} from "@/lib/workbench/compose";
import type { EcoNavModel } from "@/lib/eco-nav/model";

function launchable(appId: string): VisibleApp {
  return {
    app_id: appId,
    display_name: appId,
    icon_ref: null,
    state: "launchable",
    launch_mode: "sso_launch",
    launch_target_id: `ti-${appId}`,
    unavailable_reason: null,
  };
}

const now = "2026-09-28T00:00:00Z";

describe("代运营横幅只认已有委托", () => {
  it("本账号成员资格即使带委托编号也不显示正在为商家工作", () => {
    expect(
      resolveWorkingFor({
        seatSource: "membership",
        role: "org_owner",
        relationId: "agr_1",
        serverBanner: "正在为 A餐饮 商家工作",
        delegations: [{ delegation_id: "dlg_1", type: "ops_collab", expires_at: "2099-01-01T00:00:00Z" }],
        merchantName: "A餐饮",
        now,
      }),
    ).toBeNull();
  });

  it("未过期的代运营委托显示正在为该商家工作", () => {
    expect(
      resolveWorkingFor({
        seatSource: "delegation",
        role: "staff",
        relationId: "dlg_ops",
        delegations: [{ delegation_id: "dlg_ops", type: "ops_collab", expires_at: "2099-01-01T00:00:00Z" }],
        merchantName: "A餐饮",
        now,
      }),
    ).toBe("正在为 A餐饮 商家工作");
  });

  it("制作服务委托不当成代运营", () => {
    expect(
      resolveWorkingFor({
        seatSource: "delegation",
        role: "staff",
        relationId: "dlg_make",
        delegations: [{ delegation_id: "dlg_make", type: "maker_service", expires_at: "2099-01-01T00:00:00Z" }],
        merchantName: "A餐饮",
        now,
      }),
    ).toBeNull();
  });

  it("过期委托不再授权代运营横幅", () => {
    expect(
      resolveWorkingFor({
        seatSource: "delegation",
        role: "staff",
        relationId: "dlg_ops",
        delegations: [{ delegation_id: "dlg_ops", type: "ops_collab", expires_at: "2020-01-01T00:00:00Z" }],
        merchantName: "A餐饮",
        now,
      }),
    ).toBeNull();
  });

  it("碰一碰里激活的代管关系可以显示横幅", () => {
    expect(
      resolveWorkingFor({
        seatSource: "agency",
        role: "agent",
        relationId: "agr_1",
        delegations: [],
        merchantName: "A餐饮",
        serverBanner: "正在为 A餐饮 商家工作",
        now,
      }),
    ).toBe("正在为 A餐饮 商家工作");
  });
});

describe("内容动作按当前任务取最小集", () => {
  const apps = [launchable("product-image"), launchable("goboost"), launchable("aicut"), launchable("digital-human")];

  it("缺素材时只提供产品图交接，不把工具铺开", () => {
    const actions = offerContentTools({ gap: "needs_material", intent: "operate", apps, returnProven: false });
    expect(actions.filter((action) => action.shown).map((action) => action.app_id)).toEqual(["product-image"]);
    expect(actions[0]?.nav_intent).toBe("task_handoff");
    expect(actions[0]?.locks_existing).toBe(false);
  });

  it("已有素材时不强制跳到产品图", () => {
    const actions = offerContentTools({ gap: "has_material", intent: "operate", apps, returnProven: false });
    expect(actions.filter((action) => action.shown)).toEqual([]);
  });

  it("未开通产品图时说明增量价值，已有素材不被锁住", () => {
    const actions = offerContentTools({
      gap: "needs_material",
      intent: "operate",
      apps: [
        {
          app_id: "product-image",
          display_name: "产品图",
          icon_ref: null,
          state: "entitlement_required",
          launch_mode: "none",
          launch_target_id: null,
          unavailable_reason: "entitlement_missing",
        },
      ],
      returnProven: false,
    });
    expect(actions).toHaveLength(1);
    expect(actions[0]?.shown).toBe(false);
    expect(actions[0]?.upgrade).toContain("已有素材");
    expect(actions[0]?.locks_existing).toBe(false);
  });

  it("只有创意规划任务才出现 GoBoost", () => {
    const actions = offerContentTools({ gap: "has_material", intent: "creative_plan", apps, returnProven: false });
    expect(actions.filter((action) => action.shown).map((action) => action.app_id)).toEqual(["goboost"]);
  });

  it("深剪才出现 AiCut，口播才出现数字人", () => {
    expect(
      offerContentTools({ gap: "has_material", intent: "deep_edit", apps, returnProven: false })
        .filter((action) => action.shown)
        .map((action) => action.app_id),
    ).toEqual(["aicut"]);
    expect(
      offerContentTools({ gap: "has_material", intent: "avatar", apps, returnProven: false })
        .filter((action) => action.shown)
        .map((action) => action.app_id),
    ).toEqual(["digital-human"]);
  });

  it("回到本活动的制作在未共测前不宣传完成", () => {
    const actions = offerContentTools({ gap: "has_material", intent: "explain_return", apps, returnProven: false });
    expect(actions.find((action) => action.id === "return_to_campaign")).toBeUndefined();
    expect(actions.map((action) => action.label).join(" ")).not.toMatch(/完成|已回到/);
  });
});

describe("账单与获客降级", () => {
  it("没有账单事实时不写 0，也不按工具拆余额", () => {
    const strip = workbenchBilling(null);
    expect(strip.label).toBe("额度需确认");
    expect(strip.per_tool_balances).toEqual([]);
    expect(strip.promotions_folded).toBe(false);
    expect(JSON.stringify(strip)).not.toMatch(/"value"|:0|coupon/);
  });

  it("已知钱包只显示一个账户摘要", () => {
    const model = {
      provenance: "public_ai_context",
      billing_follows_document: true,
      billing: {
        kind: "wallet",
        known: true,
        reason: null,
        payer_source: "personal",
        viewer_account_role: "owner",
        availability: "available",
        amount: { value_minor: 8800, unit: "cny_fen" },
        entry: "billing_center",
      },
    } as EcoNavModel;
    const strip = workbenchBilling(model);
    expect(strip.label).toBe("钱包 CNY 88.00");
    expect(strip.per_tool_balances).toEqual([]);
    expect(strip.promotions_folded).toBe(false);
  });

  it("获客未开通时不锁活动主线", () => {
    const plan = enterLeadsPlan({ app: null, campaignId: "cmp_a" });
    expect(plan.available).toBe(false);
    expect(plan.locks_campaign).toBe(false);
    expect(plan.leads_fetch).toBe("not_in_touch");
  });

  it("获客可启动时交接本活动，不在碰一碰取联系人", () => {
    const plan = enterLeadsPlan({ app: launchable("leads"), campaignId: "cmp_a" });
    expect(plan.available).toBe(true);
    expect(plan.nav_intent).toBe("task_handoff");
    expect(plan.campaign_id).toBe("cmp_a");
    expect(plan.leads_fetch).toBe("not_in_touch");
    expect(plan.locks_campaign).toBe(false);
  });
});

describe("工作台信息架构", () => {
  it("没有我的事情时不占主导航", () => {
    expect(visibleSections(0)).toEqual(["content", "activities", "customers"]);
  });

  it("有待办时仍是内容、活动、客户，而不是产品目录", () => {
    expect(visibleSections(2)).toEqual(["my_tasks", "content", "activities", "customers"]);
  });

  it("草稿进入待办，待同步不写成没有销售接收", () => {
    const tasks = assembleTodos({
      tasks: [],
      drafts: [{ id: "cmp_d", title: "春季店庆", status: "draft" }],
      pending: [{ campaign_id: "cmp_a", pending_sync: 2 }],
    });
    expect(tasks).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ kind: "unpublished_campaign", object_id: "cmp_d", state: "draft" }),
        expect.objectContaining({ kind: "pending_lead", campaign_id: "cmp_a", state: "pending_sync" }),
      ]),
    );
    const pending = tasks.find((task) => task.kind === "pending_lead");
    expect(pending?.title).not.toMatch(/没有销售接收|销售已收到|销售未收到|成功/);
    const line = salesReceptionLine({ pending_sync: 2, sales_received: { available: true, value: 0 } });
    expect(line).toMatch(/待同步/);
    expect(line).not.toMatch(/销售已收到\s*0|没有销售接收|销售未收到/);
    expect(salesReceptionLine({ pending_sync: 1, sales_received: { available: true, value: 3 } })).toBe("销售已收到 3");
  });

  it("窄屏只排一列工作卡", () => {
    expect(workbenchColumns(390)).toBe(1);
    expect(workbenchColumns(960)).toBe(2);
  });
});
