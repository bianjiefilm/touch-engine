import React, { type CSSProperties } from "react";
import { presentAccountSeparation, type SeparationView } from "@/lib/account-separation";

const paneStyle: CSSProperties = {
  border: "1px solid #e5e7eb",
  borderRadius: 12,
  padding: 14,
  background: "#fff",
};

export function AccountSeparation({ view }: { view: SeparationView }) {
  const presented = presentAccountSeparation(view);
  return (
    <div data-testid="account-separation" className="tk-admin-grid-12">
      <article data-testid="account-package" style={paneStyle}>
        <h3 className="tk-admin-mt-0">套餐权限</h3>
        <p className="tk-admin-mb-0">{presented.packageText}</p>
      </article>
      <article data-testid="account-ai-fees" style={paneStyle}>
        <h3 className="tk-admin-mt-0">AI 工具费用</h3>
        <p className="tk-admin-mb-0">{presented.feeText}</p>
      </article>
      <article data-testid="account-rewards" style={paneStyle}>
        <h3 className="tk-admin-mt-0">营销奖励</h3>
        <p className="tk-admin-mb-0">{presented.rewardText}</p>
      </article>
    </div>
  );
}
