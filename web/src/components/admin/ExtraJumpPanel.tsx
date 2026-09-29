"use client";

import { useCallback, useEffect, useState } from "react";
import {
  draftsFromMerchant,
  emptyJumpDraft,
  merchantJumpKinds,
  putJumpActions,
  saveJumpError,
  unauthorizedConfigCopy,
  type JumpDraft,
} from "@/lib/jump-matrix";

const KIND_LABEL: Record<string, string> = {
  wifi: "门店 WiFi",
  navigate: "导航",
  review: "写点评",
  follow: "关注账号",
};

const inputStyle: React.CSSProperties = { padding: "8px 10px", border: "1px solid #d1d5db", borderRadius: 6, flex: "1 1 160px" };
const btnStyle: React.CSSProperties = { padding: "8px 14px", borderRadius: 6, border: "1px solid #2563eb", background: "#2563eb", color: "#fff", cursor: "pointer" };
const sectionStyle: React.CSSProperties = { background: "#fff", borderRadius: 8, padding: 16, marginTop: 20, border: "1px solid #e5e7eb" };

export function ExtraJumpPanel(props: {
  campaigns: Array<{ id: string; title: string }>;
  role: string;
  tenantId: string;
}) {
  const owner = props.role === "org_owner";
  const [campaignId, setCampaignId] = useState("");
  const [actions, setActions] = useState<Record<string, JumpDraft>>(() => draftsFromMerchant(null).actions);
  const [returnDraft, setReturnDraft] = useState<JumpDraft>(emptyJumpDraft());
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [saving, setSaving] = useState(false);
  const [preserved, setPreserved] = useState<Array<{ kind?: string; href?: string; enabled?: boolean; revoked?: boolean; expires_at?: string }>>([]);

  const load = useCallback(async (id: string) => {
    if (!id || !owner) return;
    const res = await fetch(`/api/campaigns/${encodeURIComponent(id)}/extra-jumps`, {
      headers: { "x-tenant-id": props.tenantId },
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      setError(res.status === 403 ? unauthorizedConfigCopy() : "没有读到已保存的跳转");
      return;
    }
    const drafts = draftsFromMerchant(data);
    setActions(drafts.actions);
    setReturnDraft(drafts.returnDraft);
    setPreserved(Array.isArray(data.configured) ? data.configured : []);
    setError("");
  }, [owner, props.tenantId]);

  useEffect(() => {
    if (!campaignId) return;
    void load(campaignId);
  }, [campaignId, load]);

  const patchAction = (kind: string, patch: Partial<JumpDraft>) => {
    setActions((prev) => ({ ...prev, [kind]: { ...prev[kind], ...patch } }));
  };

  const save = async () => {
    if (!owner || !campaignId) return;
    setSaving(true);
    setError("");
    setNote("");
    const res = await fetch(`/api/campaigns/${encodeURIComponent(campaignId)}/extra-jumps`, {
      method: "PUT",
      headers: { "content-type": "application/json", "x-tenant-id": props.tenantId },
      body: JSON.stringify({
        actions: putJumpActions(actions, preserved),
        return: returnDraft,
      }),
    });
    const data = await res.json().catch(() => ({}));
    setSaving(false);
    if (!res.ok) {
      const code = typeof data.error === "string" ? data.error : "";
      setError(saveJumpError(code === "forbidden" || res.status === 403 ? "forbidden" : code));
      return;
    }
    setNote("已保存。这只记下地址，不是关注、加群或付费成功。原生唤起未验证。");
    await load(campaignId);
  };

  return (
    <section style={sectionStyle} data-testid="extra-jump-panel">
      <h2>跳转</h2>
      <p style={{ color: "#6b7280", fontSize: 14 }}>
        只保存店内和活动的正式地址，以及已登记的返回地址。加企微仍在私域入口配置。原生唤起未验证。
      </p>
      {!owner && <p data-testid="extra-jump-unauthorized">{unauthorizedConfigCopy()}</p>}
      <label style={{ display: "block", marginBottom: 8 }}>
        活动
        <select
          value={campaignId}
          onChange={(e) => setCampaignId(e.target.value)}
          style={{ ...inputStyle, display: "block", marginTop: 4, width: "100%" }}
          disabled={!owner}
        >
          <option value="">选择活动</option>
          {props.campaigns.map((item) => (
            <option key={item.id} value={item.id}>{item.title}</option>
          ))}
        </select>
      </label>
      {merchantJumpKinds().map((kind) => (
        <div key={kind} style={{ display: "flex", gap: 8, flexWrap: "wrap", marginBottom: 8, alignItems: "center" }}>
          <strong style={{ flex: "0 0 88px" }}>{KIND_LABEL[kind]}</strong>
          <label>
            <input
              type="checkbox"
              checked={actions[kind]?.enabled === true}
              disabled={!owner}
              onChange={(e) => patchAction(kind, { enabled: e.target.checked })}
            />
            启用
          </label>
          <input
            placeholder="https 正式地址"
            value={actions[kind]?.href ?? ""}
            disabled={!owner}
            data-testid={kind === "wifi" ? "extra-jump-wifi-href" : undefined}
            onChange={(e) => patchAction(kind, { href: e.target.value })}
            style={inputStyle}
          />
          <label>
            <input
              type="checkbox"
              checked={actions[kind]?.revoked === true}
              disabled={!owner}
              onChange={(e) => patchAction(kind, { revoked: e.target.checked })}
            />
            撤销
          </label>
          <input
            placeholder="失效时间 RFC3339，可空"
            value={actions[kind]?.expires_at ?? ""}
            disabled={!owner}
            onChange={(e) => patchAction(kind, { expires_at: e.target.value })}
            style={inputStyle}
          />
        </div>
      ))}
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap", marginBottom: 8, alignItems: "center" }}>
        <strong style={{ flex: "0 0 88px" }}>返回</strong>
        <label>
          <input
            type="checkbox"
            checked={returnDraft.enabled}
            disabled={!owner}
            onChange={(e) => setReturnDraft({ ...returnDraft, enabled: e.target.checked })}
          />
          启用
        </label>
        <input
          placeholder="站内路径或已登记的 https 地址"
          value={returnDraft.href}
          disabled={!owner}
          data-testid="extra-jump-return-href"
          onChange={(e) => setReturnDraft({ ...returnDraft, href: e.target.value })}
          style={inputStyle}
        />
        <label>
          <input
            type="checkbox"
            checked={returnDraft.revoked}
            disabled={!owner}
            onChange={(e) => setReturnDraft({ ...returnDraft, revoked: e.target.checked })}
          />
          撤销
        </label>
        <input
          placeholder="失效时间 RFC3339，可空"
          value={returnDraft.expires_at}
          disabled={!owner}
          onChange={(e) => setReturnDraft({ ...returnDraft, expires_at: e.target.value })}
          style={inputStyle}
        />
      </div>
      {error && <p style={{ color: "#b91c1c" }}>{error}</p>}
      {note && <p style={{ color: "#374151" }}>{note}</p>}
      <button type="button" data-testid="extra-jump-save" style={btnStyle} disabled={!owner || !campaignId || saving} onClick={() => void save()}>
        {saving ? "保存中…" : "保存跳转"}
      </button>
    </section>
  );
}
