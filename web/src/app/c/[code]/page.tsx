"use client";

// 公共活动页(路由区 /c/[code]):游客只读。数据仅来自公共白名单 API
// GET /api/public/links/{code};本页无会话、无写操作、不暴露后台存在性。
// 非有效态(停用/过期/暂停/不存在)分别给出明确文案,且统一不带后台信息。

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";

interface PublicView {
  state: string;
  title?: string;
  public_content?: string;
  starts_at?: string;
  ends_at?: string;
}

const STATE_TEXT: Record<string, string> = {
  available: "活动进行中",
  link_disabled: "链接已停用",
  not_started: "活动尚未开始",
  expired: "活动已结束",
  paused: "活动暂停中",
  draft: "活动未发布",
  ended: "活动已结束",
  not_found: "活动不存在",
};

export default function PublicCampaignPage() {
  const params = useParams<{ code: string }>();
  const code = params?.code ?? "";
  const [view, setView] = useState<PublicView | null>(null);
  const [failed, setFailed] = useState("");

  useEffect(() => {
    if (!code) return;
    fetch(`/api/public/links/${encodeURIComponent(code)}`)
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        if (data && typeof data.state === "string") {
          setView(data as PublicView);
        } else {
          setFailed("活动不存在");
        }
      })
      .catch(() => setFailed("活动不存在"));
  }, [code]);

  const state = failed ? "not_found" : view?.state ?? "loading";
  const headline = state === "loading" ? "加载中…" : STATE_TEXT[state] ?? "活动不存在";

  return (
    <main style={{ maxWidth: 520, margin: "80px auto", padding: "0 20px", textAlign: "center" }}>
      <div style={{ background: "#fff", borderRadius: 12, padding: 32, border: "1px solid #e5e7eb" }}>
        <div style={{ fontSize: 40, marginBottom: 12 }}>{state === "available" ? "🎪" : "🔗"}</div>
        <h1 style={{ margin: "0 0 8px" }}>{state === "available" && view?.title ? view.title : headline}</h1>
        {state !== "available" && <p style={{ color: "#6b7280" }}>{headline}</p>}
        {state === "available" && (
          <>
            {view?.public_content && <p style={{ fontSize: 16 }}>{view.public_content}</p>}
            {(view?.starts_at || view?.ends_at) && (
              <p style={{ color: "#6b7280", fontSize: 14 }}>
                活动时间:{view?.starts_at || "即日起"} ~ {view?.ends_at || "长期"}
              </p>
            )}
          </>
        )}
      </div>
    </main>
  );
}
