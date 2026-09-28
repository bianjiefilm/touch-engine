// HUI-1671: the customer page must not treat a payload as permission to grant
// a publish reward. This deployment cannot prove a platform publish, and a
// manual proof never becomes a platform confirmation.

export interface RewardView {
  status?: string;
  grant_enabled?: boolean;
  issuance_enabled?: boolean;
  message?: string;
  reason?: string;
  user_notice?: string;
}

export interface ProofView {
  status?: string;
  platform_confirmed?: boolean;
  post_id?: string;
  grant?: boolean;
}

const HOLD = "该渠道无法证明发布成功，发布奖励已停用，待核实。";

export function publishRewardOpen(_body: RewardView): boolean {
  return false;
}

export function rewardNotice(body: RewardView): string {
  if (body.user_notice && body.user_notice.trim() !== "") {
    return body.user_notice.trim();
  }
  if (body.message && body.message.includes("待核实") && body.message.includes("停用")) {
    return body.message;
  }
  return HOLD;
}

export function proofIsPlatformConfirmed(_body: ProofView): boolean {
  return false;
}

export function proofNotice(body: ProofView): string {
  if (proofIsPlatformConfirmed(body)) return "平台已确认。";
  if (body.status === "reviewed_not_confirmed") return "已复核。这不是平台已确认的发布，也不会发券。";
  return "证明已提交，待复核。这不是平台已确认，也不会发券。";
}
