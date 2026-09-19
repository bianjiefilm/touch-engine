import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { bffPathToUpstream, proxyToServer, readBffEnv, type BffEnv } from "../src/lib/bff";

// BFF tests: the web relay must be a faithful, non-escalating proxy.
// The admin(后台) x guest(游客) x tenant matrix is enforced by the Go server;
// these tests prove the BFF (1) forwards identity/cookies verbatim, (2) injects
// and overrides the internal token, (3) relays every verdict unchanged (no
// status widening, no silent success), (4) fails closed when unconfigured, and
// (5) re-verifies the full matrix through the BFF layer.

const ENV: BffEnv = { serverUrl: "http://127.0.0.1:18240", internalToken: "bff-internal-secret" };

// ---- fake Go server implementing the same matrix the integration tests cover ----

interface Session {
  principal: string;
  tenant: string;
  role: "owner" | "staff";
  enabled: boolean;
}

const SESSIONS: Record<string, Session> = {
  "sess-owner-a": { principal: "usr_owner_a", tenant: "tnt_A", role: "owner", enabled: true },
  "sess-staff-a": { principal: "usr_staff_a", tenant: "tnt_A", role: "staff", enabled: true },
  "sess-disabled-a": { principal: "usr_disabled_a", tenant: "tnt_A", role: "staff", enabled: false },
  "sess-owner-b": { principal: "usr_owner_b", tenant: "tnt_B", role: "owner", enabled: true },
};

// campaign cmp_A1 lives in tenant A. public link CODE_VALID is available.
const CAMPAIGNS: Record<string, { tenant: string; title: string }> = {
  cmp_A1: { tenant: "tnt_A", title: "A周年庆" },
};
const PUBLIC_LINKS: Record<string, { state: string; campaign: string }> = {
  CODEVALID12: { state: "available", campaign: "cmp_A1" },
  CODEPAUSED12: { state: "paused", campaign: "cmp_A1" },
};

function upstreamMatrix(req: Request, path: string): Response {
  const fail = (status: number, code: string) =>
    new Response(JSON.stringify({ error: code }), { status });

  if (req.headers.get("x-internal-token") !== ENV.internalToken) {
    return fail(401, "unauthorized");
  }

  // public surface: no session concept; GET-only, state-specific answers
  const pub = path.match(/^public\/links\/([^/]+)$/);
  if (pub) {
    if (req.method !== "GET") return fail(405, "method_not_allowed");
    const link = PUBLIC_LINKS[pub[1]];
    if (!link || link.state !== "available") {
      return new Response(JSON.stringify({ state: link?.state ?? "not_found" }), { status: 404 });
    }
    return Response.json({
      state: "available",
      title: CAMPAIGNS[link.campaign].title,
      public_content: "到店有礼",
    });
  }

  // admin surface: session + tenant membership required
  const sessionToken = cookieToken(req.headers.get("cookie"));
  const sess = sessionToken ? SESSIONS[sessionToken] : undefined;
  if (!sess) return fail(401, "unauthenticated");
  if (!sess.enabled) return fail(403, "member_disabled");

  const tenant = req.headers.get("x-tenant-id") ?? "";
  if (!tenant) return fail(400, "tenant_required");
  if (tenant !== sess.tenant) return fail(403, "not_member");

  if (path === "campaigns" && req.method === "GET") {
    return Response.json({ items: [{ id: "cmp_A1", tenant: sess.tenant }] });
  }
  if (path === "campaigns" && req.method === "POST") {
    return Response.json({ id: "cmp_new", status: "draft" }, { status: 201 });
  }
  const match = path.match(/^campaigns\/([^/]+)$/);
  if (match) {
    const rec = CAMPAIGNS[match[1]];
    if (!rec || rec.tenant !== sess.tenant) return fail(404, "not_found");
    if (req.method === "GET") return Response.json({ id: match[1], title: rec.title });
    return Response.json({ id: match[1], title: "改" });
  }
  const statusMatch = path.match(/^campaigns\/([^/]+)\/status$/);
  if (statusMatch) {
    const rec = CAMPAIGNS[statusMatch[1]];
    if (!rec || rec.tenant !== sess.tenant) return fail(404, "not_found");
    return Response.json({ id: statusMatch[1], status: "active" });
  }
  if (path === "admin/members") {
    if (sess.role !== "owner") return fail(403, "forbidden");
    return Response.json({ items: [] });
  }
  return fail(404, "no_route");
}

function cookieToken(cookie: string | null): string | null {
  if (!cookie) return null;
  const m = cookie.match(/touch_session=([^;]+)/);
  return m ? m[1] : null;
}

let upstreamCalls: { url: string; headers: Headers; method: string }[] = [];

beforeEach(() => {
  upstreamCalls = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    upstreamCalls.push({
      url,
      headers: new Headers(init?.headers as HeadersInit),
      method: init?.method ?? "GET",
    });
    const parsed = new URL(url);
    const path = parsed.pathname.replace(/^\/api\/v1\//, "");
    const req = new Request(url, {
      method: init?.method ?? "GET",
      headers: init?.headers as HeadersInit,
      body: init?.body as BodyInit,
    });
    return upstreamMatrix(req, path);
  }));
});
afterEach(() => vi.unstubAllGlobals());

function bffReq(method: string, path: string, opts: { cookie?: string; tenant?: string; body?: string; extraHeaders?: Record<string, string> } = {}): Request {
  const headers = new Headers(opts.extraHeaders);
  if (opts.cookie) headers.set("cookie", opts.cookie);
  if (opts.tenant) headers.set("x-tenant-id", opts.tenant);
  if (opts.body) headers.set("content-type", "application/json");
  return new Request(`http://bff.local/api/${path}`, { method, headers, body: opts.body });
}

const segments = (p: string) => p.split("/").filter(Boolean);

async function call(method: string, path: string, opts: Parameters<typeof bffReq>[2] = {}) {
  return proxyToServer(bffReq(method, path, opts), ENV, segments(path));
}

// ---- config gate ------------------------------------------------------------------

describe("BFF config gate", () => {
  it("fails closed with 503 and never calls upstream when unconfigured", async () => {
    const res = await proxyToServer(bffReq("GET", "campaigns", { tenant: "tnt_A" }), { serverUrl: "", internalToken: "" }, ["campaigns"]);
    expect(res.status).toBe(503);
    const body = await res.json();
    expect(body.error).toBe("bff_config_gate");
    expect(body.detail).toEqual(["TOUCH_SERVER_URL is required", "TOUCH_INTERNAL_TOKEN is required"]);
    expect(upstreamCalls).toHaveLength(0);
  });

  it("readBffEnv trims and reads the documented keys", () => {
    const env = readBffEnv({ TOUCH_SERVER_URL: " http://x ", TOUCH_INTERNAL_TOKEN: " t " });
    expect(env.serverUrl).toBe("http://x");
    expect(env.internalToken).toBe("t");
  });
});

// ---- the admin(后台) x guest(游客) x tenant matrix, through the BFF ----------------

describe("BFF matrix relay (admin x guest x tenant)", () => {
  const cookie = (s: string) => `touch_session=${s}; HttpOnly`;

  const matrix: {
    name: string;
    session: string;
    tenant: string;
    method: string;
    path: string;
    want: number;
  }[] = [
    { name: "owner lists own campaigns", session: "sess-owner-a", tenant: "tnt_A", method: "GET", path: "campaigns", want: 200 },
    { name: "staff lists own campaigns", session: "sess-staff-a", tenant: "tnt_A", method: "GET", path: "campaigns", want: 200 },
    { name: "owner reads own campaign", session: "sess-owner-a", tenant: "tnt_A", method: "GET", path: "campaigns/cmp_A1", want: 200 },
    { name: "staff reads own campaign", session: "sess-staff-a", tenant: "tnt_A", method: "GET", path: "campaigns/cmp_A1", want: 200 },
    { name: "owner creates campaign", session: "sess-owner-a", tenant: "tnt_A", method: "POST", path: "campaigns", want: 201 },
    { name: "staff creates campaign", session: "sess-staff-a", tenant: "tnt_A", method: "POST", path: "campaigns", want: 201 },
    { name: "staff pauses campaign", session: "sess-staff-a", tenant: "tnt_A", method: "POST", path: "campaigns/cmp_A1/status", want: 200 },
    { name: "staff cannot manage members", session: "sess-staff-a", tenant: "tnt_A", method: "GET", path: "admin/members", want: 403 },
    { name: "owner manages members", session: "sess-owner-a", tenant: "tnt_A", method: "GET", path: "admin/members", want: 200 },
    { name: "disabled member refused", session: "sess-disabled-a", tenant: "tnt_A", method: "GET", path: "campaigns", want: 403 },
    { name: "no session refused", session: "", tenant: "tnt_A", method: "GET", path: "campaigns", want: 401 },
    { name: "cross tenant read refused", session: "sess-owner-b", tenant: "tnt_A", method: "GET", path: "campaigns/cmp_A1", want: 403 },
    { name: "cross tenant list forged refused", session: "sess-owner-b", tenant: "tnt_A", method: "GET", path: "campaigns", want: 403 },
    { name: "cross tenant status refused", session: "sess-owner-b", tenant: "tnt_A", method: "POST", path: "campaigns/cmp_A1/status", want: 403 },
    { name: "owner B lists own (never A's data)", session: "sess-owner-b", tenant: "tnt_B", method: "GET", path: "campaigns", want: 200 },
    { name: "guest public page available", session: "", tenant: "", method: "GET", path: "public/links/CODEVALID12", want: 200 },
    { name: "guest public page paused-state 404", session: "", tenant: "", method: "GET", path: "public/links/CODEPAUSED12", want: 404 },
    { name: "guest public page unknown 404", session: "", tenant: "", method: "GET", path: "public/links/UNKNOWNCODE1", want: 404 },
    { name: "guest cannot write public surface", session: "", tenant: "", method: "POST", path: "public/links/CODEVALID12", want: 405 },
    { name: "guest cannot create campaign", session: "", tenant: "", method: "POST", path: "campaigns", want: 401 },
  ];

  for (const c of matrix) {
    it(c.name, async () => {
      const res = await call(c.method, c.path, {
        cookie: c.session ? cookie(c.session) : undefined,
        tenant: c.tenant,
        body: c.method === "GET" ? undefined : "{}",
      });
      expect(res.status).toBe(c.want);
    });
  }

  it("owner B's campaign list never contains A's records", async () => {
    const res = await call("GET", "campaigns", { cookie: cookie("sess-owner-b"), tenant: "tnt_B" });
    const body = await res.json();
    for (const item of body.items as { tenant: string }[]) {
      expect(item.tenant).toBe("tnt_B");
    }
  });
});

// ---- relay semantics ---------------------------------------------------------------

describe("BFF relay semantics", () => {
  it("maps /api/X to upstream /api/v1/X and preserves the query string", async () => {
    const req = new Request("http://bff.local/api/campaigns?limit=5");
    await proxyToServer(req, ENV, ["campaigns"]);
    expect(upstreamCalls[0].url).toContain("http://127.0.0.1:18240/api/v1/campaigns?limit=5");
  });

  it("forwards the session cookie verbatim", async () => {
    await call("GET", "campaigns", { cookie: "touch_session=sess-owner-a", tenant: "tnt_A" });
    expect(upstreamCalls[0].headers.get("cookie")).toBe("touch_session=sess-owner-a");
  });

  it("overrides any caller-supplied internal token (no escalation)", async () => {
    const res = await call("GET", "campaigns", {
      cookie: "touch_session=sess-owner-a",
      tenant: "tnt_A",
      extraHeaders: { "x-internal-token": "forged-token" },
    });
    expect(res.status).toBe(200);
    expect(upstreamCalls[0].headers.get("x-internal-token")).toBe(ENV.internalToken);
  });

  it("relays upstream errors unchanged (no silent success)", async () => {
    const res = await call("GET", "campaigns", { cookie: "touch_session=sess-disabled-a", tenant: "tnt_A" });
    expect(res.status).toBe(403);
    const body = await res.json();
    expect(body.error).toBe("member_disabled");
  });

  it("relays set-cookie headers back to the browser (login flow)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => {
      const res = new Response(JSON.stringify({ authenticated: true }), { status: 200 });
      res.headers.append("set-cookie", "touch_session=tok1; HttpOnly; SameSite=Lax");
      res.headers.append("set-cookie", "touch_session_refresh=rt1; HttpOnly; SameSite=Lax");
      return res;
    }));
    const res = await call("POST", "auth/login", { body: JSON.stringify({ email: "a@b.c", password: "x" }) });
    expect(res.status).toBe(200);
    const setCookies = res.headers.getSetCookie();
    expect(setCookies).toHaveLength(2);
    expect(setCookies[0]).toContain("touch_session=tok1");
  });

  it("when the Go server is down the BFF answers 503 server_unavailable", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("ECONNREFUSED"); }));
    const res = await call("GET", "campaigns", { cookie: "touch_session=sess-owner-a", tenant: "tnt_A" });
    expect(res.status).toBe(503);
    const body = await res.json();
    expect(body.error).toBe("server_unavailable");
  });

  it("bffPathToUpstream builds /api/v1/... paths", () => {
    expect(bffPathToUpstream(["campaigns", "cmp_1"])).toBe("/api/v1/campaigns/cmp_1");
    expect(bffPathToUpstream(["auth", "login"])).toBe("/api/v1/auth/login");
    expect(bffPathToUpstream(["public", "links", "CODE12"])).toBe("/api/v1/public/links/CODE12");
  });
});
