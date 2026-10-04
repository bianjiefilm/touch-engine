// HUI-2628 r2（裁决3-2）：顾客公共页留资结果态从页面 data-state 三元抽出。
// 判定顺序按裁决：待同步 → 销售已收到 → 已撤销 → recorded。
// 文案同源：pending/received 文案在 web/src/lib/visitor-experience.ts，
// revoked 文案与 public-campaign 页面渲染共用 LEAD_REVOKED_COPY。
export type LeadOutcomeState = "pending" | "received" | "recorded" | "revoked";

export const LEAD_REVOKED_COPY = "已撤销。未同步的数据会停在这里，不再继续交给商家的客户系统。";

export function leadOutcomeState(copy: string): LeadOutcomeState {
  if (copy.includes("待同步")) return "pending";
  if (copy.includes("销售已收到")) return "received";
  if (copy.includes("已撤销")) return "revoked";
  return "recorded";
}
