import { describe, expect, it } from "vitest";
import { presentDeclaration } from "../src/lib/store-motion-status";

describe("declaration 状态不能照画", () => {
  it("真实未成片状态是 pending", () => {
    const view = presentDeclaration("还没生成成片");
    expect(view.tone).toBe("pending");
    expect(view.copy).toBe("还没生成成片");
  });

  it("空与缺失是未知，不是 pending", () => {
    expect(presentDeclaration(undefined).tone).toBe("unknown");
    expect(presentDeclaration(undefined).copy).toBe("状态未知（服务端还没给出声明）");
    expect(presentDeclaration("").tone).toBe("unknown");
    expect(presentDeclaration("").copy).toBe("状态未知（服务端还没给出声明）");
  });

  it("伪造状态字符串一律 unknown", () => {
    for (const forged of ["已生成", "done", "rendered", "SUCCESS", "还没生成成片 "]) {
      const view = presentDeclaration(forged);
      expect(view.tone).toBe("unknown");
      expect(view.copy).toBe("状态未知（出现未识别状态）");
    }
  });

  it("任何输入都不产生成功语气", () => {
    for (const raw of [undefined, "", "还没生成成片", "已生成"]) {
      const view = presentDeclaration(raw);
      expect(["pending", "unknown"]).toContain(view.tone);
      expect(view.copy).not.toContain("已生成");
      expect(view.copy).not.toContain("成功");
    }
  });
});
