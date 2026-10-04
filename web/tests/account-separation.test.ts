import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AccountSeparation } from "@/components/admin/AccountSeparation";
import { presentAccountSeparation, publicVisitorCopy } from "@/lib/account-separation";

const expiredWithCoupon = {
  package: {
    kind: "touch_subscription" as const,
    status: "expired",
    restricts_new_premium: true,
    locks_existing: false,
  },
  ai_fees: {
    kind: "ai_tool_fee" as const,
    quote_created: false,
    usage_created: false,
    includes_marketing_face: false,
    balance_mutated: false,
    personal_wallet_debited: false,
    reason: "payer_authorization_required",
  },
  rewards: {
    kind: "marketing_reward" as const,
    ledger: "activity" as const,
    appears_in_platform_wallet: false,
    face_as_cash_expense: false,
    items: [{ kind: "coupon", face_minor: 2000, ledger: "activity" }],
  },
};

describe("套餐、工具费和营销奖励分开", () => {
  it("过期只限制新增高级能力，券面额留在营销奖励", () => {
    const presented = presentAccountSeparation(expiredWithCoupon);
    expect(presented.packageText).toContain("不能新增高级能力");
    expect(presented.packageText).toContain("已有活动");
    expect(presented.feeText).not.toContain("20.00");
    expect(presented.feeText).not.toContain("2000");
    expect(presented.rewardText).toContain("20.00");
    expect(presented.rewardText).toContain("不是平台现金");
    expect(presented.showsEnterpriseBalance).toBe(false);
  });

  it("界面三块分开，费用块里没有券面额", () => {
    const html = renderToStaticMarkup(createElement(AccountSeparation, { view: expiredWithCoupon }));
    expect(html).toContain("套餐权限");
    expect(html).toContain("AI 工具费用");
    expect(html).toContain("营销奖励");
    const fee = html.split('data-testid="account-ai-fees"')[1]?.split('data-testid="account-rewards"')[0] ?? "";
    expect(fee).not.toContain("20.00");
    expect(html).toContain("20.00");
    expect(html).not.toContain("企业余额");
  });
});

describe("公共访客不看到企业余额", () => {
  it("浏览、留资、领券文案不带余额，也不创建平台账户", () => {
    // 夹具故意多传余额字段，证明公共文案忽略它们；断言收窄只为通过 TS 多余属性检查。
    const copy = publicVisitorCopy({
      action: "claim",
      balance: 8800,
      wallet: { value_minor: 8800 },
      org_balance: "88.00",
    } as { action?: string });
    expect(copy.showsEnterpriseBalance).toBe(false);
    expect(copy.createsPlatformUser).toBe(false);
    expect(copy.text).not.toContain("88.00");
    expect(copy.text).not.toContain("8800");
    expect(copy.text).toContain("不用登录");
  });
});
