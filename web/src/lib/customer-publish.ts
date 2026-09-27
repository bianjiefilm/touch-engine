// HUI-1670: decisions for the customer preview page. A registered adapter
// does not enable publishing. Publish success requires the three flags the
// server sets only for a verifiable platform receipt.

export interface CapCell {
  enabled: boolean;
  reason: string;
  evidence_url?: string;
}

export interface PlatformRow {
  platform: string;
  manual_guide: string;
  capabilities: Record<string, CapCell>;
}

export interface CapabilityMatrix {
  adapters_do_not_enable?: boolean;
  registered_adapters?: string[];
  platforms: PlatformRow[];
}

export interface AttemptView {
  status?: string;
  counts_as_published?: boolean;
  publish_success?: boolean;
  self_reported?: boolean;
}

const PLATFORM_LABEL: Record<string, string> = {
  douyin: "抖音",
  kuaishou: "快手",
  xiaohongshu: "小红书",
  channels: "视频号",
};

export function platformLabel(platform: string): string {
  return PLATFORM_LABEL[platform] ?? platform;
}

export function actionEnabled(row: PlatformRow | undefined, kind: string): boolean {
  return row?.capabilities?.[kind]?.enabled === true;
}

export function authorizedPublishEnabled(matrix: CapabilityMatrix): boolean {
  return matrix.platforms.some((row) => actionEnabled(row, "authorized_publish"));
}

export function publishSucceeded(body: AttemptView): boolean {
  return body.status === "publish_confirmed" && body.counts_as_published === true && body.publish_success === true;
}

export function outcomeMessage(body: AttemptView): string {
  if (publishSucceeded(body)) return "平台已确认这次发布。";
  if (body.self_reported) return "这是你的自报，还不能算发布成功，也不会发放已发布奖励。";
  if (body.status === "unknown") return "发布结果未知。请先向平台查询，不要重复发送。";
  if (body.status === "exported") return "已导出。请按步骤在官方 App 里手动发布。导出本身不是发布成功。";
  if (body.status === "previewed") return "这是预览，还没有发布。";
  if (body.status === "editor_opened" || body.status === "publish_requested") return "这一步不是平台确认的发布成功。";
  return "还不能把这一步当成发布成功。";
}
