import { createServer } from "node:http";
import { generateKeyPairSync, sign } from "node:crypto";

const { publicKey, privateKey } = generateKeyPairSync("ed25519");
const jwk = publicKey.export({ format: "jwk" });
const kid = "hui2222-test";

function b64url(value) {
  const buf = Buffer.isBuffer(value) ? value : Buffer.from(value);
  return buf.toString("base64url");
}

function mint(tenantId) {
  const now = Math.floor(Date.now() / 1000);
  const header = b64url(JSON.stringify({ alg: "EdDSA", typ: "JWT", kid }));
  const payload = b64url(
    JSON.stringify({
      sub: "usr_hui2222",
      account_id: "acct_hui2222",
      tenant_id: tenantId || "tnt_hui2222",
      typ: "access",
      aud: "product-image-engine",
      iat: now,
      exp: now + 600,
      roles: ["member"],
    }),
  );
  const data = `${header}.${payload}`;
  const sig = sign(null, Buffer.from(data), privateKey);
  return `${data}.${b64url(sig)}`;
}

const server = createServer((req, res) => {
  if (req.method === "GET" && req.url === "/.well-known/jwks.json") {
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ keys: [{ kty: "OKP", crv: "Ed25519", use: "sig", alg: "EdDSA", kid, x: jwk.x }] }));
    return;
  }
  if (req.method === "POST" && req.url === "/test/mint") {
    let raw = "";
    req.on("data", (chunk) => {
      raw += chunk;
    });
    req.on("end", () => {
      const body = raw ? JSON.parse(raw) : {};
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify({ access_token: mint(body.tenant_id), token_type: "Bearer" }));
    });
    return;
  }
  res.statusCode = 404;
  res.end();
});

server.listen(18222, "127.0.0.1", () => {
  console.log("test-identity-stub listening on 127.0.0.1:18222");
});
