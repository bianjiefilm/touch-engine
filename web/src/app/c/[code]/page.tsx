"use client";

// 公共活动页(路由区 /c/[code]):游客看到的是这家店的活动，不是平台后台。
// 顺序:门店 → 能得到什么 → 可做的动作 → 单独的留资同意 → 提交后的下一步。
// 碰一下、扫码、看视频不会变成留资。留资必须勾选本次告知;营销许可可拒绝。

import { Suspense, useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { classifyEntry, failureState, STATE_ACTION, STATE_TEXT, storeNoticeText, type PublicUiState } from "@/lib/public-state";
import { inSiteTargetFromParams } from "@/lib/safe-redirect";
import {
  actionClickRecord,
  activityBlocks,
  beaconChannel,
  guestActionLabel,
  guestActionsFromPayload,
  isOfficialActionUrl,
  leadDisclosureReady,
  leadOutcomeCopy,
  presentGuestActions,
  publicSectionOrder,
  settleClick,
  type GuestCapability,
  type PublicSection,
} from "@/lib/visitor-experience";
import {
  activityJumpActions,
  presentPrivateDomain,
  privateDomainClick,
  privateDomainLabel,
  privateDomainNote,
  settlePrivateDomainClick,
  type PrivateDomainGuide,
} from "@/lib/private-domain";
import { CustomerPublish } from "./customer-publish";

interface PublicView {
  state: string;
  title?: string;
  public_content?: string;
  starts_at?: string;
  ends_at?: string;
  store_notice?: string;
  merchant_name?: string;
  store_name?: string;
  brand_shell?: { display_name?: string; support_name?: string; support_contact?: string };
}

interface LeadFormView {
  enabled: boolean;
  required_fields?: string[];
  purpose?: string;
  recipient?: { name?: string };
  notice?: { version: string; text: string };
  marketing_optin_enabled?: boolean;
  marketing_blocks_browse?: boolean;
}

interface LeadSubmitResult {
  submission_ref?: string;
  state?: string;
  duplicate?: boolean;
  crm_received?: boolean;
  submitted_to?: { name?: string };
}

export default function PublicCampaignPage() {
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
  const entry = classifyEntry(searchParams.get("entry"));
  const source = beaconChannel(entry, searchParams.get("tenant_id") ?? searchParams.get("tenant"));
  const inSiteTarget = inSiteTargetFromParams((k) => searchParams.get(k));

  const [view, setView] = useState<PublicView | null>(null);
  const [leadForm, setLeadForm] = useState<LeadFormView | null>(null);
  const [uiState, setUiState] = useState<PublicUiState>("loading");
  const [reloadTick, setReloadTick] = useState(0);

  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [wechat, setWechat] = useState("");
  const [consent, setConsent] = useState(false);
  const [marketing, setMarketing] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState("");
  const [submitted, setSubmitted] = useState<LeadSubmitResult | null>(null);
  const [actionNote, setActionNote] = useState("");
  const [privateNote, setPrivateNote] = useState("");
  const [guestActions, setGuestActions] = useState<GuestCapability[]>([]);
  const [privateDomain, setPrivateDomain] = useState<PrivateDomainGuide>({
    entries: [],
    connected: false,
    redemption: "unknown",
  });

  const [revoking, setRevoking] = useState(false);
  const [revokeDone, setRevokeDone] = useState(false);
  const [revokeError, setRevokeError] = useState("");
  const viewSent = useRef(false);

  useEffect(() => {
    if (!code || entry === "unsupported") return;
    let alive = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}`)
      .then(async (res) => {
        if (!alive) return;
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

  useEffect(() => {
    if (!code || uiState !== "available" || viewSent.current) return;
    viewSent.current = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}/view-events`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ channel: source.channel }),
    }).catch(() => undefined);
    fetch(`/api/public/links/${encodeURIComponent(code)}/lead-form`)
      .then(async (res) => {
        if (!res.ok) return null;
        const data = await res.json().catch(() => null);
        if (data && data.enabled) setLeadForm(data as LeadFormView);
      })
      .catch(() => undefined);
  }, [code, source.channel, uiState]);

  useEffect(() => {
    if (!code || uiState !== "available") return;
    let alive = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}/extra-jumps`)
      .then(async (res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (alive) setGuestActions(guestActionsFromPayload(data));
      })
      .catch(() => {
        if (alive) setGuestActions([]);
      });
    fetch(`/api/public/links/${encodeURIComponent(code)}/private-domain?channel=${encodeURIComponent(source.channel)}`)
      .then(async (res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (alive) setPrivateDomain(presentPrivateDomain(data, source.channel));
      })
      .catch(() => {
        if (alive) setPrivateDomain(presentPrivateDomain(null, source.channel));
      });
    return () => {
      alive = false;
    };
  }, [code, source.channel, uiState]);

  const retry = useCallback(() => {
    viewSent.current = false;
    setUiState("loading");
    setReloadTick((t) => t + 1);
  }, []);

  const disclosure = {
    merchant: leadForm?.recipient?.name || view?.merchant_name || "",
    purpose: leadForm?.purpose || "",
    requiredFields: leadForm?.required_fields ?? [],
    noticeVersion: leadForm?.notice?.version || "",
  };
  const disclosureReady = Boolean(leadForm?.enabled) && leadDisclosureReady(disclosure);
  const leadBlocked = activityBlocks({ marketingOptIn: marketing, consent: consent }).includes("lead");

  const submitLead = useCallback(() => {
    if (!code || !disclosureReady || !leadForm?.notice) return;
    setSubmitError("");
    if (leadBlocked) {
      setSubmitError("请先阅读并同意告知内容。拒绝营销通知不会挡住看活动。");
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
        channel: source.channel === "web" ? "web" : source.channel,
      }),
    })
      .then(async (res) => {
        const data = (await res.json().catch(() => null)) as LeadSubmitResult | null;
        if (res.ok && data?.submission_ref) {
          setSubmitted(data);
        } else {
          setSubmitError(errorMessage(data) ?? "提交失败,请稍后再试");
        }
      })
      .catch(() => setSubmitError("网络异常,请稍后再试"))
      .finally(() => setSubmitting(false));
  }, [code, disclosureReady, leadBlocked, leadForm, marketing, name, phone, source.channel, wechat]);

  const revokeLead = useCallback(() => {
    if (!code || !submitted?.submission_ref) return;
    setRevokeError("");
    setRevoking(true);
    fetch(`/api/public/links/${encodeURIComponent(code)}/lead-revocations`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ submission_ref: submitted.submission_ref, phone }),
    })
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        if (res.ok && data?.state === "revoked") setRevokeDone(true);
        else setRevokeError(errorMessage(data) ?? "撤销失败,请核对手机号后重试");
      })
      .catch(() => setRevokeError("网络异常,请稍后再试"))
      .finally(() => setRevoking(false));
  }, [code, phone, submitted]);

  const onPrivateDomain = (kind: string, href: string) => {
    const record = privateDomainClick(kind);
    const entry = privateDomain.entries.find((item) => item.kind === kind && item.href === href);
    if (!entry || record.success || record.platformResult !== "unknown" || record.redemption !== "unknown") {
      setPrivateNote("没有打开。这一下也不是添加成功、进群成功、新增联系人或核销。");
      return;
    }
    setPrivateNote("正在打开。这一下只是点击，还不是添加成功。");
    const params = new URLSearchParams({ channel: source.channel });
    fetch(`/api/public/links/${encodeURIComponent(code)}/private-domain/${encodeURIComponent(kind)}/clicks?${params.toString()}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    })
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        const settled = settlePrivateDomainClick(res.ok ? data : null);
        if (!settled.open || settled.event !== entry.event) {
          setPrivateNote("平台结果还是未知。没有把它当成添加成功、进群成功、新增联系人或核销。");
          return;
        }
        setPrivateNote(privateDomainNote(privateDomain, settled));
        window.open(href, "_blank", "noopener,noreferrer");
      })
      .catch(() => {
        setPrivateNote("没有打开。这一下也不是添加成功、进群成功、新增联系人或核销。");
      });
  };

  const onGuestAction = (kind: string, href: string) => {
    const record = actionClickRecord(kind);
    if (!isOfficialActionUrl(href) || record.success || record.platformResult !== "unknown") {
      setActionNote("这个动作没有真实地址，没有打开，也没有记成成功。");
      return;
    }
    setActionNote("正在打开。这一下还不是添加成功、关注成功，也不是留资。");
    fetch(`/api/public/links/${encodeURIComponent(code)}/extra-jumps/${encodeURIComponent(kind)}/clicks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    })
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        const settled = settleClick(res.ok ? data : null);
        if (!settled.open) {
          setActionNote("平台结果还是未知。没有把它当成添加成功、关注成功或留资。");
          return;
        }
        setActionNote("已打开链接。平台还没有确认添加、关注或留资。");
        window.open(href, "_blank", "noopener,noreferrer");
      })
      .catch(() => {
        setActionNote("没有打开。这一下也不是添加成功、关注成功或留资。");
      });
  };

  const state: PublicUiState = entry === "unsupported" ? "entry_unsupported" : uiState;
  const headline = STATE_TEXT[state] ?? "活动不存在";
  const action = STATE_ACTION[state] ?? "";
  const showActivity = state === "available" && Boolean(view?.title);
  const visibleActions = activityJumpActions(presentGuestActions(guestActions));
  const merchantLabel = view?.merchant_name || "这家店";
  const outcome = submitted
    ? leadOutcomeCopy({
        merchant: submitted.submitted_to?.name || disclosure.merchant || merchantLabel,
        state: submitted.duplicate ? "duplicate" : submitted.state || "accepted",
        duplicate: submitted.duplicate,
        crmReceived: submitted.crm_received,
      })
    : "";

  const sectionNodes: Record<PublicSection, ReactElement | null> = {
    store: (
      <header key="store" data-testid="store-identity">
        <p style={eyebrow}>{merchantLabel}</p>
        {view?.store_name && <p style={storeLine}>{view.store_name}</p>}
        <h1 style={titleStyle}>{showActivity && view?.title ? view.title : headline}</h1>
      </header>
    ),
    value: showActivity ? (
      <section key="value" data-testid="store-value">
        <p style={valueStyle}>{view?.public_content || "请按门店现场说明参与这次活动。"}</p>
        <p style={noteStyle}>碰一下、扫码或看视频只是打开这个活动，不会自动留下联系方式。</p>
        {(view?.starts_at || view?.ends_at) && (
          <p style={noteStyle}>
            活动时间:{view?.starts_at || "即日起"} ~ {view?.ends_at || "长期"}
          </p>
        )}
        {storeNoticeText(view?.store_notice) && (
          <p style={warnStyle}>{storeNoticeText(view?.store_notice)}</p>
        )}
      </section>
    ) : null,
    actions: showActivity ? (
      <section key="actions" data-testid="activity-actions">
        <CustomerPublish code={code} defaultCopy={view?.public_content ?? ""} />
        {visibleActions.length > 0 && (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 12 }}>
            {visibleActions.map((item) => (
              <button key={item.kind} type="button" style={quietButton} data-testid={`extra-jump-${item.kind}`} onClick={() => onGuestAction(item.kind, item.href)}>
                {guestActionLabel(item.kind)}
              </button>
            ))}
          </div>
        )}
        {actionNote && <p style={noteStyle}>{actionNote}</p>}
        {privateDomain.entries.length > 0 && (
          <div data-testid="private-domain" style={{ marginTop: 16 }}>
            <h2 style={sectionTitle}>加企微或进社群</h2>
            <p style={noteStyle}>点一下才会打开。拒绝留资也可以继续看活动。这里不会自动加好友，也不会自动进群。</p>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
              {privateDomain.entries.map((item) => (
                <button
                  key={item.kind}
                  type="button"
                  style={quietButton}
                  data-testid={`private-domain-${item.kind}`}
                  onClick={() => onPrivateDomain(item.kind, item.href)}
                >
                  {privateDomainLabel(item.kind)}
                </button>
              ))}
            </div>
            {privateNote && <p style={noteStyle}>{privateNote}</p>}
          </div>
        )}
      </section>
    ) : null,
    lead: showActivity && disclosureReady && !submitted ? (
      <section key="lead" data-testid="lead-disclosure" style={{ marginTop: 8 }}>
        <h2 style={sectionTitle}>把联系方式交给{disclosure.merchant}</h2>
        <p style={noteStyle}>{disclosure.purpose}</p>
        <p style={noteStyle}>
          接收方:{disclosure.merchant}。必填:{fieldLabels(disclosure.requiredFields)}。告知版本 {disclosure.noticeVersion}。
        </p>
        <p style={noteStyle}>{leadForm?.notice?.text}</p>
        <label style={labelStyle}>
          姓名
          <input style={inputStyle} value={name} onChange={(e) => setName(e.target.value)} placeholder="怎么称呼你" />
        </label>
        <label style={labelStyle}>
          手机号
          <input style={inputStyle} value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="商家用来联系你" inputMode="numeric" />
        </label>
        <label style={labelStyle}>
          微信号(选填)
          <input style={inputStyle} value={wechat} onChange={(e) => setWechat(e.target.value)} placeholder="可选" />
        </label>
        <label style={checkStyle}>
          <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} data-testid="consent" />
          我同意把上面的联系方式交给{disclosure.merchant}，用于这次告知里的用途
        </label>
        {leadForm?.marketing_optin_enabled && (
          <label style={checkStyle}>
            <input type="checkbox" checked={marketing} onChange={(e) => setMarketing(e.target.checked)} data-testid="marketing-optin" />
            另外同意以后接收活动通知。不勾选也可以继续看活动，也可以只提交这次联系方式
          </label>
        )}
        {submitError && <p style={{ color: "#b91c1c", fontSize: 13 }}>{submitError}</p>}
        <button type="button" onClick={submitLead} disabled={submitting || leadBlocked} data-testid="lead-submit" style={primaryButton}>
          {submitting ? "提交中…" : "提交给这家店"}
        </button>
      </section>
    ) : null,
    next: submitted ? (
      <section key="next" data-testid="lead-outcome" style={outcomeBox}>
        {revokeDone ? (
          <p style={{ margin: 0, color: "#065f46" }}>已撤销。未同步的数据会停在这里，不再继续交给商家的客户系统。</p>
        ) : (
          <>
            <p style={{ margin: "0 0 8px" }}>{outcome}</p>
            <p style={noteStyle}>留资编号 {submitted.submission_ref}。可以在本页撤销。</p>
            {revokeError && <p style={{ margin: "0 0 6px", color: "#b91c1c", fontSize: 13 }}>{revokeError}</p>}
            <button type="button" onClick={revokeLead} disabled={revoking} style={quietButton}>
              {revoking ? "撤销中…" : "撤销我的授权"}
            </button>
          </>
        )}
      </section>
    ) : null,
  };

  return (
    <main data-testid="public-activity" style={pageStyle}>
      <div style={cardStyle}>
        {state !== "available" && (
          <div data-testid="degraded-state">
            <h1 style={titleStyle}>{headline}</h1>
            {action && <p style={noteStyle}>{action}</p>}
            {state === "network_error" && (
              <button type="button" onClick={retry} style={quietButton}>
                重试
              </button>
            )}
            {inSiteTarget && (
              <p style={{ marginTop: 12 }}>
                <a href={inSiteTarget}>返回</a>
              </p>
            )}
          </div>
        )}
        {state === "available" && publicSectionOrder().map((section) => sectionNodes[section])}
        {view?.brand_shell?.display_name && (
          <footer style={footerStyle}>
            技术服务 {view.brand_shell.display_name}
            {view.brand_shell.support_name ? ` · ${view.brand_shell.support_name}` : ""}
            {view.brand_shell.support_contact ? ` · ${view.brand_shell.support_contact}` : ""}
          </footer>
        )}
      </div>
    </main>
  );
}

function fieldLabels(fields: string[]): string {
  return fields
    .map((field) => (field === "name" ? "姓名" : field === "phone" ? "手机号" : field))
    .join("、");
}

function errorMessage(data: unknown): string | null {
  if (data && typeof data === "object" && "message" in (data as Record<string, unknown>)) {
    const message = (data as Record<string, unknown>).message;
    if (typeof message === "string") return message;
  }
  return null;
}

const pageStyle = { maxWidth: 480, margin: "0 auto", padding: "16px 16px 40px" } as const;
const cardStyle = { background: "#fff", borderRadius: 12, padding: 20, border: "1px solid #e5e7eb" } as const;
const eyebrow = { margin: "0 0 4px", color: "#6b7280", fontSize: 13 } as const;
const storeLine = { margin: "0 0 8px", fontSize: 15, fontWeight: 600 } as const;
const titleStyle = { margin: "0 0 8px", fontSize: 28, lineHeight: 1.25 } as const;
const valueStyle = { fontSize: 18, lineHeight: 1.5, margin: "12px 0 8px" } as const;
const noteStyle = { color: "#6b7280", fontSize: 14, lineHeight: 1.5, margin: "8px 0" } as const;
const warnStyle = { color: "#b45309", fontSize: 14, background: "#fef3c7", borderRadius: 8, padding: "8px 12px", marginTop: 10 } as const;
const sectionTitle = { fontSize: 18, margin: "20px 0 8px" } as const;
const labelStyle = { display: "block", fontSize: 14, marginBottom: 10 } as const;
const checkStyle = { display: "flex", gap: 8, alignItems: "flex-start", fontSize: 14, margin: "10px 0" } as const;
const inputStyle = {
  display: "block",
  width: "100%",
  marginTop: 4,
  padding: "10px 12px",
  borderRadius: 8,
  border: "1px solid #d1d5db",
  fontSize: 16,
  boxSizing: "border-box",
} as const;
const primaryButton = {
  width: "100%",
  marginTop: 8,
  padding: "12px 0",
  borderRadius: 8,
  border: "none",
  background: "#111827",
  color: "#fff",
  fontSize: 16,
  cursor: "pointer",
} as const;
const quietButton = {
  padding: "8px 14px",
  borderRadius: 8,
  border: "1px solid #d1d5db",
  background: "#fff",
  cursor: "pointer",
} as const;
const outcomeBox = { marginTop: 16, textAlign: "left" as const, background: "#f9fafb", borderRadius: 8, padding: 16 };
const footerStyle = { marginTop: 28, paddingTop: 16, borderTop: "1px solid #e5e7eb", color: "#6b7280", fontSize: 12 } as const;
