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
  }, [api, tenantId]);

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
                </td>
              </tr>
            ))}
          </tbody>
        </table>

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
