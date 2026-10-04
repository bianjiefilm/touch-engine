#!/usr/bin/env node
// HUI-2628 r2 E2E 专用身份 stub（:18461）。
// 实现 touch-server internal/identity.Client 的最小协议：
//   POST /internal/v1/identity/login           {email,password,app_id} -> TokenPair
//   POST /internal/v1/identity/session/resolve {session_token}          -> {authenticated, session}
//   POST /internal/v1/identity/refresh         -> TokenPair
//   POST /internal/v1/identity/revocations     -> 200
// session token 确定性编码 email（base64url），resolve 反解出 principal/email。
// 仅监听 127.0.0.1；凭据是合成串，不落库不入证据。
import http from "node:http";

const port = Number(process.argv[2] || 18461);
const PRINCIPAL = process.env.E2E_PRINCIPAL || "usr_e2e_owner";

const pair = (email) => ({
  access_token: "sess-" + Buffer.from(email).toString("base64url"),
  refresh_token: "refresh-" + Buffer.from(email).toString("base64url"),
  token_type: "Bearer",
  expires_in: 3600,
});

function readBody(req) {
  return new Promise((resolve) => {
    let raw = "";
    req.on("data", (c) => {
      raw += c;
      if (raw.length > 1e6) req.destroy();
    });
    req.on("end", () => {
      try {
        resolve(JSON.parse(raw || "{}"));
      } catch {
        resolve({});
      }
    });
  });
}

const server = http.createServer(async (req, res) => {
  const url = req.url || "/";
  if (req.method === "GET" && url === "/healthz") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ ok: true, stub: "touch-identity", port }));
    return;
  }
  const body = await readBody(req);
  if (req.method === "POST" && url === "/internal/v1/identity/login") {
    if (!body.email || !body.password) {
      res.writeHead(401, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "login_rejected" }));
      return;
    }
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(pair(body.email)));
    return;
  }
  if (req.method === "POST" && url === "/internal/v1/identity/refresh") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(pair("owner@e2e.test")));
    return;
  }
  if (req.method === "POST" && url === "/internal/v1/identity/revocations") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ revoked: true }));
    return;
  }
  if (req.method === "POST" && url === "/internal/v1/identity/session/resolve") {
    const token = String(body.session_token || "");
    if (!token.startsWith("sess-")) {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ authenticated: false }));
      return;
    }
    const email = Buffer.from(token.slice(5), "base64url").toString("utf8");
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ authenticated: true, app_id: "touch-engine", session: { principal_id: PRINCIPAL, email } }));
    return;
  }
  res.writeHead(404, { "content-type": "application/json" });
  res.end(JSON.stringify({ error: "not_found" }));
});

server.listen(port, "127.0.0.1", () => console.log(`[identity-stub] listening :${port}`));
