// HUI-1896 公共访客活动体验。碰一下、扫码、看视频只打开活动；
// 留资必须另一次明确同意。来源标记不能改租户。

export type PublicSection = "store" | "value" | "actions" | "lead" | "next";

export function publicSectionOrder(): PublicSection[] {
  return ["store", "value", "actions", "lead", "next"];
}

export interface ParticipationEffects {
  lead: boolean;
  crm: boolean;
  platformAccount: boolean;
  order: boolean;
}

const noEffects: ParticipationEffects = {
  lead: false,
  crm: false,
  platformAccount: false,
  order: false,
};

export function participationEffects(input: { kind: string; consent?: boolean }): ParticipationEffects {
  if (input.kind === "lead_submit" && input.consent === true) {
    return { ...noEffects, lead: true };
  }
  if (input.kind === "crm_received") {
    return { ...noEffects, crm: true };
  }
  return noEffects;
}

export function activityBlocks(input: { marketingOptIn: boolean; consent: boolean }): Array<"lead"> {
  void input.marketingOptIn;
  if (!input.consent) return ["lead"];
  return [];
}

export function leadDisclosureReady(input: {
  merchant: string;
  purpose: string;
  requiredFields: string[];
  noticeVersion: string;
}): boolean {
  return Boolean(
    input.merchant.trim() &&
      input.purpose.trim() &&
      input.noticeVersion.trim() &&
      input.requiredFields.length > 0 &&
      input.requiredFields.every((field) => field.trim() !== ""),
  );
}

export function leadOutcomeCopy(input: {
  merchant: string;
  state: string;
  duplicate?: boolean;
  crmReceived?: boolean;
  salesReceived?: boolean | "unknown";
  ownerFollowedUp?: boolean | "unknown";
}): string {
  // 调用方即使传来 true，这里也不把它写成销售已收到或负责人已跟进。
  void input.salesReceived;
  void input.ownerFollowedUp;
  const merchant = input.merchant.trim() || "这家店";
  if (input.duplicate || input.state === "duplicate") {
    return `你已经把联系方式交给${merchant}。这次没有再记一条。`;
  }
  if (input.state === "crm_received" || input.crmReceived) {
    return `已提交给${merchant}，对方客户系统已接收。负责人是否已经跟进，这里还不知道。`;
  }
  if (input.state === "crm_paused") {
    return `${merchant}的客户系统暂停接收。记录还在本页，对方还没确认收到。`;
  }
  if (input.state === "pending_sync") {
    return `已提交给${merchant}。正在同步到对方客户系统，对方还没确认收到。`;
  }
  return `已提交给${merchant}。对方客户系统还没确认收到。你可以在本页撤销授权。`;
}

export interface GuestCapability {
  kind: "wifi" | "navigate" | "review" | "wecom" | "follow" | string;
  available: boolean;
  result: string;
  href?: string;
}

const guestActionKinds = new Set(["wifi", "navigate", "review", "wecom", "follow"]);

export function guestActionLabel(kind: string): string {
  if (kind === "wifi") return "门店 WiFi";
  if (kind === "navigate") return "导航";
  if (kind === "review") return "写点评";
  if (kind === "wecom") return "加企微";
  if (kind === "follow") return "关注账号";
  return "";
}

export function guestActionsFromPayload(payload: {
  actions?: Array<{ kind?: string; available?: boolean; result?: string; href?: string }>;
} | null): GuestCapability[] {
  return (payload?.actions ?? []).flatMap((item) => {
    if (!item?.kind || !guestActionKinds.has(item.kind)) return [];
    return [{
      kind: item.kind,
      available: item.available === true,
      result: item.result === "ready" ? "ready" : "closed",
      href: typeof item.href === "string" ? item.href : "",
    }];
  });
}

export function settleClick(server: {
  recorded_as?: string;
  success?: boolean;
  platform_result?: string;
} | null): {
  recordedAs: "click";
  success: false;
  platformResult: "unknown";
  added: false;
  followed: false;
  leadCreated: false;
  crmImported: false;
  rewardTriggered: false;
  publishSuccess: false;
  open: boolean;
} {
  const honest = server?.recorded_as === "click" && server.success === false && server.platform_result === "unknown";
  return {
    recordedAs: "click",
    success: false,
    platformResult: "unknown",
    added: false,
    followed: false,
    leadCreated: false,
    crmImported: false,
    rewardTriggered: false,
    publishSuccess: false,
    open: honest,
  };
}

export function isOfficialActionUrl(href: string): boolean {
  let url: URL;
  try {
    url = new URL(href);
  } catch {
    return false;
  }
  if (url.protocol !== "https:") return false;
  const host = url.hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (host === "localhost" || host === "127.0.0.1" || host === "::1" || host === "0.0.0.0") return false;
  if (host.endsWith(".invalid") || host.endsWith(".local")) return false;
  if (/^192\.168\./.test(host) || /^10\./.test(host)) return false;
  if (/^172\.(1[6-9]|2\d|3[0-1])\./.test(host)) return false;
  return true;
}

export function presentGuestActions(items: GuestCapability[]): Array<{ kind: string; href: string }> {
  return items
    .filter((item) => guestActionKinds.has(item.kind) && item.available && item.result === "ready" && typeof item.href === "string" && isOfficialActionUrl(item.href))
    .map((item) => ({ kind: item.kind, href: item.href as string }));
}

export function actionClickRecord(kind: string): {
  kind: string;
  recordedAs: "click";
  success: false;
  platformResult: "unknown";
} {
  return { kind, recordedAs: "click", success: false, platformResult: "unknown" };
}

export function beaconChannel(
  entry: string | null,
  _tenantFromQuery: string | null,
): { channel: "qr" | "nfc" | "web"; tenantOverride: null } {
  const channel = entry === "qr" || entry === "nfc" ? entry : "web";
  return { channel, tenantOverride: null };
}

export function nfcCoverageClaim(input: { testedUrls: boolean; devices: string[] }): {
  allModels: false;
  devices: string[];
} {
  void input.testedUrls;
  return { allModels: false, devices: input.devices };
}
