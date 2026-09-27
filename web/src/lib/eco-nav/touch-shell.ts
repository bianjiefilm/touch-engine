export const NAV_INTENT_TASK_HANDOFF = "task_handoff" as const;

export type TaskKind = "make_campaign_image" | "make_campaign_video" | "view_campaign_leads";

export interface TaskHandoffPlan {
  nav_intent: typeof NAV_INTENT_TASK_HANDOFF;
  kind: TaskKind;
  campaign_id: string;
  creates_handoff: true;
  creates_or_restores_project: boolean;
  charges_customer: false;
  /** 碰一碰没有 lead-record 取数路由。交接计划不读取获客联系人。 */
  leads_fetch: "not_in_touch";
}

export function planTaskHandoff(kind: TaskKind, campaignId: string): TaskHandoffPlan {
  return {
    nav_intent: NAV_INTENT_TASK_HANDOFF,
    kind,
    campaign_id: campaignId,
    creates_handoff: true,
    creates_or_restores_project: kind === "make_campaign_image" || kind === "make_campaign_video",
    charges_customer: false,
    leads_fetch: "not_in_touch",
  };
}

export function agentWorkBanner(sessionRole: string, merchantName: string | null | undefined): string | null {
  if (sessionRole !== "agent") return null;
  const name = merchantName?.trim() ?? "";
  if (!name) return "正在为该商家工作";
  return `正在为 ${name} 商家工作`;
}

export interface MerchantSurface {
  campaigns: { id: string }[];
  selectedCampaignId: string | null;
  qrFor?: string | null;
}

export function applyMerchantSwitch<T extends MerchantSurface>(surface: T): T {
  return { ...surface, campaigns: [], selectedCampaignId: null, qrFor: null };
}

export function acceptTenantPayload<T>(requestedTenantId: string, currentTenantId: string, items: T[]): T[] | null {
  if (requestedTenantId !== currentTenantId) return null;
  return items;
}

/** 预览夹具里的租户不能写进商家后台会话。只有公共身份上下文才提交切换。 */
export function commitTenantSwitch(provenance: string, tenantId: string | null | undefined): string | null {
  if (provenance !== "public_ai_context") return null;
  const id = tenantId?.trim() ?? "";
  return id === "" ? null : id;
}

export function payerLabel(provenance: string, confirmedLabel: string): string {
  if (provenance !== "public_ai_context") return "付款主体需确认";
  return confirmedLabel;
}
