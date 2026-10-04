// HUI-2628 r2（裁决3-1）：活动详情页的内联 takenList 抽成可测纯函数。
// 语义与页内原实现一致：请求失败或形状不对=error；null 或空数组=empty；否则 ready。
// 空结果不是产品通过，只是这段数据没有可展示行。
export interface TakenListResult {
  state: "error" | "empty" | "ready";
  items: unknown[];
}

export function takenList<T = unknown>(resp: { ok: boolean; items?: unknown[] | null }): TakenListResult {
  if (!resp.ok) return { state: "error", items: [] };
  if (resp.items == null) return { state: "empty", items: [] };
  if (!Array.isArray(resp.items)) return { state: "error", items: [] };
  return { state: resp.items.length === 0 ? "empty" : "ready", items: resp.items as T[] };
}
