import { notFound } from "next/navigation";
import { PreviewShell } from "./preview-shell";
import { AccountSeparation } from "@/components/admin/AccountSeparation";

const previewSeparation = {
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
    items: [
      { kind: "coupon", face_minor: 2000, ledger: "activity" },
      { kind: "points", face_minor: 300, ledger: "activity" },
      { kind: "lottery", face_minor: 100, ledger: "activity" },
      { kind: "group_buy_voucher", face_minor: 1500, ledger: "activity" },
    ],
  },
};

export default function EcoNavPreviewPage() {
  if (process.env.TOUCH_ECO_NAV_PREVIEW !== "1") notFound();
  return (
    <>
      <PreviewShell />
      <main className="tk-admin-shell">
        <h2>账户分开预览</h2>
        <p>这是本地预览，不是生产账单，也不会扣费。</p>
        <AccountSeparation view={previewSeparation} />
      </main>
    </>
  );
}
