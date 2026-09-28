import { billingBadgeText, type EcoNavModel, type VisibleApp } from "@/lib/eco-nav/model";

export interface WorkingForInput {
  seatSource: "membership" | "delegation" | "agency";
  role: string;
  relationId?: string;
  serverBanner?: string;
  delegations: { delegation_id: string; type: "maker_service" | "ops_collab"; expires_at: string }[];
  merchantName: string;
  now: string;
}

export interface ContentToolInput {
  gap: "needs_material" | "has_material";
  intent: "operate" | "creative_plan" | "deep_edit" | "avatar" | "explain_return";
  apps: VisibleApp[];
  returnProven: boolean;
}

export interface ContentToolOffer {
  id: string;
  app_id: string;
  label: string;
  shown: boolean;
  nav_intent: "task_handoff";
  locks_existing: boolean;
  upgrade?: string;
}

const TOOL_FOR_INTENT: Record<Exclude<ContentToolInput["intent"], "operate" | "explain_return">, { appId: string; id: string; label: string }> = {
  creative_plan: { appId: "goboost", id: "creative_plan", label: "做脚本和分镜" },
  deep_edit: { appId: "aicut", id: "deep_edit", label: "剪辑成片" },
  avatar: { appId: "digital-human", id: "avatar", label: "数字人口播" },
};

function banner(name: string): string {
  const trimmed = name.trim();
  return trimmed ? `正在为 ${trimmed} 商家工作` : "正在为该商家工作";
}

export function resolveWorkingFor(input: WorkingForInput): string | null {
  if (input.seatSource === "membership" || !input.relationId) return null;
  if (input.seatSource === "agency") {
    if (input.role !== "agent") return null;
    return input.serverBanner || banner(input.merchantName);
  }
  if (input.seatSource !== "delegation") return null;
  const match = input.delegations.find(
    (item) => item.delegation_id === input.relationId && item.type === "ops_collab" && item.expires_at > input.now,
  );
  if (!match) return null;
  return banner(input.merchantName);
}

function offerFor(app: VisibleApp | undefined, id: string, label: string): ContentToolOffer | null {
  if (!app) return null;
  if (app.state === "launchable") {
    return { id, app_id: app.app_id, label, shown: true, nav_intent: "task_handoff", locks_existing: false };
  }
  if (app.state === "entitlement_required") {
    return {
      id,
      app_id: app.app_id,
      label,
      shown: false,
      nav_intent: "task_handoff",
      locks_existing: false,
      upgrade: `开通${app.display_name}后可以新增制作，已有素材仍可继续使用。`,
    };
  }
  return null;
}

export function offerContentTools(input: ContentToolInput): ContentToolOffer[] {
  if (input.intent === "explain_return") {
    if (!input.returnProven) return [];
    const app = input.apps.find((item) => item.app_id === "digital-human");
    const offer = offerFor(app, "explain_handoff", "把讲解交给数字人");
    return offer ? [offer] : [];
  }
  if (input.intent === "operate") {
    if (input.gap !== "needs_material") return [];
    const offer = offerFor(
      input.apps.find((item) => item.app_id === "product-image"),
      "supplement_image",
      "补充商品图",
    );
    return offer ? [offer] : [];
  }
  const rule = TOOL_FOR_INTENT[input.intent];
  const offer = offerFor(
    input.apps.find((item) => item.app_id === rule.appId),
    rule.id,
    rule.label,
  );
  return offer ? [offer] : [];
}

export function workbenchBilling(model: EcoNavModel | null): {
  label: string;
  per_tool_balances: [];
  promotions_folded: false;
} {
  if (!model) return { label: "额度需确认", per_tool_balances: [], promotions_folded: false };
  const label = billingBadgeText(model);
  return { label, per_tool_balances: [], promotions_folded: false };
}

export function enterLeadsPlan(input: { app: VisibleApp | null; campaignId: string }): {
  available: boolean;
  locks_campaign: boolean;
  leads_fetch: "not_in_touch";
  nav_intent?: "task_handoff";
  campaign_id?: string;
} {
  if (input.app?.app_id === "leads" && input.app.state === "launchable") {
    return {
      available: true,
      locks_campaign: false,
      leads_fetch: "not_in_touch",
      nav_intent: "task_handoff",
      campaign_id: input.campaignId,
    };
  }
  return { available: false, locks_campaign: false, leads_fetch: "not_in_touch" };
}

export interface TodoInput {
  kind: string;
  campaign_id?: string;
  title: string;
  object_id: string;
  state: string;
}

export function assembleTodos(input: {
  tasks: TodoInput[];
  drafts: { id: string; title: string; status: string }[];
  pending: { campaign_id: string; pending_sync: number }[];
}): TodoInput[] {
  const tasks = input.tasks.map((task) => ({ ...task }));
  for (const draft of input.drafts) {
    if (draft.status !== "draft") continue;
    if (tasks.some((task) => task.kind === "unpublished_campaign" && task.object_id === draft.id)) continue;
    tasks.push({
      kind: "unpublished_campaign",
      campaign_id: draft.id,
      title: "待发布活动",
      object_id: draft.id,
      state: "draft",
    });
  }
  for (const row of input.pending) {
    if (row.pending_sync <= 0) continue;
    if (tasks.some((task) => task.kind === "pending_lead" && (task.campaign_id === row.campaign_id || task.object_id === row.campaign_id))) {
      continue;
    }
    tasks.push({
      kind: "pending_lead",
      campaign_id: row.campaign_id,
      title: "有待同步的授权线索",
      object_id: row.campaign_id,
      state: "pending_sync",
    });
  }
  return tasks;
}

export function salesReceptionLine(card: {
  pending_sync?: number;
  sales_received?: { available?: boolean; value?: number };
}): string {
  const pending = card.pending_sync ?? 0;
  const sales = card.sales_received;
  const confirmed = sales?.available === true && typeof sales.value === "number" && sales.value > 0;
  if (pending > 0 && !confirmed) return `待同步 ${pending}，销售接收尚未确认`;
  if (sales?.available === true && typeof sales.value === "number") return `销售已收到 ${sales.value}`;
  return "销售接收未知";
}

export function visibleSections(taskCount: number): string[] {
  const core = ["content", "activities", "customers"];
  return taskCount > 0 ? ["my_tasks", ...core] : core;
}

export function workbenchColumns(width: number): number {
  return width < 720 ? 1 : 2;
}
