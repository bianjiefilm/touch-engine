// HUI-1664 FEAT-0165 公共活动页状态语义(纯函数,无 React 依赖,便于 vitest)。
//
// 复用 T0 五态解析(store.ResolveLink 的 machine state),不新增活动模型;
// 在其上补两类兜底:
//   - network_error:BFF/Go 服务不可达或网络失败 → 文案 + 重试动作;
//   - entry_unsupported:URL 显式携带不支持的入口标记 → 指引改用商家最新
//     二维码/碰一碰标签。无 entry 参数默认视为 web 直接访问(支持)。

export type PublicEntry = "qr" | "nfc" | "web" | "unsupported";

export type PublicUiState =
  | "loading"
  | "available"
  | "link_disabled"
  | "not_started"
  | "expired"
  | "paused"
  | "draft"
  | "ended"
  | "not_found"
  | "network_error"
  | "entry_unsupported";

// 机器态 → 游客文案(与 Go 侧 publicLinkView.state 对齐)
export const STATE_TEXT: Record<PublicUiState, string> = {
  loading: "加载中…",
  available: "活动进行中",
  link_disabled: "链接已停用",
  not_started: "活动尚未开始",
  expired: "活动已结束",
  paused: "活动暂停中",
  draft: "活动未发布",
  ended: "活动已结束",
  not_found: "活动不存在",
  network_error: "网络异常,活动加载失败",
  entry_unsupported: "入口方式不支持",
};

// 每个非可用态都给出可恢复动作(文案层面)
export const STATE_ACTION: Record<PublicUiState, string> = {
  loading: "",
  available: "",
  link_disabled: "请联系商家获取最新的活动入口",
  not_started: "请按活动时间再来看看",
  expired: "活动已结束,感谢关注",
  paused: "请稍后再来",
  draft: "活动还未发布,请稍后再来",
  ended: "感谢关注,请留意商家后续活动",
  not_found: "请核对二维码是否正确,或联系商家",
  network_error: "请检查网络后重试",
  entry_unsupported: "请通过商家提供的最新二维码或碰一碰标签重新进入",
};

// 入口标记白名单:显式非白名单值 → unsupported;缺省 → web(直接访问)。
export const ENTRY_WHITELIST = ["qr", "nfc", "web"] as const;

export function classifyEntry(raw: string | null | undefined): PublicEntry {
  if (raw == null || raw === "") return "web";
  if ((ENTRY_WHITELIST as readonly string[]).includes(raw)) {
    return raw as Exclude<PublicEntry, "unsupported">;
  }
  return "unsupported";
}

// 网络层失败归一:fetch 抛错(status=null)或 BFF/上游 502/503/504 →
// network_error;服务端正常作答(含 404)交给 state 文案,不冒充网络故障。
export function failureState(status: number | null): PublicUiState {
  if (status == null) return "network_error";
  if (status === 502 || status === 503 || status === 504) return "network_error";
  return "not_found";
}
