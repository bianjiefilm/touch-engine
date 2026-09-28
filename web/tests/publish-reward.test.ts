import { describe, expect, it } from "vitest";
import { proofIsPlatformConfirmed, publishRewardOpen, rewardNotice } from "../src/lib/publish-reward";

describe("发布奖励在无法核实发布时保持停用", () => {
  it("待核实状态不能发奖，也不能发券", () => {
    const body = {
      status: "pending_verification",
      grant_enabled: false,
      issuance_enabled: false,
      coupons_issued: 0,
      message: "该渠道无法证明发布成功，发布奖励已停用，待核实。",
    };
    expect(publishRewardOpen(body)).toBe(false);
    expect(publishRewardOpen({ status: "active", grant_enabled: true, issuance_enabled: true })).toBe(false);
    expect(rewardNotice(body)).toContain("待核实");
    expect(rewardNotice(body)).toContain("停用");
  });

  it("预览、导出、自报和未知都不会被文案说成已发券", () => {
    for (const reason of ["preview_is_not_publish", "export_is_not_publish", "self_report_is_not_publish", "unknown_is_not_publish"]) {
      expect(rewardNotice({ status: "pending_verification", grant_enabled: false, reason })).not.toContain("已发券");
      expect(publishRewardOpen({ status: "pending_verification", grant_enabled: false, reason })).toBe(false);
    }
  });

  it("人工证明待复核不是平台已确认", () => {
    expect(proofIsPlatformConfirmed({ status: "pending_review", platform_confirmed: false, post_id: "" })).toBe(false);
    expect(proofIsPlatformConfirmed({ status: "reviewed_not_confirmed", platform_confirmed: false })).toBe(false);
    expect(proofIsPlatformConfirmed({ status: "pending_review", platform_confirmed: true })).toBe(false);
  });
});
