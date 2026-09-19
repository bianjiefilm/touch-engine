import { afterEach, describe, expect, it, vi } from "vitest";
import { proxyToServer, type BffEnv } from "../src/lib/bff";

// HUI-1664:QR PNG 经 BFF 透传 —— 二进制原样、content-type 不被改写、
// size/format 查询串原样上行、内部令牌由 BFF 注入。

const ENV: BffEnv = { serverUrl: "http://127.0.0.1:18240", internalToken: "bff-internal-secret" };

const PNG_1PX = Uint8Array.from([
  0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
]);

let lastInput: string | null = null;
let lastToken: string | null = null;
let lastTenant: string | null = null;

afterEach(() => {
  vi.unstubAllGlobals();
  lastInput = null;
  lastToken = null;
  lastTenant = null;
});

function stubPngUpstream(): void {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      lastInput = typeof input === "string" ? input : input.toString();
      lastToken = new Headers(init?.headers).get("x-internal-token");
      lastTenant = new Headers(init?.headers).get("x-tenant-id");
      return new Response(PNG_1PX.slice().buffer, {
        status: 200,
        headers: { "content-type": "image/png", "content-disposition": 'attachment; filename="qr-X-256.png"' },
      });
    }),
  );
}

describe("BFF QR PNG 透传", () => {
  it("查询串上行、二进制与头原样回传", async () => {
    stubPngUpstream();
    const req = new Request(
      "http://h5.test/api/campaigns/cmp_1/links/lnk_1/qrcode?size=512",
      { headers: { "x-tenant-id": "tnt_a" } },
    );
    const res = await proxyToServer(req, ENV, ["campaigns", "cmp_1", "links", "lnk_1", "qrcode"]);

    expect(lastInput).toBe(
      "http://127.0.0.1:18240/api/v1/campaigns/cmp_1/links/lnk_1/qrcode?size=512",
    );
    expect(lastToken).toBe("bff-internal-secret");
    expect(lastTenant).toBe("tnt_a");
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("image/png");
    const got = new Uint8Array(await res.arrayBuffer());
    expect(got).toEqual(PNG_1PX);
  });

  it("format=json 元数据同样原样透传", async () => {
    stubPngUpstream();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string | URL | Request) => {
        lastInput = typeof input === "string" ? input : input.toString();
        return new Response(`{"url":"https://h5.test/c/ABC123XYZ789","code":"ABC123XYZ789","size":256}`, {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }),
    );
    const req = new Request(
      "http://h5.test/api/campaigns/cmp_1/links/lnk_1/qrcode?format=json&size=256",
      { headers: { "x-tenant-id": "tnt_a" } },
    );
    const res = await proxyToServer(req, ENV, ["campaigns", "cmp_1", "links", "lnk_1", "qrcode"]);
    expect(lastInput).toBe(
      "http://127.0.0.1:18240/api/v1/campaigns/cmp_1/links/lnk_1/qrcode?format=json&size=256",
    );
    expect(res.status).toBe(200);
    const data = (await res.json()) as { url: string; code: string; size: number };
    expect(data).toEqual({ url: "https://h5.test/c/ABC123XYZ789", code: "ABC123XYZ789", size: 256 });
  });
});
