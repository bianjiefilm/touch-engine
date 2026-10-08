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
  // fix2 主行动收敛（gate-r2 盲评 fail 项）：make_campaign_image 是唯一真执行动作
  // （POST 创建/恢复产品图工程），其余两个是说明性交接。页面至多一个可执行主行动。
  return (
    <span className="tk-row">
      {kinds.map((kind) => {
        const plan = planTaskHandoff(kind, campaignId);
        const primary = kind === "make_campaign_image";
        return (
          <button
            key={kind}
            type="button"
            data-testid={`task-${kind}`}
            data-primary-action={primary ? "true" : undefined}
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
            className={primary ? "tk-button" : "tk-quiet"}
          >
            {LABEL[kind]}
          </button>
        );
      })}
    </span>
  );
}
