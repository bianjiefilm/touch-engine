// HUI-1672 跳转矩阵。网页可达、唤起客户端、平台动作是三条分开的证据。
// 客户端只打开和按钮一致、且服务端仍标记为未知点击的地址。

export interface ClickReceipt {
  href?: string;
  recorded_as?: string;
  success?: boolean;
  platform_result?: string;
}

export interface ClosedJump {
  kind?: string;
  reason?: string;
  href?: string;
}

export interface ClosedNotice {
  kind: string;
  reason: string;
  text: string;
}

export interface ReturnView {
  shown?: boolean;
  href?: string;
}

export interface JumpDraft {
  enabled: boolean;
  href: string;
  revoked: boolean;
  expires_at: string;
}

const CLOSED_TEXT: Record<string, string> = {
  expired: "已失效",
  revoked: "已撤销",
  unauthorized: "未授权",
  cross_brand: "跨品牌",
};

const KIND_LABEL: Record<string, string> = {
  wifi: "门店 WiFi",
  navigate: "导航",
  review: "写点评",
  follow: "关注账号",
  return: "返回",
};

export function merchantJumpKinds(): Array<"wifi" | "navigate" | "review" | "follow"> {
  return ["wifi", "navigate", "review", "follow"];
}

export function emptyJumpDraft(): JumpDraft {
  return { enabled: false, href: "", revoked: false, expires_at: "" };
}

export function canonicalHref(buttonHref: string, serverHref: string | undefined): string {
  if (!buttonHref || typeof serverHref !== "string" || serverHref === "" || buttonHref !== serverHref) return "";
  return serverHref;
}

export function honestOpen(buttonHref: string, server: ClickReceipt | null): boolean {
  if (!server) return false;
  if (server.recorded_as !== "click" || server.success !== false || server.platform_result !== "unknown") return false;
  return canonicalHref(buttonHref, server.href) !== "";
}

export function closedNotices(rows: ClosedJump[] | null | undefined): ClosedNotice[] {
  const notices: ClosedNotice[] = [];
  for (const row of rows ?? []) {
    if (!row?.kind || row.kind === "wecom" || row.kind === "community") continue;
    if (typeof row.href === "string" && row.href !== "") continue;
    const reason = row.reason ?? "";
    const reasonText = CLOSED_TEXT[reason];
    if (!reasonText) continue;
    const label = KIND_LABEL[row.kind] ?? row.kind;
    notices.push({ kind: row.kind, reason, text: `${label}：${reasonText}` });
  }
  return notices;
}

export function capabilityGapLines(): string[] {
  return [
    "原生唤起未验证。这里不能确认手机上有没有对应客户端。",
    "不会自动关注、自动加群或自动付费。",
    "连 WiFi、打开地图或点评客户端都要你自己点。网页打开不是这些动作成功。",
  ];
}

export function standingEvidenceCopy(): string {
  return "网页地址只说明网页可达。原生唤起未验证。平台动作仍是未知，不是加企微、关注或点评成功。";
}

export function openedEvidenceCopy(): string {
  return "网页已打开，这只说明网页可达。原生唤起未验证。平台动作仍是未知，不是加企微、关注或点评成功。";
}

export function authorizedReturnHref(ret: ReturnView | null | undefined): string {
  if (!ret || ret.shown !== true || typeof ret.href !== "string" || ret.href === "") return "";
  if (openMode(ret.href) === "refuse") return "";
  return ret.href;
}

export function openMode(href: string): "assign" | "blank" | "refuse" {
  if (typeof href !== "string" || href === "") return "refuse";
  if (href.startsWith("/") && !href.startsWith("//")) return "assign";
  if (href.startsWith("https://")) return "blank";
  return "refuse";
}

export function saveJumpError(code: string): string {
  if (code === "unregistered_url") return "未登记的外部地址已拒绝";
  if (code === "bad_expires_at") return "失效时间必须是 RFC3339";
  if (code === "forbidden") return "未授权，不能配置跳转";
  return "没有保存";
}

export function unauthorizedConfigCopy(): string {
  return "未授权，不能配置跳转";
}

export function putJumpActions(
  actions: Record<string, JumpDraft>,
  configured: Array<{ kind?: string; href?: string; enabled?: boolean; revoked?: boolean; expires_at?: string }> | undefined,
): Array<JumpDraft & { kind: string }> {
  const out: Array<JumpDraft & { kind: string }> = merchantJumpKinds().map((kind) => ({
    kind,
    ...(actions[kind] ?? emptyJumpDraft()),
  }));
  for (const item of configured ?? []) {
    if (item?.kind !== "wecom") continue;
    out.push({
      kind: "wecom",
      enabled: item.enabled === true,
      href: typeof item.href === "string" ? item.href : "",
      revoked: item.revoked === true,
      expires_at: typeof item.expires_at === "string" ? item.expires_at : "",
    });
  }
  return out;
}

export function draftsFromMerchant(payload: {
  configured?: Array<{ kind?: string; href?: string; enabled?: boolean; revoked?: boolean; expires_at?: string }>;
  return_configured?: { href?: string; enabled?: boolean; revoked?: boolean; expires_at?: string };
} | null): { actions: Record<string, JumpDraft>; returnDraft: JumpDraft } {
  const actions: Record<string, JumpDraft> = {};
  for (const kind of merchantJumpKinds()) actions[kind] = emptyJumpDraft();
  for (const item of payload?.configured ?? []) {
    if (!item?.kind || !Object.prototype.hasOwnProperty.call(actions, item.kind)) continue;
    actions[item.kind] = {
      enabled: item.enabled === true,
      href: typeof item.href === "string" ? item.href : "",
      revoked: item.revoked === true,
      expires_at: typeof item.expires_at === "string" ? item.expires_at : "",
    };
  }
  const saved = payload?.return_configured;
  return {
    actions,
    returnDraft: {
      enabled: saved?.enabled === true,
      href: typeof saved?.href === "string" ? saved.href : "",
      revoked: saved?.revoked === true,
      expires_at: typeof saved?.expires_at === "string" ? saved.expires_at : "",
    },
  };
}
