import { describe, expect, it } from "vitest";
import { copyUsability, handoffHasFormalJump } from "../src/lib/copy-draft";

describe("文案可用性", () => {
  it("未授权模型不能标成真实可用", () => {
    const view = copyUsability({ usable: false, model_status: "not_authorized", billed: false });
    expect(view.usable).toBe(false);
    expect(view.label).toContain("不是真实可用文案");
  });

  it("usable 为真但模型未授权时仍然不可用", () => {
    const view = copyUsability({ usable: true, model_status: "not_authorized", billed: false });
    expect(view.usable).toBe(false);
  });

  it("扣费标记不能被展示成已完成文案", () => {
    const view = copyUsability({ usable: true, model_status: "authorized", billed: true });
    expect(view.usable).toBe(false);
    expect(view.label).toContain("扣费");
  });
});

describe("专业工具交接", () => {
  it("不把 .invalid 或本机地址当成正式跳转", () => {
    expect(handoffHasFormalJump({ store_name: "江边小馆", asset_ids: ["ast_1"] })).toBe(false);
    expect(handoffHasFormalJump({ launch_url: "https://tools.example.invalid/open" })).toBe(true);
    expect(handoffHasFormalJump({ launch_url: "http://127.0.0.1:18340/admin" })).toBe(true);
    expect(handoffHasFormalJump({ launch_url: "http://localhost/go" })).toBe(true);
  });
});
