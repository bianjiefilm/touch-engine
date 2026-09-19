import { afterEach, describe, expect, it, vi } from "vitest";
import { proxyToServer, type BffEnv } from "../src/lib/bff";

// HUI-1747 中继纪律:公共留资面(唯一游客写面)经 BFF 透传时必须——
//   1. 原样转发 POST body(联系方式只在 body 中,不入 URL);
//   2. 内部令牌由 BFF 注入并覆盖调用方(不可提权);
//   3. Go 服务端的裁定(201/200/400/404/409/429)原样回传,不加宽、不伪成功;
//   4. BFF 未配置 = 503 fail-closed;
//   5. 撤销调用同样原样透传,验证失败 404 原样回传。

const ENV: BffEnv = { serverUrl: "http://127.0.0.1:18240", internalToken: "bff-internal-secret" };

let lastRequest: { method: string; path: string; token: string | null; body: string } | null = null;
let stubStatus = 201;
let stubBody = `{"submission_ref":"sub_abc","state":"accepted","duplicate":false}`;

const upstream = httpStub();

// proxyToServer calls fetch(upstreamUrl: string, init: RequestInit) with
// init.body already materialized as an ArrayBuffer — mirror that exact shape.
function httpStub(): ReturnType<typeof vi.fn> {
  return vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const raw = init?.body;
    const body =
      typeof raw === "string" ? raw : raw instanceof ArrayBuffer ? new TextDecoder().decode(raw) : "";
    lastRequest = {
      method: init?.method ?? "GET",
      path: new URL(typeof input === "string" ? input : input.toString()).pathname,
      token: new Headers(init?.headers).get("x-internal-token"),
      body,
    };
    // 204 must carry a null body (undici throws on `new Response("", {204})`)
    return new Response(stubStatus === 204 ? null : stubBody, {
      status: stubStatus,
      headers: { "content-type": "application/json" },
    });
  }) as unknown as ReturnType<typeof vi.fn>;
}

// patch global fetch for the duration of each test
function withStubbedFetch(run: () => Promise<void>) {
  const realFetch = globalThis.fetch;
  globalThis.fetch = upstream as unknown as typeof fetch;
  return run().finally(() => {
    globalThis.fetch = realFetch;
  });
}

afterEach(() => {
  stubStatus = 201;
  stubBody = `{"submission_ref":"sub_abc","state":"accepted","duplicate":false}`;
  lastRequest = null;
});

function post(path: string, body: string): Promise<Response> {
  return proxyToServer(
    new Request(`http://localhost:18340${path}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body,
    }),
    ENV,
    path.replace(/^\/api\//, "").split("/"),
  );
}

describe("BFF relays the public lead-capture surface", () => {
  it("forwards lead submissions verbatim and injects the internal token", async () => {
    await withStubbedFetch(async () => {
      const body = `{"name":"张三","phone":"13800138000","consent_version":"v1","consent":true,"marketing_optin":true}`;
      const res = await post("/api/public/links/ABC123XYZ456/lead-submissions", body);
      expect(res.status).toBe(201);
      expect(lastRequest?.method).toBe("POST");
      expect(lastRequest?.path).toBe("/api/v1/public/links/ABC123XYZ456/lead-submissions");
      expect(lastRequest?.body).toBe(body);
      expect(lastRequest?.token).toBe("bff-internal-secret");
      const out = (await res.json()) as { submission_ref: string };
      expect(out.submission_ref).toBe("sub_abc");
    });
  });

  it("relays the Go server verdicts unchanged (no status widening)", async () => {
    await withStubbedFetch(async () => {
      for (const tc of [
        { status: 400, body: `{"error":"consent_required","message":"x"}` },
        { status: 404, body: `{"state":"expired"}` },
        { status: 409, body: `{"error":"lead_capture_not_open"}` },
        { status: 429, body: `{"error":"rate_limited"}` },
      ]) {
        stubStatus = tc.status;
        stubBody = tc.body;
        const res = await post("/api/public/links/ABC123XYZ456/lead-submissions", `{}`);
        expect(res.status).toBe(tc.status);
        expect(await res.json()).toEqual(JSON.parse(tc.body));
      }
    });
  });

  it("relays revocations and their uniform 404 without reinterpretation", async () => {
    await withStubbedFetch(async () => {
      stubStatus = 200;
      stubBody = `{"submission_ref":"sub_abc","state":"revoked"}`;
      const ok = await post("/api/public/links/ABC123XYZ456/lead-revocations", `{"submission_ref":"sub_abc","phone":"13800138000"}`);
      expect(ok.status).toBe(200);
      expect(lastRequest?.path).toBe("/api/v1/public/links/ABC123XYZ456/lead-revocations");

      stubStatus = 404;
      stubBody = `{"error":"not_found","message":"submission not found or verification failed"}`;
      const denied = await post("/api/public/links/ABC123XYZ456/lead-revocations", `{"submission_ref":"sub_x","phone":"100"}`);
      expect(denied.status).toBe(404);
    });
  });

  it("relays the anonymous view beacon without injecting identity", async () => {
    await withStubbedFetch(async () => {
      stubStatus = 204;
      stubBody = "";
      const res = await post("/api/public/links/ABC123XYZ456/view-events", `{"channel":"web"}`);
      expect(res.status).toBe(204);
      expect(lastRequest?.path).toBe("/api/v1/public/links/ABC123XYZ456/view-events");
      expect(lastRequest?.body).toBe(`{"channel":"web"}`);
    });
  });

  it("fails closed when the BFF is unconfigured", async () => {
    const unconfigured: BffEnv = { serverUrl: "", internalToken: "   " };
    const res = await proxyToServer(
      new Request("http://localhost:18340/api/public/links/ABC123XYZ456/lead-submissions", {
        method: "POST",
        body: `{}`,
      }),
      unconfigured,
      ["public", "links", "ABC123XYZ456", "lead-submissions"],
    );
    expect(res.status).toBe(503);
    const out = (await res.json()) as { error: string };
    expect(out.error).toBe("bff_config_gate");
  });
});
