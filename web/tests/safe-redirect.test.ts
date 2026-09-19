import { describe, expect, it } from "vitest";
import { inSiteTargetFromParams, sanitizeInSitePath } from "../src/lib/safe-redirect";

// HUI-1664 重定向守卫矩阵:任何 next/redirect/return 参数只放行站内相对路径;
// 协议相对、绝对 URL、%编码、反斜杠、多重编码、脚本scheme 一律拒绝(返回
// null = 默认落地本活动页)。

describe("sanitizeInSitePath 拒绝矩阵", () => {
  const rejected: Array<string | null | undefined> = [
    "//evil.com",
    "//evil.com/path",
    "https://evil.com",
    "http://evil.com/x",
    "/%2F%2Fevil",
    "/%2f%2fevil",
    "/\\evil",
    "/a\\evil",
    "/%252F%252Fevil", // 双重编码
    "/c%2F..%2F..%2Fadmin",
    "/admin?next=x", // 查询字符串不允许
    "/admin#frag", // 片段不允许
    "javascript:alert(1)",
    "JavaScript:alert(1)",
    "data:text/html,evil",
    "  //evil.com  ", // 前后空白也不救
    "/UPPER/case",
    "/c/ABC123XYZ789", // 大写短码路径:白名单严格小写,一律拒绝(拍板矩阵)
    "/.dot./segments",
    "..",
    ".",
    "admin", // 无前导斜杠
    "",
    "   ",
    null,
    undefined,
  ];
  for (const raw of rejected) {
    it(`拒绝 ${JSON.stringify(raw)}`, () => {
      expect(sanitizeInSitePath(raw)).toBeNull();
    });
  }
});

describe("sanitizeInSitePath 放行矩阵", () => {
  const allowed = [
    "/", // 站内根
    "/c",
    "/c/abc123xyz789",
    "/admin",
    "/a/b/c_d-e",
    "/123",
  ];
  for (const raw of allowed) {
    it(`放行 ${JSON.stringify(raw)}`, () => {
      expect(sanitizeInSitePath(raw)).toBe(raw);
    });
  }
});

describe("inSiteTargetFromParams", () => {
  it("无跳转参数 → null(默认落地本活动页)", () => {
    expect(inSiteTargetFromParams(() => null)).toBeNull();
    expect(inSiteTargetFromParams((k) => (k === "entry" ? "qr" : null))).toBeNull();
  });

  it("空串参数视为不存在", () => {
    expect(inSiteTargetFromParams((k) => (k === "next" ? "" : null))).toBeNull();
  });

  it("合法站内路径放行", () => {
    expect(inSiteTargetFromParams((k) => (k === "next" ? "/admin" : null))).toBe("/admin");
    expect(inSiteTargetFromParams((k) => (k === "redirect" ? "/c" : null))).toBe("/c");
    expect(inSiteTargetFromParams((k) => (k === "return" ? "/a/b" : null))).toBe("/a/b");
  });

  it("绕过用例全部落回 null", () => {
    for (const key of ["next", "redirect", "return"]) {
      for (const bad of ["//evil.com", "https://evil.com", "/%2F%2Fevil", "/\\evil", "/%252F%252Fevil"]) {
        expect(inSiteTargetFromParams((k) => (k === key ? bad : null))).toBeNull();
      }
    }
  });
});
