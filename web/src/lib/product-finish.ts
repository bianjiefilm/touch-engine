import { publishRewardOpen, type RewardView } from "@/lib/publish-reward";

export type RewardTone = "unknown" | "held" | "confirmed";

export interface RewardPresentation {
  tone: RewardTone;
  success: boolean;
  className: "tk-unknown" | "tk-ok";
}

// 发奖开关关闭时，任何未知、空和待核实都不能画成成功。
export function rewardPresentation(body: RewardView): RewardPresentation {
  const status = (body.status ?? "").trim().toLowerCase();
  if (publishRewardOpen(body) && body.grant_enabled === true && body.issuance_enabled === true && status === "confirmed") {
    return { tone: "confirmed", success: true, className: "tk-ok" };
  }
  if (status === "" || status === "unknown") {
    return { tone: "unknown", success: false, className: "tk-unknown" };
  }
  return { tone: "held", success: false, className: "tk-unknown" };
}

export interface TodayRef {
  id: string;
  title: string;
  ends_at?: string;
}

export interface TodayInput {
  stores: number;
  active: TodayRef[];
  paused: TodayRef[];
  ended: TodayRef[];
  drafts: TodayRef[];
  materialGap: "needs_material" | "has_material" | "unknown";
  pendingLeads: number | null;
  rewardKnown: boolean;
  redemptionKnown: boolean;
}

export interface TodayItem {
  id: string;
  title: string;
  href: string;
  tone: "empty" | "now" | "paused" | "ended" | "expired" | "unknown" | "confirmed";
}

export function planToday(input: TodayInput): TodayItem[] {
  const items: TodayItem[] = [];
  if (input.stores === 0) {
    items.push({ id: "stores", title: "先登记门店", href: "/work/stores", tone: "empty" });
  }
  const draft = input.drafts[0];
  if (draft) items.push({ id: "drafts", title: `先处理草稿「${draft.title}」`, href: `/work/campaigns/${draft.id}`, tone: "now" });
  const paused = input.paused[0];
  if (paused) items.push({ id: "paused", title: `处理暂停的活动「${paused.title}」`, href: "/work/campaigns?state=paused", tone: "paused" });
  const overdue = input.active.filter((item) => isPastEnd(item.ends_at));
  const live = input.active.filter((item) => !isPastEnd(item.ends_at));
  const expired = overdue[0];
  if (expired) items.push({ id: "expired", title: `「${expired.title}」窗口已过，不要当成今天还在进行`, href: "/work/campaigns?state=expired", tone: "expired" });
  const active = live[0];
  if (active) items.push({ id: "active", title: `今天进行中：${active.title}`, href: `/work/campaigns/${active.id}`, tone: "now" });
  const ended = input.ended[0];
  if (ended) items.push({ id: "ended", title: `「${ended.title}」已结束，不要当成还在进行`, href: "/work/campaigns?state=ended", tone: "ended" });
  if (input.materialGap === "needs_material") {
    items.push({ id: "materials", title: "补上活动要用的素材", href: "/work/materials", tone: "now" });
  } else if (input.materialGap === "unknown") {
    items.push({ id: "materials", title: "素材是否齐还不知道", href: "/work/materials", tone: "unknown" });
  }
  if (input.pendingLeads === null) {
    items.push({ id: "leads", title: "留资待同步数量未知", href: "/work/analytics", tone: "unknown" });
  } else if (input.pendingLeads > 0) {
    items.push({ id: "leads", title: `有 ${input.pendingLeads} 条待同步留资`, href: "/work/analytics", tone: "now" });
  }
  if (!input.rewardKnown || !input.redemptionKnown) {
    items.push({ id: "rewards", title: "奖励和核销还不能当成已完成", href: "/work/rewards", tone: "unknown" });
  }
  if (items.length === 0) {
    items.push({ id: "quiet", title: "今天没有待处理的活动", href: "/work/campaigns", tone: "empty" });
  }
  return items;
}

const SURFACE = {
  empty: "还没有记录",
  loading: "正在读取",
  error: "没有读到，请重试",
  expired: "已过期",
  paused: "已暂停",
  ended: "已结束",
} as const;

export type SurfaceState = keyof typeof SURFACE;

export function surfaceLabel(state: SurfaceState): string {
  return SURFACE[state];
}

export function isPastEnd(endsAt: string | undefined, now = Date.now()): boolean {
  if (!endsAt) return false;
  const time = Date.parse(endsAt);
  return Number.isFinite(time) && time < now;
}

export interface HandoffRow {
  id: string;
  status: string;
  ends_at?: string;
}

export interface HandoffPick {
  id?: string;
  state: "live" | "draft" | "paused_fallback" | "none";
}

// HUI-2628 r2（裁决3-3，root 2026-10-04 修正为行为保持抽取）：今天台的交接选择从组件体抽出。
// 与组件现实现逐行对齐：第一个未过期 active → live；否则第一个 draft（不查过期）→ draft；
// 否则第一个未过期且非 ended 的活动（现网即未过期 paused）→ paused_fallback；否则 none。
export function pickHandoff(rows: HandoffRow[], nowISO: string): HandoffPick {
  const now = Date.parse(nowISO);
  const liveActive = rows.find((item) => item.status === "active" && !isPastEnd(item.ends_at, now));
  if (liveActive) return { id: liveActive.id, state: "live" };
  const draft = rows.find((item) => item.status === "draft");
  if (draft) return { id: draft.id, state: "draft" };
  const pausedFallback = rows.find((item) => item.status !== "ended" && !isPastEnd(item.ends_at, now));
  if (pausedFallback) return { id: pausedFallback.id, state: "paused_fallback" };
  return { state: "none" };
}
