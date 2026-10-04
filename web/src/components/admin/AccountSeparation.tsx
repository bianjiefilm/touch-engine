import React from "react";
import { presentAccountSeparation, type SeparationView } from "@/lib/account-separation";

export function AccountSeparation({ view }: { view: SeparationView }) {
  const presented = presentAccountSeparation(view);
  return (
    <div data-testid="account-separation" className="tk-admin-grid-12">
      {/* HUI-2628 r2：paneStyle 常量并入 tk-admin-card（token 化），uifinish 文件级闸 0 命中。 */}
      <article data-testid="account-package" className="tk-admin-card">
        <h3 className="tk-admin-mt-0">套餐权限</h3>
        <p className="tk-admin-mb-0">{presented.packageText}</p>
      </article>
      <article data-testid="account-ai-fees" className="tk-admin-card">
        <h3 className="tk-admin-mt-0">AI 工具费用</h3>
        <p className="tk-admin-mb-0">{presented.feeText}</p>
      </article>
      <article data-testid="account-rewards" className="tk-admin-card">
        <h3 className="tk-admin-mt-0">营销奖励</h3>
        <p className="tk-admin-mb-0">{presented.rewardText}</p>
      </article>
    </div>
  );
}
