"use client";

// 公共活动页(路由区 /c/[code]):游客只读 + HUI-1747 授权留资(唯一游客写面)
// + HUI-1664 二维码兜底入口语义。
// 数据仅来自公共白名单 API;本页无会话、无后台信息、不暴露内部 id。
// 纪律:
//   - 与 NFC 共用 T0 五态解析(store.ResolveLink);不建第二套活动模型;
//   - 非有效态(停用/未开始/过期/暂停/不存在)分别给出明确文案与可恢复动作;
//     网络失败给「重试」;入口参数非白名单给「入口不支持」指引;
//   - 任何 next/redirect/return 参数只放行站内相对路径(safe-redirect 守卫),
//     非法一律忽略,默认落地本活动页;URL 参数绝不派生任何后台能力;
//   - 留资表单 = 固定最小集(姓名/手机号/可选微信号)+ 告知确认 + 后续营销
//     **独立勾选**;成功页保留 submission_ref 与撤销渠道文案;
//   - 加载即发匿名浏览事件(纯聚合计数,与线索池隔离);
//   - 联系方式只经 POST body 进入表单 API,绝不进 URL/日志。

import { Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { classifyEntry, failureState, STATE_ACTION, STATE_TEXT, type PublicUiState } from "@/lib/public-state";
import { inSiteTargetFromParams } from "@/lib/safe-redirect";

interface PublicView {
  state: string;
  title?: string;
  public_content?: string;
  starts_at?: string;
  ends_at?: string;
}

interface LeadFormView {
  enabled: boolean;
  fields?: string[];
  notice?: { version: string; text: string };
  marketing_optin_enabled?: boolean;
}

export default function PublicCampaignPage() {
  // Next 15:useSearchParams 需在 Suspense 边界内预渲染
  return (
    <Suspense fallback={null}>
      <PublicCampaignInner />
    </Suspense>
  );
}

function PublicCampaignInner() {
  const params = useParams<{ code: string }>();
  const code = params?.code ?? "";
  const searchParams = useSearchParams();

  // 入口标记(可选元数据):显式非白名单值 → 入口不支持(不发请求)
  const entry = classifyEntry(searchParams.get("entry"));
  // 受控跳转:仅站内相对路径;非法(null)→ 默认落地本活动页,不渲染任何跳转
  const inSiteTarget = inSiteTargetFromParams((k) => searchParams.get(k));

  const [view, setView] = useState<PublicView | null>(null);
  const [leadForm, setLeadForm] = useState<LeadFormView | null>(null);
  const [uiState, setUiState] = useState<PublicUiState>("loading");
  const [reloadTick, setReloadTick] = useState(0);

  // 留资表单状态
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [wechat, setWechat] = useState("");
  const [consent, setConsent] = useState(false);
  const [marketing, setMarketing] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState("");
  const [submittedRef, setSubmittedRef] = useState("");

  // 撤销状态
  const [revoking, setRevoking] = useState(false);
  const [revokeDone, setRevokeDone] = useState(false);
  const [revokeError, setRevokeError] = useState("");

  // 匿名浏览事件:一次加载只发一次,纯聚合计数
  const viewSent = useRef(false);

  useEffect(() => {
    if (!code || entry === "unsupported") return;
    let alive = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}`)
      .then(async (res) => {
        if (!alive) return;
        // BFF/上游不可达:网络失败兜底(重试动作),不冒充 not_found
        if (res.status === 502 || res.status === 503 || res.status === 504) {
          setUiState(failureState(res.status));
          return;
        }
        const data = await res.json().catch(() => null);
        if (data && typeof data.state === "string") {
          setView(data as PublicView);
          setUiState(data.state as PublicUiState);
        } else {
          setUiState("not_found");
        }
      })
      .catch(() => {
        if (alive) setUiState(failureState(null));
      });
    return () => {
      alive = false;
    };
  }, [code, entry, reloadTick]);

  // 浏览埋点 + 留资表单描述(仅 available 态;feature off 时端点 404,静默跳过)
  useEffect(() => {
    if (!code || uiState !== "available" || viewSent.current) return;
    viewSent.current = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}/view-events`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ channel: "web" }),
    }).catch(() => undefined);
    fetch(`/api/public/links/${encodeURIComponent(code)}/lead-form`)
      .then(async (res) => {
        if (!res.ok) return null;
        const data = await res.json().catch(() => null);
        if (data && data.enabled) setLeadForm(data as LeadFormView);
      })
      .catch(() => undefined);
  }, [code, uiState]);

  const retry = useCallback(() => {
    setUiState("loading");
    setReloadTick((t) => t + 1);
  }, []);

  const submitLead = useCallback(() => {
    if (!code || !leadForm?.notice) return;
    setSubmitError("");
    if (!consent) {
      setSubmitError("请先阅读并同意告知内容");
      return;
    }
    setSubmitting(true);
    fetch(`/api/public/links/${encodeURIComponent(code)}/lead-submissions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name,
        phone,
        wechat,
        consent: true,
        consent_version: leadForm.notice.version,
        marketing_optin: marketing,
        channel: "web",
      }),
    })
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        if (res.ok && data?.submission_ref) {
          setSubmittedRef(data.submission_ref as string);
        } else {
          setSubmitError(errorMessage(data) ?? "提交失败,请稍后再试");
        }
      })
      .catch(() => setSubmitError("网络异常,请稍后再试"))
      .finally(() => setSubmitting(false));
  }, [code, consent, leadForm, marketing, name, phone, wechat]);

  const revokeLead = useCallback(() => {
    if (!code || !submittedRef) return;
    setRevokeError("");
    setRevoking(true);
    fetch(`/api/public/links/${encodeURIComponent(code)}/lead-revocations`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ submission_ref: submittedRef, phone }),
    })
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        if (res.ok && data?.state === "revoked") {
          setRevokeDone(true);
        } else {
          setRevokeError(errorMessage(data) ?? "撤销失败,请核对手机号后重试");
        }
      })
      .catch(() => setRevokeError("网络异常,请稍后再试"))
      .finally(() => setRevoking(false));
  }, [code, phone, submittedRef]);

  const state: PublicUiState = entry === "unsupported" ? "entry_unsupported" : uiState;
  const headline = STATE_TEXT[state] ?? "活动不存在";
  const action = STATE_ACTION[state] ?? "";

  return (
    <main style={{ maxWidth: 520, margin: "80px auto", padding: "0 20px", textAlign: "center" }}>
      <div style={{ background: "#fff", borderRadius: 12, padding: 32, border: "1px solid #e5e7eb" }}>
        <div style={{ fontSize: 40, marginBottom: 12 }}>{state === "available" ? "🎪" : "🔗"}</div>
        <h1 style={{ margin: "0 0 8px" }}>{state === "available" && view?.title ? view.title : headline}</h1>
        {state !== "available" && (
          <p style={{ color: "#6b7280" }}>{headline}</p>
        )}
        {state !== "available" && action && <p style={{ color: "#6b7280", fontSize: 14 }}>{action}</p>}
        {state === "network_error" && (
          <button
            onClick={retry}
            style={{ marginTop: 12, padding: "8px 22px", borderRadius: 6, border: "1px solid #2563eb", background: "#fff", color: "#2563eb", cursor: "pointer" }}
          >
            重试
          </button>
        )}
        {inSiteTarget && state !== "available" && (
          <p style={{ marginTop: 12 }}>
            <a href={inSiteTarget} style={{ color: "#2563eb", fontSize: 14 }}>返回</a>
          </p>
        )}
        {state === "available" && (
          <>
            {view?.public_content && <p style={{ fontSize: 16 }}>{view.public_content}</p>}
            {(view?.starts_at || view?.ends_at) && (
              <p style={{ color: "#6b7280", fontSize: 14 }}>
                活动时间:{view?.starts_at || "即日起"} ~ {view?.ends_at || "长期"}
              </p>
            )}

            {submittedRef ? (
              // 留资成功页:保留引用号与可见的撤销渠道
              <div style={{ marginTop: 20, textAlign: "left", background: "#f9fafb", borderRadius: 8, padding: 16 }}>
                {revokeDone ? (
                  <p style={{ margin: 0, color: "#065f46" }}>
                    已撤销:商家将停止把你作为营销线索使用,未同步的数据已阻止同步。
                  </p>
                ) : (
                  <>
                    <p style={{ margin: "0 0 6px" }}>提交成功。你的留资编号:<code style={{ fontSize: 13 }}>{submittedRef}</code></p>
                    <p style={{ margin: "0 0 6px", color: "#6b7280", fontSize: 13 }}>
                      如需撤销授权,可随时在本页操作;已同步给商家的信息,我们将向商家发送停止营销通知。
                    </p>
                    {revokeError && <p style={{ margin: "0 0 6px", color: "#b91c1c", fontSize: 13 }}>{revokeError}</p>}
                    <button
                      onClick={revokeLead}
                      disabled={revoking}
                      style={{ padding: "8px 14px", borderRadius: 6, border: "1px solid #d1d5db", background: "#fff", cursor: "pointer" }}
                    >
                      {revoking ? "撤销中…" : "撤销我的授权"}
                    </button>
                  </>
                )}
              </div>
            ) : leadForm?.enabled ? (
              // 固定轻表单:最小字段 + 告知 + 营销独立勾选
              <div style={{ marginTop: 20, textAlign: "left" }}>
                <label style={labelStyle}>
                  姓名
                  <input style={inputStyle} value={name} onChange={(e) => setName(e.target.value)} placeholder="怎么称呼你" />
                </label>
                <label style={labelStyle}>
                  手机号
                  <input style={inputStyle} value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="用于商家联系你" inputMode="numeric" />
                </label>
                <label style={labelStyle}>
                  微信号(选填)
                  <input style={inputStyle} value={wechat} onChange={(e) => setWechat(e.target.value)} placeholder="可选" />
                </label>
                <p style={{ color: "#6b7280", fontSize: 13, margin: "10px 0" }}>{leadForm.notice?.text}</p>
                <label style={{ display: "block", fontSize: 14, marginBottom: 8 }}>
                  <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} /> 我已阅读并同意上述内容
                </label>
                {leadForm.marketing_optin_enabled && (
                  <label style={{ display: "block", fontSize: 14, marginBottom: 12 }}>
                    <input type="checkbox" checked={marketing} onChange={(e) => setMarketing(e.target.checked)} />{" "}
                    同意商家后续向我发送营销信息(可单独撤销,不影响本次服务)
                  </label>
                )}
                {submitError && <p style={{ color: "#b91c1c", fontSize: 13 }}>{submitError}</p>}
                <button
                  onClick={submitLead}
                  disabled={submitting}
                  style={{ width: "100%", padding: "10px 0", borderRadius: 8, border: "none", background: "#2563eb", color: "#fff", fontSize: 16, cursor: "pointer" }}
                >
                  {submitting ? "提交中…" : "提交"}
                </button>
              </div>
            ) : null}
          </>
        )}
      </div>
    </main>
  );
}

const labelStyle = { display: "block", fontSize: 14, marginBottom: 10 } as const;
const inputStyle = {
  display: "block",
  width: "100%",
  marginTop: 4,
  padding: "8px 10px",
  borderRadius: 6,
  border: "1px solid #d1d5db",
  fontSize: 15,
  boxSizing: "border-box",
} as const;

function errorMessage(data: unknown): string | null {
  if (data && typeof data === "object" && "message" in (data as Record<string, unknown>)) {
    const m = (data as Record<string, unknown>).message;
    if (typeof m === "string") return m;
  }
  return null;
}
