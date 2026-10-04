"use client";

import { useEffect, useState } from "react";
import { TaskHandoffActions } from "@/components/admin/TaskHandoffActions";
import { MerchantGate, useSession } from "@/components/work/merchant-session";
import { planToday, surfaceLabel, type TodayItem } from "@/lib/product-finish";
import { contentGap } from "@/lib/workbench/compose";

interface CampaignRow {
  id: string;
  title: string;
  status: string;
}

interface StoreRow {
  id: string;
  status?: string;
}

interface WorkbenchCustomers {
  available?: boolean;
  cards?: { pending_sync?: number | null }[];
}

interface WorkbenchPayload {
  content?: { gap?: string };
  customers?: WorkbenchCustomers;
}

export function TodayDesk() {
  return (
    <MerchantGate>
      <TodayBody />
    </MerchantGate>
  );
}

function TodayBody() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [items, setItems] = useState<TodayItem[]>([]);
  const [handoffId, setHandoffId] = useState("");
  const [notice, setNotice] = useState("");

  useEffect(() => {
    let alive = true;
    void (async () => {
      const [campaignsRes, storesRes, workRes] = await Promise.all([
        session.api("GET", "campaigns"),
        session.api("GET", "stores"),
        session.api("GET", "workbench"),
      ]);
      if (!alive) return;
      if (!campaignsRes.ok || !storesRes.ok) {
        setPhase("error");
        return;
      }
      const campaigns = Array.isArray(campaignsRes.data.items) ? (campaignsRes.data.items as CampaignRow[]) : [];
      const stores = Array.isArray(storesRes.data.items) ? (storesRes.data.items as StoreRow[]) : [];
      const work = workRes.ok ? (workRes.data as WorkbenchPayload) : null;
      const pick = (status: string) => campaigns.filter((item) => item.status === status).map((item) => ({ id: item.id, title: item.title }));
      const next = planToday({
        stores: stores.length,
        active: pick("active"),
        paused: pick("paused"),
        ended: pick("ended"),
        drafts: pick("draft"),
        materialGap: contentGap(work?.content?.gap),
        pendingLeads: pendingCount(work?.customers),
        rewardKnown: false,
        redemptionKnown: false,
      });
      setItems(next);
      setHandoffId(pick("active")[0]?.id || pick("draft")[0]?.id || campaigns[0]?.id || "");
      setPhase("ready");
    })().catch(() => {
      if (alive) setPhase("error");
    });
    return () => {
      alive = false;
    };
  }, [session]);

  return (
    <main className="tk-page">
      <p className="tk-kicker">{session.tenantName || "门店"}</p>
      <h1 className="tk-title">今天</h1>
      <ul className="tk-nav">
        <li><a href="/work/stores">门店</a></li>
        <li><a href="/work/campaigns">活动</a></li>
        <li><a href="/work/materials">素材</a></li>
        <li><a href="/work/rewards">奖励</a></li>
        <li><a href="/work/analytics">统计</a></li>
      </ul>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? <p className="tk-state tk-danger" data-state="error">{surfaceLabel("error")}</p> : null}
      {phase === "ready" ? (
        <>
          <p className="tk-lead">{items[0]?.title ?? "先看今天还没收口的事。"}</p>
          <ol className="tk-list">
            {items.map((item) => (
              <li key={item.id}>
                <a href={item.href}>{item.title}</a>
                <span className={item.tone === "unknown" ? "tk-unknown" : "tk-note"}> {toneText(item.tone)}</span>
              </li>
            ))}
          </ol>
          <section className="tk-section">
            <h2 className="tk-section-title">下一步</h2>
            {handoffId ? (
              <>
                <p className="tk-note">获客和内容从当前活动交接。碰一碰不读取联系人，也不把未知写成已排期。</p>
                <TaskHandoffActions campaignId={handoffId} onPlanned={setNotice} />
                <p className="tk-gap">
                  <a href={`/work/campaigns/${handoffId}#jumps`}>去保存这活动的跳转</a>
                </p>
              </>
            ) : (
              <p className="tk-note">先创建活动，再交接获客、内容和跳转。<a href="/work/campaigns">去活动</a></p>
            )}
            {notice ? <p data-testid="task-notice">{notice}</p> : null}
            <p className="tk-gap"><a href="/admin">去处理标签和文案</a></p>
          </section>
        </>
      ) : null}
    </main>
  );
}

function pendingCount(customers: WorkbenchCustomers | undefined): number | null {
  if (!customers || customers.available === false) return null;
  const cards = customers.cards ?? [];
  let seen = false;
  let sum = 0;
  for (const card of cards) {
    if (typeof card.pending_sync === "number") {
      seen = true;
      sum += card.pending_sync;
    }
  }
  if (!seen) return cards.length === 0 ? 0 : null;
  return sum;
}

function toneText(tone: TodayItem["tone"]): string {
  if (tone === "unknown") return "未知";
  if (tone === "paused") return surfaceLabel("paused");
  if (tone === "ended") return surfaceLabel("ended");
  if (tone === "empty") return surfaceLabel("empty");
  return "现在做";
}
