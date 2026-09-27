import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { PROVISIONAL_DOCUMENT } from "@/lib/eco-nav/fixture";
import {
  billingBadgeText,
  parseEcoNavDocument,
  planManualSwitch,
  selectTenant,
  switchableApps,
  toViewModel,
  type VisibleApp,
} from "@/lib/eco-nav/model";
import {
  acceptTenantPayload,
  agentWorkBanner,
  applyMerchantSwitch,
  commitTenantSwitch,
  payerLabel,
  planTaskHandoff,
} from "@/lib/eco-nav/touch-shell";

function launchable(partial: Partial<VisibleApp> & Pick<VisibleApp, "app_id" | "display_name">): VisibleApp {
  return {
    icon_ref: null,
    state: "launchable",
    launch_mode: "sso_launch",
    launch_target_id: "ti-product-image",
    unavailable_reason: null,
    ...partial,
  } as VisibleApp;
}

describe("碰一碰手工切换与任务交接", () => {
  it("手工切产品图不带 campaign_id，也不创建或恢复项目", () => {
    const app = launchable({ app_id: "product-image", display_name: "产品图" });
    const manual = planManualSwitch(app, true, () => "https://127.0.0.1/apps/product-image?campaign_id=cmp_a");
    expect(manual.href).toBeNull();
    expect(manual.creates_handoff).toBe(false);
    expect(manual.nav_intent).toBe("manual_switch");
    expect(manual).not.toHaveProperty("campaign_id");

    const task = planTaskHandoff("make_campaign_image", "cmp_a");
    expect(task.nav_intent).toBe("task_handoff");
    expect(task.creates_handoff).toBe(true);
    expect(task.creates_or_restores_project).toBe(true);
    expect(task.campaign_id).toBe("cmp_a");
    expect(task.charges_customer).toBe(false);
    expect(task.leads_fetch).toBe("not_in_touch");
  });

  it("查看线索只记交接，不在碰一碰取 lead-record", () => {
    const task = planTaskHandoff("view_campaign_leads", "cmp_b");
    expect(task.creates_or_restores_project).toBe(false);
    expect(task.leads_fetch).toBe("not_in_touch");
    expect(task.charges_customer).toBe(false);
  });
});

describe("品牌应用列表与代运营切换", () => {
  it("品牌甲的应用不会出现在品牌乙", () => {
    const base = structuredClone(PROVISIONAL_DOCUMENT);
    const brandA = structuredClone(base);
    brandA.brand.display_name = "品牌甲";
    brandA.visible_apps = [
      base.visible_apps[0],
      {
        app_id: "product-image",
        display_name: "产品图",
        icon_ref: null,
        state: "launchable",
        launch_mode: "sso_launch",
        launch_target_id: "ti-product-image",
        unavailable_reason: null,
      },
    ];
    const brandB = structuredClone(base);
    brandB.brand.brand_id = "brand-b";
    brandB.brand.display_name = "品牌乙";
    brandB.visible_apps = [
      base.visible_apps[0],
      {
        app_id: "leads",
        display_name: "获客",
        icon_ref: null,
        state: "launchable",
        launch_mode: "sso_launch",
        launch_target_id: "ti-leads-web",
        unavailable_reason: null,
      },
    ];
    const parsedA = parseEcoNavDocument(brandA);
    const parsedB = parseEcoNavDocument(brandB);
    if (!parsedA.ok || !parsedB.ok) throw new Error("brand docs");
    const viewA = toViewModel(parsedA.document, { provenance: "provisional_fixture", statusSummary: "" });
    const viewB = toViewModel(parsedB.document, { provenance: "provisional_fixture", statusSummary: "" });
    expect(viewA.brand?.display_name).toBe("品牌甲");
    expect(viewB.brand?.display_name).toBe("品牌乙");
    expect(viewA.current_app.app_id).toBe("touch");
    const appsA = switchableApps(viewA.apps, viewA.current_app.app_id).map((item) => item.app_id);
    const appsB = switchableApps(viewB.apps, viewB.current_app.app_id).map((item) => item.app_id);
    expect(appsA).toEqual(["product-image"]);
    expect(appsB).toEqual(["leads"]);
    expect(appsA).not.toEqual(appsB);
  });

  it("代运营切换商家后清掉活动，额度不再沿用上一商家", () => {
    expect(agentWorkBanner("agent", "A餐饮")).toBe("正在为 A餐饮 商家工作");
    expect(agentWorkBanner("org_owner", "A餐饮")).toBeNull();
    expect(agentWorkBanner("agent", "  ")).toBe("正在为该商家工作");

    const surface = applyMerchantSwitch({
      campaigns: [{ id: "cmp_a" }],
      selectedCampaignId: "cmp_a",
      qrFor: "cmp_a",
    });
    expect(surface.campaigns).toEqual([]);
    expect(surface.selectedCampaignId).toBeNull();
    expect(surface.qrFor).toBeNull();

    const parsed = parseEcoNavDocument(PROVISIONAL_DOCUMENT);
    if (!parsed.ok) throw new Error("provisional");
    const live = toViewModel(parsed.document, { provenance: "public_ai_context", statusSummary: "" });
    const moved = selectTenant(
      {
        ...live,
        billing_follows_document: true,
        billing: {
          kind: "wallet",
          known: true,
          reason: null,
          payer_source: "delegation",
          viewer_account_role: "payer",
          availability: "available",
          amount: { value_minor: 8800, unit: "cny_fen" },
          entry: "billing_center",
        },
      },
      "tenant-b",
    );
    expect(moved.active_tenant_id).toBe("tenant-b");
    expect(billingBadgeText(moved)).toBe("额度需确认");
    expect(billingBadgeText(moved)).not.toContain("88.00");
    expect(commitTenantSwitch("provisional_fixture", "tenant-a")).toBeNull();
    expect(commitTenantSwitch("public_ai_context", "tnt_real")).toBe("tnt_real");
    expect(payerLabel("provisional_fixture", "个人付款")).toBe("付款主体需确认");
    expect(payerLabel("public_ai_context", "委托付款")).toBe("委托付款");
    expect(acceptTenantPayload("tenant-a", "tenant-b", [{ id: "cmp_a" }])).toBeNull();
    expect(acceptTenantPayload("tenant-b", "tenant-b", [{ id: "cmp_b" }])).toEqual([{ id: "cmp_b" }]);
  });
});

describe("公共页不挂生态顶栏", () => {
  const publicFiles = ["src/app/page.tsx", "src/app/layout.tsx", "src/app/c/[code]/page.tsx"];

  it("首页、根布局和公共活动页不引用 EcoTopNav", () => {
    for (const file of publicFiles) {
      const source = readFileSync(path.resolve(process.cwd(), file), "utf8");
      expect(source, file).not.toMatch(/EcoTopNav|AdminShell/);
    }
  });

  it("顶栏组件不发起交接、不读取线索、不写跳转地址", () => {
    const source = readFileSync(path.resolve(process.cwd(), "src/components/eco-nav/EcoTopNav.tsx"), "utf8");
    expect(source).not.toMatch(/task_handoff|campaign_id|lead-record|fetch\(|https?:\/\//);
  });
});
