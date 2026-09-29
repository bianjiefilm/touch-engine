import { describe, expect, it } from "vitest";
import { copyJobClosed, copyJobKey, copyUsability, handoffHasFormalJump } from "../src/lib/copy-draft";

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

describe("文案任务收口", () => {
  it("没有真实完成时不能标成成功文案", () => {
    const view = copyJobClosed({ real_generation: "incomplete", success: false });
    expect(view.closed).toBe(true);
    expect(view.label).toContain("真实生成未完成");
    expect(copyJobClosed({ real_generation: "completed", success: false }).closed).toBe(true);
  });

  it("只有完成且成功才离开失败关闭", () => {
    const view = copyJobClosed({ real_generation: "completed", success: true });
    expect(view.closed).toBe(false);
    expect(view.label).not.toContain("真实生成未完成");
  });

  it("同一份资料得到同一个请求键，改资料就换键", () => {
    const left = copyJobKey("cmp_1", "价格过期");
    expect(copyJobKey("cmp_1", "价格过期")).toBe(left);
    expect(copyJobKey("cmp_1", "价格已确认")).not.toBe(left);
    expect(left).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(left.length).toBeLessThanOrEqual(80);
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
