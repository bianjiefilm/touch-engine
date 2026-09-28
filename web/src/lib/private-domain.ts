// HUI-1673 私域引流。只展示商家配置、且当前渠道能打开的企微联系或社群链接。
// click_wecom / click_community 只是点击。没有企微回调时不标已接通，也不记核销。

export interface PrivateDomainEntry {
  kind: "wecom" | "community";
  href: string;
  event: "click_wecom" | "click_community";
}

export interface PrivateDomainGuide {
  entries: PrivateDomainEntry[];
  connected: false;
  redemption: "unknown";
}

export interface PrivateDomainClickRecord {
  kind: string;
  event: "click_wecom" | "click_community";
  recordedAs: "click";
  success: false;
  platformResult: "unknown";
  redemption: "unknown";
}

const openChannels = new Set(["web", "qr", "nfc"]);

export function presentPrivateDomain(
  payload: {
    entries?: Array<{ kind?: string; available?: boolean; result?: string; href?: string; event?: string }>;
  } | null,
  channel: string,
): PrivateDomainGuide {
  const guide: PrivateDomainGuide = { entries: [], connected: false, redemption: "unknown" };
  if (!openChannels.has(channel)) return guide;
  const seen = new Set<string>();
  for (const item of payload?.entries ?? []) {
    const entry = acceptEntry(item);
    if (!entry || seen.has(entry.kind)) continue;
    seen.add(entry.kind);
    guide.entries.push(entry);
  }
  return guide;
}

export function activityJumpActions(actions: Array<{ kind: string; href: string }>): Array<{ kind: string; href: string }> {
  return actions.filter((item) => item.kind !== "wecom" && item.kind !== "community");
}

export function privateDomainLabel(kind: string): string {
  if (kind === "wecom") return "加企微";
  if (kind === "community") return "进社群";
  return "";
}

export function privateDomainClick(kind: string): PrivateDomainClickRecord {
  return {
    kind,
    event: kind === "community" ? "click_community" : "click_wecom",
    recordedAs: "click",
    success: false,
    platformResult: "unknown",
    redemption: "unknown",
  };
}

export function settlePrivateDomainClick(server: {
  event?: string;
  recorded_as?: string;
  success?: boolean;
  platform_result?: string;
  redemption?: string;
  connected?: boolean;
} | null): {
  event: "click_wecom" | "click_community";
  recordedAs: "click";
  success: false;
  platformResult: "unknown";
  added: false;
  joined: false;
  contactCreated: false;
  redemption: "unknown";
  connected: false;
  open: boolean;
} {
  const event = server?.event === "click_community" ? "click_community" : "click_wecom";
  const honest = server?.event === event &&
    server.recorded_as === "click" &&
    server.success === false &&
    server.platform_result === "unknown" &&
    server.redemption === "unknown" &&
    server.connected === false;
  return {
    event,
    recordedAs: "click",
    success: false,
    platformResult: "unknown",
    added: false,
    joined: false,
    contactCreated: false,
    redemption: "unknown",
    connected: false,
    open: honest,
  };
}

export function privateDomainNote(
  guide: PrivateDomainGuide,
  settled?: { open: boolean },
): string {
  void guide;
  if (settled?.open) {
    return "这一下只是点击。还不是添加成功、进群成功或新增联系人。核销结果未知。";
  }
  return "";
}

function acceptEntry(item: { kind?: string; available?: boolean; result?: string; href?: string; event?: string } | undefined): PrivateDomainEntry | null {
  if (!item || item.available !== true || item.result !== "ready" || typeof item.href !== "string") return null;
  if (item.kind === "wecom" && item.event === "click_wecom" && isWecomContact(item.href)) {
    return { kind: "wecom", href: item.href, event: "click_wecom" };
  }
  if (item.kind === "community" && item.event === "click_community" && isWecomGroup(item.href)) {
    return { kind: "community", href: item.href, event: "click_community" };
  }
  return null;
}

function isWecomContact(href: string): boolean {
  return officialPath(href, "/ca/");
}

function isWecomGroup(href: string): boolean {
  return officialPath(href, "/gm/");
}

function officialPath(href: string, prefix: string): boolean {
  let url: URL;
  try {
    url = new URL(href);
  } catch {
    return false;
  }
  if (url.protocol !== "https:" || url.username || url.password) return false;
  if (url.hostname.toLowerCase() !== "work.weixin.qq.com") return false;
  if (!url.pathname.startsWith(prefix)) return false;
  const rest = url.pathname.slice(prefix.length).replace(/^\/+|\/+$/g, "");
  if (!rest || rest.includes("..")) return false;
  const segment = rest.split("/")[0];
  return segment !== "" && segment !== ".";
}
