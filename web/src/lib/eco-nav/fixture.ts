import { parseEcoNavDocument, toViewModel, PROVISIONAL_STATUS_SUMMARY, type EcoNavModel } from "@/lib/eco-nav/model";

/**
 * PROVISIONAL：本地预览文档，本身必须能被冻结解析器接受。
 * 不是 public-ai Identity，也不是生产 Manifest。
 * 启动目标只写 launch_target_id，不写 URL。
 */

const APP_IDS = [
  ["goboost", "GoBoost", "ti-goboost-web"],
  ["product-image", "产品图", "ti-product-image-web"],
  ["aicut", "AiCut", "ti-aicut-web"],
  ["digital-human", "数海观澜数字人", "ti-digital-human"],
  ["leads", "获客", "ti-leads-web"],
  ["orders", "订单", "ti-orders-web"],
] as const;

function launchable(appId: string, displayName: string, targetId: string) {
  return {
    app_id: appId,
    display_name: displayName,
    icon_ref: null,
    state: "launchable" as const,
    launch_mode: "sso_launch" as const,
    launch_target_id: targetId,
    unavailable_reason: null,
  };
}

function scope(tenantId: string, displayName: string) {
  return { tenant_id: tenantId, display_name: displayName, source: "membership" as const };
}

export const PROVISIONAL_DOCUMENT = {
  schema_version: "eco-nav/v1",
  status: { state: "degraded" as const, reasons: ["billing_unavailable"] },
  brand: {
    brand_id: "brand-huigoo",
    kind: "first_party" as const,
    status: "active" as const,
    config_version: "0",
    display_name: "数海观澜",
    logo: null,
    theme: { token_set_ref: "default", accent: null },
    support: null,
  },
  app: {
    app_id: "touch",
    display_name: "碰一碰",
    icon_ref: null,
    home_target_id: "ti-touch-web",
  },
  visible_apps: [
    {
      app_id: "touch",
      display_name: "碰一碰",
      icon_ref: null,
      state: "current" as const,
      launch_mode: "none" as const,
      launch_target_id: null,
      unavailable_reason: null,
    },
    ...APP_IDS.map(([appId, displayName, targetId]) => launchable(appId, displayName, targetId)),
  ],
  work_context: {
    state: "resolved" as const,
    current_scope: scope("tenant-a", "客户甲"),
    switchable_scopes: [scope("tenant-a", "客户甲"), scope("tenant-b", "客户乙")],
    restored: false,
  },
  role: { label: "商家", source: "membership" as const, delegations: [] },
  billing: {
    kind: "wallet" as const,
    known: false,
    reason: "unavailable" as const,
    payer_source: "personal" as const,
    viewer_account_role: null,
    availability: "unknown" as const,
    amount: null,
    entry: null,
  },
  return_context: null,
  capabilities: {
    can_switch_app: true,
    can_switch_tenant: true,
    can_manage_members: false,
    can_open_billing: false,
  },
};

export function provisionalEcoNav(summary = PROVISIONAL_STATUS_SUMMARY): EcoNavModel {
  const parsed = parseEcoNavDocument(PROVISIONAL_DOCUMENT);
  if (!parsed.ok) throw new Error("provisional eco-nav document is not eco-nav/v1");
  return toViewModel(parsed.document, { provenance: "provisional_fixture", statusSummary: summary });
}
