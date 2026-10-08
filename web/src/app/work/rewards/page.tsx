"use client";

import { useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { presentAccountSeparation, type SeparationView } from "@/lib/account-separation";
import { rewardPresentation, surfaceLabel } from "@/lib/product-finish";
import { rewardNotice, type RewardView } from "@/lib/publish-reward";

interface Campaign {
  id: string;
  title: string;
  status: string;
}

interface Ruleset {
  reward_threshold?: number;
  daily_publish_limit?: number;
  duplicate_publish_window_hours?: number;
  per_contact_daily_submission_cap?: number;
}

export default function RewardsPage() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "empty" | "ready">("loading");
  const [error, setError] = useState("");
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [campaignId, setCampaignId] = useState("");
  const [rulesNote, setRulesNote] = useState("规则还没读取");
  const [reward, setReward] = useState<RewardView>({ status: "unknown" });
  const [separation, setSeparation] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let alive = true;
    void (async () => {
      const [campaignRes, accountRes] = await Promise.all([
        session.api("GET", "campaigns"),
        session.api("GET", "account-separation"),
      ]);
      if (!alive) return;
      if (!campaignRes.ok) {
        setError(failureText(campaignRes.status, campaignRes.data));
        setPhase("error");
        return;
      }
      const items = Array.isArray(campaignRes.data.items) ? (campaignRes.data.items as Campaign[]) : [];
      setCampaigns(items);
      setCampaignId(items[0]?.id ?? "");
      if (accountRes.ok) {
        const presented = presentAccountSeparation(accountRes.data as unknown as SeparationView);
        setSeparation(presented.rewardText);
      } else {
        setSeparation("营销奖励账本没有读到。不把它写成已发放。");
      }
      setPhase(items.length === 0 ? "empty" : "ready");
    })().catch(() => {
      if (alive) setPhase("error");
    });
    return () => {
      alive = false;
    };
  }, [session, attempt]);

  useEffect(() => {
    if (!campaignId) return;
    let alive = true;
    void (async () => {
      const rulesRes = await session.api("GET", `campaigns/${campaignId}/rules`);
      if (!alive) return;
      if (rulesRes.status === 404) {
        setRulesNote("还没有活动规则，或规则未开通。缺失不是已核销，也不是不限已经生效的证明。");
      } else if (!rulesRes.ok) {
        setRulesNote(failureText(rulesRes.status, rulesRes.data));
      } else {
        const ruleset = (rulesRes.data.ruleset ?? {}) as Ruleset;
        setRulesNote([
          line("奖励门槛", ruleset.reward_threshold),
          line("每日发布上限", ruleset.daily_publish_limit),
          line("重复发布窗口（小时）", ruleset.duplicate_publish_window_hours),
          line("每人每日提交上限", ruleset.per_contact_daily_submission_cap),
          "发布上限和奖励门槛仍未接入发布事实。这里不能改核销。",
        ].join(" "));
      }
      const linkRes = await session.api("GET", `campaigns/${campaignId}/links`);
      const links = linkRes.ok && Array.isArray(linkRes.data.items) ? linkRes.data.items as { code?: string }[] : [];
      const code = links.find((item) => item.code)?.code;
      if (!code) {
        setReward({ status: "unknown" });
        return;
      }
      const rewardRes = await fetch(`/api/public/links/${encodeURIComponent(code)}/publish-reward`);
      const data = (await rewardRes.json().catch(() => null)) as RewardView | null;
      if (!alive) return;
      setReward(rewardRes.ok && data ? data : { status: "unknown" });
    })().catch(() => {
      if (alive) setReward({ status: "unknown" });
    });
    return () => {
      alive = false;
    };
  }, [campaignId, session]);

  const presented = rewardPresentation(reward);

  return (
    <main>
      <h1 className="tk-title">奖励和规则</h1>
      <p className="tk-lead">未知奖励不是已发放。核销没有事实时保持未知。这一页只读，不改门槛，也不发券。</p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {error || surfaceLabel("error")}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => setAttempt((a) => a + 1)}>重试</button>
        </p>
      ) : null}
      {phase === "empty" ? <p className="tk-state" data-state="empty">还没有活动，所以没有可查看的奖励规则。</p> : null}
      {phase === "ready" ? (
        <>
          <label className="tk-label">
            活动
            <select className="tk-select" value={campaignId} onChange={(event) => setCampaignId(event.target.value)}>
              {campaigns.map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}
            </select>
          </label>
          <p className={presented.className} data-reward-tone={presented.tone} data-success={presented.success ? "true" : "false"}>
            {rewardNotice(reward)} 核销未知。
          </p>
          <p className="tk-note">{rulesNote}</p>
          <p className="tk-note">{separation}</p>
          <p className="tk-gap"><a href={campaignId ? `/work/campaigns/${campaignId}` : "/work/campaigns"}>回到活动</a></p>
        </>
      ) : null}
    </main>
  );
}

function line(name: string, value: number | undefined): string {
  if (typeof value !== "number") return `${name}未设置（空表示不限）。这不是已核销。`;
  return `${name} ${value}。`;
}
