import { describe, expect, it } from "vitest";
import { STORE_NOTICE_TEXT, storeNoticeText } from "../src/lib/public-state";

// HUI-1674:门店停用标注映射 —— 白名单内给出固定文案,其余一律空串。
describe("storeNoticeText", () => {
  it("maps the whitelisted notice to fixed copy", () => {
    expect(storeNoticeText("store_unavailable")).toBe(STORE_NOTICE_TEXT["store_unavailable"]);
    expect(storeNoticeText("store_unavailable")).toContain("门店暂不可用");
  });

  it("returns empty string for unknown, empty or missing notices", () => {
    expect(storeNoticeText(undefined)).toBe("");
    expect(storeNoticeText(null)).toBe("");
    expect(storeNoticeText("")).toBe("");
    expect(storeNoticeText("store_ok")).toBe("");
    expect(storeNoticeText("STORE_UNAVAILABLE")).toBe(""); // 大小写敏感,防伪造扩展
  });

  it("never leaks non-whitelisted values through", () => {
    const text = storeNoticeText("sto_1234567890abcdef");
    expect(text).toBe("");
  });
});
