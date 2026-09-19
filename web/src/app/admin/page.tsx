"use client";

// 商家后台(路由区 /admin):全部数据经 BFF(/api/*)转发到 Go 服务,
// 由服务端做平台会话解析 + 租户成员校验。本页面不持有任何凭证。

import { useCallback, useEffect, useState } from "react";

interface Campaign {
  id: string;
  title: string;
  public_content: string;
  status: string;
  starts_at: string;
  ends_at: string;
  order_ref?: string;
}

interface StoreRec {
  id: string;
  name: string;
  address: string;
}

interface LinkRec {
  id: string;
  code: string;
  enabled: boolean;
}

interface QrMeta {
  url: string;
  code: string;
  size: number;
}

// HUI-1665 NFC 标签管理(管理面,owner-only;服务端裁决,staff 见 403 文案)
interface TagGroup {
  id: string;
  name: string;
}

interface TagView {
  id: string;
  label: string;
  code: string;
  link_id: string;
  campaign_id: string;
  status: "active" | "disabled";
  uid_hint?: string;
  store_name?: string;
  group_name?: string;
}

const QR_SIZES = [128, 256, 512] as const;

interface AssetRec {
  id: string;
  asset_id: string;
  version: string;
}

const TENANT_KEY = "touch_admin_tenant";

export default function AdminPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [tenantId, setTenantId] = useState("");
  const [role, setRole] = useState("");
  const [email_, setEmailMasked] = useState("");
  const [error, setError] = useState("");
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [stores, setStores] = useState<StoreRec[]>([]);

  const [newStore, setNewStore] = useState({ name: "", address: "" });
  const [newCampaign, setNewCampaign] = useState({ title: "", public_content: "", starts_at: "", ends_at: "", store_id: "", order_ref: "" });
  const [newAsset, setNewAsset] = useState({ campaign: "", asset_id: "", version: "" });

  // HUI-1664 二维码面板:活动 → 展开链接的 canonical URL + size 白名单下载
  const [qrFor, setQrFor] = useState<string | null>(null);
  const [qrLinks, setQrLinks] = useState<LinkRec[]>([]);
  const [qrMeta, setQrMeta] = useState<Record<string, QrMeta>>({});
  const [qrSize, setQrSize] = useState<number>(256);
  const [qrBusy, setQrBusy] = useState(false);

  // HUI-1665 NFC 标签区(分组 / 批量创建 / 标签表 / CSV 导出)
  const [tagGroups, setTagGroups] = useState<TagGroup[]>([]);
  const [tags, setTags] = useState<TagView[]>([]);
  const [nfcError, setNfcError] = useState("");
  const [newGroup, setNewGroup] = useState("");
  const [tagFilter, setTagFilter] = useState({ campaign: "", group: "", status: "" });
  const [batch, setBatch] = useState({ campaign: "", mode: "shared", count: "10", store: "", group: "", prefix: "" });
  const [batchLinks, setBatchLinks] = useState<LinkRec[]>([]);
  const [batchLinkIds, setBatchLinkIds] = useState<string[]>([]);
  const [rebind, setRebind] = useState<{ tagId: string; campaign: string; links: LinkRec[]; linkId: string } | null>(null);
  const [uidDraft, setUidDraft] = useState<Record<string, string>>({});

  useEffect(() => {
    setTenantId(localStorage.getItem(TENANT_KEY) ?? "");
  }, []);

  const api = useCallback(
    async (method: string, path: string, body?: unknown) => {
      const res = await fetch("/api/" + path, {
        method,
        headers: {
          ...(body ? { "content-type": "application/json" } : {}),
          "x-tenant-id": tenantId,
        },
        body: body ? JSON.stringify(body) : undefined,
      });
      const data = await res.json().catch(() => ({}));
      return { ok: res.ok, status: res.status, data } as { ok: boolean; status: number; data: Record<string, unknown> };
    },
    [tenantId],
  );

  const openQrPanel = useCallback(
    async (campaignId: string) => {
      if (qrFor === campaignId) {
        setQrFor(null);
        return;
      }
      setQrFor(campaignId);
      setQrLinks([]);
      setQrMeta({});
      setQrBusy(true);
      const res = await api("GET", `campaigns/${campaignId}/links`);
      if (!res.ok) {
        setError(whoStatusText(res.status, res.data));
        setQrBusy(false);
        return;
      }
      const items = (res.data.items as LinkRec[]) ?? [];
      setQrLinks(items);
      // 每个链接的 canonical 载荷(json 元数据)用于展示
      const metas: Record<string, QrMeta> = {};
      await Promise.all(
        items.map(async (l) => {
          const m = await api("GET", `campaigns/${campaignId}/links/${l.id}/qrcode?format=json&size=${qrSize}`);
          if (m.ok) metas[l.id] = m.data as unknown as QrMeta;
        }),
      );
      setQrMeta(metas);
      setQrBusy(false);
    },
    [api, qrFor, qrSize],
  );

  const downloadQr = useCallback(
    async (campaignId: string, linkId: string, code: string, size: number) => {
      setError("");
      const res = await fetch(`/api/campaigns/${campaignId}/links/${linkId}/qrcode?size=${size}`, {
        headers: { "x-tenant-id": tenantId },
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(whoStatusText(res.status, data as Record<string, unknown>));
        return;
      }
      const blob = await res.blob();
      const href = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = href;
      a.download = `qr-${code}-${size}.png`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(href);
    },
    [tenantId],
  );

  const loadTags = useCallback(async () => {
    if (!tenantId) return;
    const qs = new URLSearchParams();
    if (tagFilter.campaign) qs.set("campaign_id", tagFilter.campaign);
    if (tagFilter.group) qs.set("group_id", tagFilter.group);
    if (tagFilter.status) qs.set("status", tagFilter.status);
    const [grp, tgs] = await Promise.all([
      api("GET", "nfc/tag-groups"),
      api("GET", "nfc/tags" + (qs.toString() ? "?" + qs.toString() : "")),
    ]);
    if (grp.ok) setTagGroups((grp.data.items as TagGroup[]) ?? []);
    if (tgs.ok) {
      setTags((tgs.data.items as TagView[]) ?? []);
      setNfcError("");
    } else {
      setNfcError(whoStatusText(tgs.status, tgs.data));
    }
  }, [api, tenantId, tagFilter.campaign, tagFilter.group, tagFilter.status]);

  const refresh = useCallback(async () => {
    if (!tenantId) return;
    const who = await api("GET", "whoami");
    if (!who.ok) {
      setError(whoStatusText(who.status, who.data));
      return;
    }
    setRole(String(who.data.role ?? ""));
    setEmailMasked(String(who.data.email ?? ""));
    setError("");
    const [cmp, sto] = await Promise.all([api("GET", "campaigns"), api("GET", "stores")]);
    if (cmp.ok) setCampaigns((cmp.data.items as Campaign[]) ?? []);
    if (sto.ok) setStores((sto.data.items as StoreRec[]) ?? []);
    await loadTags();
  }, [api, tenantId, loadTags]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function handleLogin(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    const res = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ email, password }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      setError(whoStatusText(res.status, data));
      return;
    }
    if (!tenantId) {
      setError("登录成功。请填写租户 ID(由运维提供,形如 tnt_*)后刷新。");
      return;
    }
    await refresh();
  }

  async function transition(id: string, status: string) {
    const res = await api("POST", `campaigns/${id}/status`, { status });
    if (!res.ok) setError(whoStatusText(res.status, res.data));
    await refresh();
  }

  async function createCampaign(e: React.FormEvent) {
    e.preventDefault();
    const res = await api("POST", "campaigns", {
      title: newCampaign.title,
      public_content: newCampaign.public_content,
      starts_at: newCampaign.starts_at || undefined,
      ends_at: newCampaign.ends_at || undefined,
      store_id: newCampaign.store_id || undefined,
      order_ref: newCampaign.order_ref || undefined,
    });
    if (!res.ok) {
      setError(whoStatusText(res.status, res.data));
      return;
    }
    setNewCampaign({ title: "", public_content: "", starts_at: "", ends_at: "", store_id: "", order_ref: "" });
    await refresh();
  }

  async function createCampaignLinks(campaignId: string) {
    const res = await api("POST", `campaigns/${campaignId}/links`, {});
    if (!res.ok) {
      setError(whoStatusText(res.status, res.data));
      return;
    }
    await refresh();
  }

  async function addAsset(e: React.FormEvent) {
    e.preventDefault();
    const res = await api("POST", `campaigns/${newAsset.campaign}/assets`, {
      asset_id: newAsset.asset_id,
      version: newAsset.version || undefined,
    });
    if (!res.ok) {
      setError(whoStatusText(res.status, res.data));
      return;
    }
    setNewAsset({ campaign: "", asset_id: "", version: "" });
    setError("");
  }

  async function logout() {
    await fetch("/api/auth/logout", { method: "POST" });
    setRole("");
    setEmailMasked("");
  }

  // ---- HUI-1665 NFC 标签操作(全部 owner-only,服务端裁决) -----------------

  async function nfcFail(res: { ok: boolean; status: number; data: Record<string, unknown> }) {
    setNfcError(whoStatusText(res.status, res.data));
    return false;
  }

  async function createTagGroup(e: React.FormEvent) {
    e.preventDefault();
    const res = await api("POST", "nfc/tag-groups", { name: newGroup });
    if (!res.ok) return nfcFail(res);
    setNewGroup("");
    await loadTags();
  }

  async function deleteTagGroup(id: string) {
    const res = await api("DELETE", `nfc/tag-groups/${id}`);
    if (!res.ok) return nfcFail(res);
    if (tagFilter.group === id) setTagFilter({ ...tagFilter, group: "" });
    await loadTags();
  }

  async function loadBatchLinks(campaignId: string) {
    setBatch({ ...batch, campaign: campaignId });
    setBatchLinks([]);
    setBatchLinkIds([]);
    if (!campaignId) return;
    const res = await api("GET", `campaigns/${campaignId}/links`);
    if (!res.ok) return nfcFail(res);
    setBatchLinks((res.data.items as LinkRec[]) ?? []);
  }

  async function submitBatch(e: React.FormEvent) {
    e.preventDefault();
    const count = Number(batch.count);
    if (!batch.campaign || batchLinkIds.length === 0 || !Number.isInteger(count) || count < 1 || count > 500) {
      setNfcError("请选择活动与至少一条短码链接,数量须为 1..500 的整数。");
      return;
    }
    const res = await api("POST", "nfc/tags/batch", {
      campaign_id: batch.campaign,
      link_ids: batchLinkIds,
      bind_mode: batch.mode,
      count,
      store_id: batch.store || undefined,
      group_id: batch.group || undefined,
      label_prefix: batch.prefix || undefined,
    });
    if (!res.ok) return nfcFail(res);
    setNfcError("");
    await loadTags();
  }

  async function setTagStatus(tag: TagView, status: "active" | "disabled") {
    const res = await api("POST", `nfc/tags/${tag.id}/status`, { status });
    if (!res.ok) return nfcFail(res);
    setNfcError(status === "disabled" ? `已停用:短码 ${tag.code} 的公共页随即进入停用态。` : "");
    await loadTags();
  }

  async function saveUidHint(tag: TagView) {
    const res = await api("PATCH", `nfc/tags/${tag.id}`, { uid_hint: uidDraft[tag.id] ?? tag.uid_hint ?? "" });
    if (!res.ok) return nfcFail(res);
    setNfcError("");
    await loadTags();
  }

  async function deleteTag(id: string) {
    const res = await api("DELETE", `nfc/tags/${id}`);
    if (!res.ok) return nfcFail(res);
    await loadTags();
  }

  async function openRebind(tag: TagView) {
    if (rebind?.tagId === tag.id) {
      setRebind(null);
      return;
    }
    setRebind({ tagId: tag.id, campaign: tag.campaign_id, links: [], linkId: tag.link_id });
    const res = await api("GET", `campaigns/${tag.campaign_id}/links`);
    if (!res.ok) return nfcFail(res);
    setRebind({ tagId: tag.id, campaign: tag.campaign_id, links: (res.data.items as LinkRec[]) ?? [], linkId: tag.link_id });
  }

  async function rebindCampaign(campaignId: string) {
    if (!rebind) return;
    setRebind({ ...rebind, campaign: campaignId, links: [], linkId: "" });
    if (!campaignId) return;
    const res = await api("GET", `campaigns/${campaignId}/links`);
    if (!res.ok) return nfcFail(res);
    setRebind({ ...rebind, campaign: campaignId, links: (res.data.items as LinkRec[]) ?? [], linkId: "" });
  }

  async function confirmRebind() {
    if (!rebind || !rebind.linkId) {
      setNfcError("请选择目标短码链接。");
      return;
    }
    const res = await api("PATCH", `nfc/tags/${rebind.tagId}`, { link_id: rebind.linkId });
    if (!res.ok) return nfcFail(res);
    setNfcError("");
    setRebind(null);
    await loadTags();
  }

  async function exportCsv() {
    const qs = new URLSearchParams();
    if (tagFilter.campaign) qs.set("campaign_id", tagFilter.campaign);
    if (tagFilter.group) qs.set("group_id", tagFilter.group);
    if (tagFilter.status) qs.set("status", tagFilter.status);
    const query = qs.toString();
    const res = await fetch("/api/nfc/tags/export.csv" + (query ? "?" + query : ""), {
      headers: { "x-tenant-id": tenantId },
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      setNfcError(whoStatusText(res.status, data as Record<string, unknown>));
      return;
    }
    const blob = await res.blob();
    const href = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = href;
    a.download = "nfc-tags.csv";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(href);
  }

  if (!role) {
    return (
      <main style={{ maxWidth: 420, margin: "60px auto", padding: "0 20px" }}>
        <h1>商家后台登录</h1>
        <form onSubmit={handleLogin} style={formStyle}>
          <input placeholder="平台账号邮箱" value={email} onChange={(e) => setEmail(e.target.value)} style={inputStyle} />
          <input placeholder="密码" type="password" value={password} onChange={(e) => setPassword(e.target.value)} style={inputStyle} />
          <input
            placeholder="租户 ID(tnt_*,由运维提供)"
            value={tenantId}
            onChange={(e) => {
              setTenantId(e.target.value);
              localStorage.setItem(TENANT_KEY, e.target.value);
            }}
            style={inputStyle}
          />
          <button type="submit" style={btnStyle}>登录</button>
        </form>
        {error && <p style={{ color: "#b91c1c" }}>{error}</p>}
      </main>
    );
  }

  return (
    <main style={{ maxWidth: 960, margin: "40px auto", padding: "0 20px" }}>
      <header style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline" }}>
        <h1>碰一碰 · 商家后台</h1>
        <div>
          <span style={{ marginRight: 12 }}>{email_} · {role} · {tenantId}</span>
          <button onClick={logout} style={btnStyle}>退出</button>
        </div>
      </header>
      {error && <p style={{ color: "#b91c1c" }}>{error}</p>}

      <section style={sectionStyle}>
        <h2>门店</h2>
        <ul>
          {stores.map((s) => (
            <li key={s.id}>{s.name}({s.address || "无地址"})</li>
          ))}
        </ul>
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            const res = await api("POST", "stores", newStore);
            if (!res.ok) setError(whoStatusText(res.status, res.data));
            setNewStore({ name: "", address: "" });
            await refresh();
          }}
          style={{ display: "flex", gap: 8 }}
        >
          <input placeholder="门店名" value={newStore.name} onChange={(e) => setNewStore({ ...newStore, name: e.target.value })} style={inputStyle} required />
          <input placeholder="地址(可选)" value={newStore.address} onChange={(e) => setNewStore({ ...newStore, address: e.target.value })} style={inputStyle} />
          <button style={btnStyle}>新增门店</button>
        </form>
      </section>

      <section style={sectionStyle}>
        <h2>活动</h2>
        <table width="100%" cellPadding={6} style={{ borderCollapse: "collapse" }}>
          <thead>
            <tr style={{ textAlign: "left", borderBottom: "1px solid #e5e7eb" }}>
              <th>标题</th><th>状态</th><th>有效期</th><th>订单引用</th><th>操作</th>
            </tr>
          </thead>
          <tbody>
            {campaigns.map((c) => (
              <tr key={c.id} style={{ borderBottom: "1px solid #f3f4f6" }}>
                <td>{c.title}</td>
                <td>{c.status}</td>
                <td>{c.starts_at || "∞"} ~ {c.ends_at || "∞"}</td>
                <td>{c.order_ref || "(无订单活动)"}</td>
                <td>
                  {c.status === "draft" && <button onClick={() => transition(c.id, "active")} style={btnStyle}>启用</button>}
                  {c.status === "active" && <button onClick={() => transition(c.id, "paused")} style={btnStyle}>暂停</button>}
                  {c.status === "paused" && <button onClick={() => transition(c.id, "active")} style={btnStyle}>恢复</button>}
                  {(c.status === "active" || c.status === "paused") && <button onClick={() => transition(c.id, "ended")} style={btnStyle}>结束</button>}
                  <button onClick={() => createCampaignLinks(c.id)} style={btnStyle}>生成链接</button>
                  <button onClick={() => void openQrPanel(c.id)} style={{ ...btnStyle, background: "#fff", color: "#2563eb" }}>
                    {qrFor === c.id ? "收起二维码" : "二维码"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>

        {qrFor && (
          <div style={{ marginTop: 12, border: "1px solid #e5e7eb", borderRadius: 8, padding: 12 }}>
            <div style={{ display: "flex", gap: 12, alignItems: "center", marginBottom: 8 }}>
              <strong>二维码(扫码进入公共活动页)</strong>
              <label style={{ fontSize: 14 }}>
                尺寸
                <select value={qrSize} onChange={(e) => setQrSize(Number(e.target.value))} style={{ ...inputStyle, marginLeft: 6, width: "auto" }}>
                  {QR_SIZES.map((s) => (
                    <option key={s} value={s}>{s}px</option>
                  ))}
                </select>
              </label>
            </div>
            {qrBusy && <p style={{ color: "#6b7280" }}>加载中…</p>}
            {!qrBusy && qrLinks.length === 0 && <p style={{ color: "#6b7280" }}>该活动还没有短码链接,请先「生成链接」。</p>}
            {qrLinks.map((l) => (
              <div key={l.id} style={{ display: "flex", gap: 12, alignItems: "center", flexWrap: "wrap", borderBottom: "1px solid #f3f4f6", padding: "6px 0" }}>
                <code style={{ fontSize: 13 }}>{qrMeta[l.id]?.url ?? l.code}</code>
                {!l.enabled && <span style={{ color: "#b45309", fontSize: 13 }}>已停用</span>}
                <button
                  onClick={() => void downloadQr(qrFor, l.id, qrMeta[l.id]?.code ?? l.code, qrSize)}
                  style={btnStyle}
                >
                  下载 PNG({qrSize}px)
                </button>
              </div>
            ))}
            <p style={{ color: "#6b7280", fontSize: 13, margin: "8px 0 0" }}>
              二维码内容为纯短码地址,不含任何凭证或客户信息;游客扫码无需注册商家账户。
            </p>
          </div>
        )}

        <form onSubmit={createCampaign} style={{ ...formStyle, marginTop: 12 }}>
          <input placeholder="活动标题" value={newCampaign.title} onChange={(e) => setNewCampaign({ ...newCampaign, title: e.target.value })} style={inputStyle} required />
          <input placeholder="公开内容(游客可见)" value={newCampaign.public_content} onChange={(e) => setNewCampaign({ ...newCampaign, public_content: e.target.value })} style={inputStyle} />
          <input placeholder="开始时间 RFC3339(可空)" value={newCampaign.starts_at} onChange={(e) => setNewCampaign({ ...newCampaign, starts_at: e.target.value })} style={inputStyle} />
          <input placeholder="结束时间 RFC3339(可空)" value={newCampaign.ends_at} onChange={(e) => setNewCampaign({ ...newCampaign, ends_at: e.target.value })} style={inputStyle} />
          <select value={newCampaign.store_id} onChange={(e) => setNewCampaign({ ...newCampaign, store_id: e.target.value })} style={inputStyle}>
            <option value="">不关联门店</option>
            {stores.map((s) => (
              <option key={s.id} value={s.id}>{s.name}</option>
            ))}
          </select>
          <input placeholder="订单引用(可选,不透明)" value={newCampaign.order_ref} onChange={(e) => setNewCampaign({ ...newCampaign, order_ref: e.target.value })} style={inputStyle} />
          <button style={btnStyle}>创建活动(草稿)</button>
        </form>
      </section>

      <section style={sectionStyle}>
        <h2>素材引用(引用平台资产,不复制文件)</h2>
        <form onSubmit={addAsset} style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          <select value={newAsset.campaign} onChange={(e) => setNewAsset({ ...newAsset, campaign: e.target.value })} style={inputStyle} required>
            <option value="">选择活动</option>
            {campaigns.map((c) => (
              <option key={c.id} value={c.id}>{c.title}</option>
            ))}
          </select>
          <input placeholder="平台 asset_id" value={newAsset.asset_id} onChange={(e) => setNewAsset({ ...newAsset, asset_id: e.target.value })} style={inputStyle} required />
          <input placeholder="版本(可选,须为平台 sha256)" value={newAsset.version} onChange={(e) => setNewAsset({ ...newAsset, version: e.target.value })} style={inputStyle} />
          <button style={btnStyle}>添加引用</button>
        </form>
      </section>

      <section style={sectionStyle}>
        <h2>NFC 标签(仅商家管理员可用;物理写入由 NFC 工具按导出文件执行)</h2>
        {nfcError && <p style={{ color: "#b91c1c" }}>{nfcError}</p>}

        {/* 分组 */}
        <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
          <form onSubmit={createTagGroup} style={{ display: "flex", gap: 8 }}>
            <input placeholder="新分组名" value={newGroup} onChange={(e) => setNewGroup(e.target.value)} style={inputStyle} required />
            <button style={btnStyle}>新建分组</button>
          </form>
          <span style={{ fontSize: 14, color: "#6b7280" }}>
            分组:{tagGroups.length === 0 ? "(无)" : ""}
          </span>
          {tagGroups.map((g) => (
            <span key={g.id} style={{ fontSize: 14, border: "1px solid #e5e7eb", borderRadius: 6, padding: "2px 8px" }}>
              {g.name}{" "}
              <button type="button" onClick={() => void deleteTagGroup(g.id)} style={{ ...btnStyle, padding: "0 6px", background: "#fff", color: "#b91c1c", borderColor: "#b91c1c" }} title="删除分组(组内标签变为未分组)">
                ×
              </button>
            </span>
          ))}
        </div>

        {/* 批量创建 */}
        <form onSubmit={submitBatch} style={{ ...formStyle, marginTop: 12, borderTop: "1px solid #f3f4f6", paddingTop: 12 }}>
          <strong style={{ fontSize: 14 }}>批量创建标签(绑定既有短码,1..500)</strong>
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <select value={batch.campaign} onChange={(e) => void loadBatchLinks(e.target.value)} style={inputStyle} required>
              <option value="">选择活动</option>
              {campaigns.map((c) => (
                <option key={c.id} value={c.id}>{c.title}</option>
              ))}
            </select>
            <select value={batch.mode} onChange={(e) => setBatch({ ...batch, mode: e.target.value })} style={{ ...inputStyle, flex: "0 0 auto" }}>
              <option value="shared">共用一条短码</option>
              <option value="rotate">轮流绑定多条短码</option>
            </select>
            <input placeholder="数量(1..500)" value={batch.count} onChange={(e) => setBatch({ ...batch, count: e.target.value })} style={{ ...inputStyle, flex: "0 1 120px" }} required />
            <select value={batch.store} onChange={(e) => setBatch({ ...batch, store: e.target.value })} style={inputStyle}>
              <option value="">不绑门店</option>
              {stores.map((s) => (
                <option key={s.id} value={s.id}>{s.name}</option>
              ))}
            </select>
            <select value={batch.group} onChange={(e) => setBatch({ ...batch, group: e.target.value })} style={inputStyle}>
              <option value="">不分组</option>
              {tagGroups.map((g) => (
                <option key={g.id} value={g.id}>{g.name}</option>
              ))}
            </select>
            <input placeholder="标签名前缀(默认 NFC)" value={batch.prefix} onChange={(e) => setBatch({ ...batch, prefix: e.target.value })} style={inputStyle} />
            <button style={btnStyle}>批量生成</button>
          </div>
          {batch.campaign && (
            <div style={{ display: "flex", gap: 12, flexWrap: "wrap", fontSize: 14 }}>
              {batchLinks.length === 0 && <span style={{ color: "#6b7280" }}>该活动还没有短码,请先在活动区「生成链接」。</span>}
              {batchLinks.map((l) => (
                <label key={l.id} style={{ display: "inline-flex", gap: 4, alignItems: "center" }}>
                  <input
                    type="checkbox"
                    checked={batchLinkIds.includes(l.id)}
                    onChange={(e) =>
                      setBatchLinkIds(e.target.checked ? [...batchLinkIds, l.id] : batchLinkIds.filter((x) => x !== l.id))
                    }
                  />
                  <code>{l.code}</code>
                  {!l.enabled && <span style={{ color: "#b45309" }}>(停用)</span>}
                </label>
              ))}
            </div>
          )}
        </form>

        {/* 筛选 + 导出 */}
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap", marginTop: 12, alignItems: "center" }}>
          <select value={tagFilter.campaign} onChange={(e) => setTagFilter({ ...tagFilter, campaign: e.target.value })} style={{ ...inputStyle, flex: "0 1 auto" }}>
            <option value="">全部活动</option>
            {campaigns.map((c) => (
              <option key={c.id} value={c.id}>{c.title}</option>
            ))}
          </select>
          <select value={tagFilter.group} onChange={(e) => setTagFilter({ ...tagFilter, group: e.target.value })} style={{ ...inputStyle, flex: "0 1 auto" }}>
            <option value="">全部分组</option>
            {tagGroups.map((g) => (
              <option key={g.id} value={g.id}>{g.name}</option>
            ))}
          </select>
          <select value={tagFilter.status} onChange={(e) => setTagFilter({ ...tagFilter, status: e.target.value })} style={{ ...inputStyle, flex: "0 1 auto" }}>
            <option value="">全部状态</option>
            <option value="active">启用</option>
            <option value="disabled">停用</option>
          </select>
          <button onClick={() => void exportCsv()} style={btnStyle}>导出 CSV(供 NFC 写入工具)</button>
          <span style={{ fontSize: 13, color: "#6b7280" }}>{tags.length} 条标签</span>
        </div>

        {/* 标签表 */}
        <table width="100%" cellPadding={6} style={{ borderCollapse: "collapse", marginTop: 8 }}>
          <thead>
            <tr style={{ textAlign: "left", borderBottom: "1px solid #e5e7eb" }}>
              <th>标签</th><th>短码 / URL</th><th>门店</th><th>分组</th><th>状态</th><th>UID 提示(写入后回填)</th><th>操作</th>
            </tr>
          </thead>
          <tbody>
            {tags.map((t) => (
              <tr key={t.id} style={{ borderBottom: "1px solid #f3f4f6" }}>
                <td>{t.label}</td>
                <td><code style={{ fontSize: 12 }}>https://…/c/{t.code}</code></td>
                <td>{t.store_name || "—"}</td>
                <td>{t.group_name || "—"}</td>
                <td>{t.status === "active" ? "启用" : <span style={{ color: "#b45309" }}>停用</span>}</td>
                <td>
                  <span style={{ display: "inline-flex", gap: 4 }}>
                    <input
                      placeholder="如 04:A2:2F"
                      value={uidDraft[t.id] ?? t.uid_hint ?? ""}
                      onChange={(e) => setUidDraft({ ...uidDraft, [t.id]: e.target.value })}
                      style={{ ...inputStyle, flex: "0 1 140px", padding: "4px 6px" }}
                    />
                    <button type="button" onClick={() => void saveUidHint(t)} style={{ ...btnStyle, padding: "4px 8px" }}>存</button>
                  </span>
                </td>
                <td style={{ whiteSpace: "nowrap" }}>
                  {t.status === "active"
                    ? <button onClick={() => void setTagStatus(t, "disabled")} style={{ ...btnStyle, background: "#fff", color: "#b45309", borderColor: "#b45309" }}>停用</button>
                    : <button onClick={() => void setTagStatus(t, "active")} style={btnStyle}>恢复</button>}
                  <button onClick={() => void openRebind(t)} style={{ ...btnStyle, background: "#fff", color: "#2563eb" }}>
                    {rebind?.tagId === t.id ? "收起换绑" : "换绑"}
                  </button>
                  <button onClick={() => void deleteTag(t.id)} style={{ ...btnStyle, background: "#fff", color: "#b91c1c", borderColor: "#b91c1c" }}>删除</button>
                </td>
              </tr>
            ))}
            {tags.length === 0 && (
              <tr><td colSpan={7} style={{ color: "#6b7280" }}>暂无标签;先用上方向导批量生成,再导出 CSV 交给 NFC 写入工具。</td></tr>
            )}
          </tbody>
        </table>

        {/* 换绑面板 */}
        {rebind && (
          <div style={{ marginTop: 8, border: "1px solid #e5e7eb", borderRadius: 8, padding: 12 }}>
            <strong style={{ fontSize: 14 }}>换绑标签:先选活动,再选该活动下的短码(标签 URL 随之变化)</strong>
            <div style={{ display: "flex", gap: 8, flexWrap: "wrap", marginTop: 8 }}>
              <select value={rebind.campaign} onChange={(e) => void rebindCampaign(e.target.value)} style={inputStyle}>
                <option value="">选择活动</option>
                {campaigns.map((c) => (
                  <option key={c.id} value={c.id}>{c.title}</option>
                ))}
              </select>
              <select value={rebind.linkId} onChange={(e) => setRebind({ ...rebind, linkId: e.target.value })} style={inputStyle}>
                <option value="">选择短码</option>
                {rebind.links.map((l) => (
                  <option key={l.id} value={l.id}>{l.code}{l.enabled ? "" : "(停用)"}</option>
                ))}
              </select>
              <button onClick={() => void confirmRebind()} style={btnStyle}>确认换绑</button>
            </div>
          </div>
        )}

        <p style={{ color: "#6b7280", fontSize: 13, margin: "8px 0 0" }}>
          CSV 列:label, short_code, url, store, group, uid_hint(初始留空);URL 为纯短码地址,不含任何凭证或客户信息。
          停用标签后其短码公共页立即进入停用态;物理写入与实机验证由 NFC 工具执行。
        </p>
      </section>
    </main>
  );
}

function whoStatusText(status: number, data: Record<string, unknown>): string {
  const detail = typeof data.message === "string" ? `:${data.message}` : "";
  return `请求失败(${status} ${String(data.error ?? "")})${detail}`;
}

const formStyle: React.CSSProperties = { display: "flex", flexDirection: "column", gap: 8 };
const inputStyle: React.CSSProperties = { padding: "8px 10px", border: "1px solid #d1d5db", borderRadius: 6, flex: "1 1 160px" };
const btnStyle: React.CSSProperties = { padding: "8px 14px", borderRadius: 6, border: "1px solid #2563eb", background: "#2563eb", color: "#fff", cursor: "pointer" };
const sectionStyle: React.CSSProperties = { background: "#fff", borderRadius: 8, padding: 16, marginTop: 20, border: "1px solid #e5e7eb" };
