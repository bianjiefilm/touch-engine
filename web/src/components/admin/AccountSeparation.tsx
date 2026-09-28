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
    <div data-testid="account-separation" style={{ display: "grid", gap: 12, gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))" }}>
      <article data-testid="account-package" style={paneStyle}>
        <h3 style={{ marginTop: 0 }}>套餐权限</h3>
        <p style={{ marginBottom: 0 }}>{presented.packageText}</p>
      </article>
      <article data-testid="account-ai-fees" style={paneStyle}>
        <h3 style={{ marginTop: 0 }}>AI 工具费用</h3>
        <p style={{ marginBottom: 0 }}>{presented.feeText}</p>
      </article>
      <article data-testid="account-rewards" style={paneStyle}>
        <h3 style={{ marginTop: 0 }}>营销奖励</h3>
        <p style={{ marginBottom: 0 }}>{presented.rewardText}</p>
      </article>
    </div>
  );
}
