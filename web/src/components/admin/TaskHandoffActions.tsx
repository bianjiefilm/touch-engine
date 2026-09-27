"use client";

import { planTaskHandoff, type TaskKind } from "@/lib/eco-nav/touch-shell";

const LABEL: Record<TaskKind, string> = {
  make_campaign_image: "为本活动制作图片",
  make_campaign_video: "为本活动制作视频",
  view_campaign_leads: "查看本活动线索",
};

const NOTICE: Record<TaskKind, string> = {
  make_campaign_image: "已准备为本活动制作图片的交接。手工切换应用不会创建目标项目，也不会把当前活动带过去。",
  make_campaign_video: "已准备为本活动制作视频的交接。手工切换应用不会创建目标项目，也不会把当前活动带过去。",
  view_campaign_leads: "已准备查看本活动线索的交接。碰一碰这里不读取 lead-record，获客契约对端也不表示本活动已经打通。",
};

export function TaskHandoffActions({
  campaignId,
  onPlanned,
}: {
  campaignId: string;
  onPlanned: (message: string) => void;
}) {
  const kinds: TaskKind[] = ["make_campaign_image", "make_campaign_video", "view_campaign_leads"];
  return (
    <span style={{ display: "inline-flex", gap: 6, flexWrap: "wrap" }}>
      {kinds.map((kind) => {
        const plan = planTaskHandoff(kind, campaignId);
        return (
          <button
            key={kind}
            type="button"
            data-testid={`task-${kind}`}
            data-nav-intent={plan.nav_intent}
            data-creates-handoff="true"
            data-creates-project={plan.creates_or_restores_project ? "true" : "false"}
            data-campaign-id={plan.campaign_id}
            data-charges-customer="false"
            data-leads-fetch={plan.leads_fetch}
            onClick={() => {
              if (kind !== "make_campaign_image") {
                onPlanned(NOTICE[kind]);
                return;
              }
              void fetch("/api/eco-nav/campaign-image", {
                method: "POST",
                headers: { "content-type": "application/json" },
                body: JSON.stringify({ campaign_id: campaignId }),
              })
                .then(async (res) => {
                  const data = (await res.json()) as { message?: string; resolution?: string; project_id?: string };
                  if (!res.ok || !data.project_id) {
                    onPlanned(data.message || "产品图没有创建或恢复工程");
                    return;
                  }
                  const verb = data.resolution === "restored" ? "恢复" : "创建";
                  onPlanned(`产品图已${verb}工程 ${data.project_id}`);
                })
                .catch(() => onPlanned("产品图没有创建或恢复工程"));
            }}
            style={{ padding: "8px 10px", borderRadius: 6, border: "1px solid #2563eb", background: "#fff", color: "#2563eb", cursor: "pointer" }}
          >
            {LABEL[kind]}
          </button>
        );
      })}
    </span>
  );
}
