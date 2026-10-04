"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { surfaceLabel } from "@/lib/product-finish";

const TENANT_KEY = "touch_admin_tenant";

export interface MerchantResult {
  ok: boolean;
  status: number;
  data: Record<string, unknown>;
}

export interface MerchantSession {
  tenantId: string;
  role: string;
  email: string;
  tenantName: string;
  phase: "loading" | "anonymous" | "ready" | "error";
  error: string;
  login: (email: string, password: string, tenantId: string) => Promise<void>;
  logout: () => Promise<void>;
  switchTenant: (tenantId: string) => void;
  api: (method: string, path: string, body?: unknown) => Promise<MerchantResult>;
}

const SessionContext = createContext<MerchantSession | null>(null);

export function useSession(): MerchantSession {
  const value = useContext(SessionContext);
  if (!value) throw new Error("merchant session missing");
  return value;
}

function failureText(status: number, data: Record<string, unknown>): string {
  const detail = typeof data.message === "string" ? `：${data.message}` : "";
  return `没有完成（${status} ${String(data.error ?? "")}）${detail}`;
}

export function MerchantGate({ children }: { children: ReactNode }) {
  const session = useMerchantSession();
  if (session.phase === "loading") {
    return (
      <main className="tk-page">
        <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p>
      </main>
    );
  }
  if (session.phase === "error") {
    return (
      <main className="tk-page">
        <p className="tk-state tk-danger" data-state="error">{session.error}</p>
      </main>
    );
  }
  if (session.phase !== "ready") return <Login session={session} />;
  return <SessionContext.Provider value={session}>{children}</SessionContext.Provider>;
}

function Login({ session }: { session: MerchantSession }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [tenantId, setTenantId] = useState(session.tenantId);
  const [error, setError] = useState("");

  return (
    <main className="tk-page">
      <p className="tk-kicker">碰一碰</p>
      <h1 className="tk-title">今天要做的事</h1>
      <p className="tk-lead">登录后看今天的活动、门店、素材、触达、留资和还不能当成完成的奖励。</p>
      <form
        className="tk-form"
        onSubmit={(event) => {
          event.preventDefault();
          setError("");
          void session.login(email, password, tenantId).catch((err: unknown) => {
            setError(err instanceof Error ? err.message : "没有登录");
          });
        }}
      >
        <label className="tk-label">
          邮箱
          <input className="tk-input" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="username" required />
        </label>
        <label className="tk-label">
          密码
          <input className="tk-input" type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" required />
        </label>
        <label className="tk-label">
          租户编号
          <input className="tk-input" value={tenantId} onChange={(event) => setTenantId(event.target.value)} required />
        </label>
        <button className="tk-button" type="submit">登录并查看今天</button>
      </form>
      {error ? <p className="tk-danger" data-state="error">{error}</p> : null}
    </main>
  );
}

function useMerchantSession(): MerchantSession {
  const [tenantId, setTenantId] = useState("");
  const [role, setRole] = useState("");
  const [email, setEmail] = useState("");
  const [tenantName, setTenantName] = useState("");
  const [phase, setPhase] = useState<MerchantSession["phase"]>("loading");
  const [error, setError] = useState("");

  const applyWho = useCallback(async (nextTenant: string) => {
    const res = await fetch("/api/whoami", { headers: { "x-tenant-id": nextTenant } });
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (!res.ok || typeof data.role !== "string" || data.role === "") {
      setRole("");
      setPhase("anonymous");
      return;
    }
    setRole(data.role);
    setEmail(typeof data.email === "string" ? data.email : "");
    setTenantName(typeof data.tenant_name === "string" ? data.tenant_name : "");
    setPhase("ready");
  }, []);

  useEffect(() => {
    const stored = window.localStorage.getItem(TENANT_KEY) ?? "";
    setTenantId(stored);
    if (!stored) {
      setPhase("anonymous");
      return;
    }
    void applyWho(stored).catch(() => {
      setError("没有连上服务。登录状态未知，没有把它当成已登录。");
      setPhase("error");
    });
  }, [applyWho]);

  const api = useCallback(async (method: string, path: string, body?: unknown) => {
    const res = await fetch("/api/" + path.replace(/^\//, ""), {
      method,
      headers: {
        "x-tenant-id": tenantId,
        ...(body ? { "content-type": "application/json" } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    return { ok: res.ok, status: res.status, data };
  }, [tenantId]);

  const login = useCallback(async (nextEmail: string, password: string, nextTenant: string) => {
    const res = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ email: nextEmail, password }),
    });
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (!res.ok) throw new Error(failureText(res.status, data));
    window.localStorage.setItem(TENANT_KEY, nextTenant);
    setTenantId(nextTenant);
    await applyWho(nextTenant);
  }, [applyWho]);

  const logout = useCallback(async () => {
    await fetch("/api/auth/logout", { method: "POST" });
    setRole("");
    setEmail("");
    setPhase("anonymous");
  }, []);

  const switchTenant = useCallback((nextTenant: string) => {
    window.localStorage.setItem(TENANT_KEY, nextTenant);
    setTenantId(nextTenant);
    setRole("");
    setPhase("loading");
    void applyWho(nextTenant).catch(() => {
      setError("切换后没有读到身份。没有沿用上一户的数据。");
      setPhase("error");
    });
  }, [applyWho]);

  return useMemo(() => ({
    tenantId,
    role,
    email,
    tenantName,
    phase,
    error,
    login,
    logout,
    switchTenant,
    api,
  }), [api, email, error, login, logout, phase, role, switchTenant, tenantId, tenantName]);
}

export { failureText };
