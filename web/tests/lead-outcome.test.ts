import { describe, expect, it } from "vitest";
import { LEAD_REVOKED_COPY, leadOutcomeState } from "@/lib/lead-outcome";

describe("leadOutcomeState 四态", () => {
  it("待同步 → pending（页面真实 pending 文案）", () => {
    expect(leadOutcomeState("已提交给这家店。待同步。对方客户系统还没确认收到。你可以在本页撤销授权。")).toBe("pending");
  });

  it("销售已收到 → received", () => {
    expect(leadOutcomeState("销售已收到 3")).toBe("received");
  });

  it("撤销文案 → revoked", () => {
    expect(leadOutcomeState(LEAD_REVOKED_COPY)).toBe("revoked");
  });

  it("其他 → recorded", () => {
    expect(leadOutcomeState("已记录，等待商家处理。")).toBe("recorded");
  });

  it("待同步优先于已撤销（顺序判定，与裁决一致）", () => {
    expect(leadOutcomeState("已撤销。待同步的数据不再移交。")).toBe("pending");
  });

  it("撤销文案不含「待同步」，不会被误判成 pending", () => {
    expect(LEAD_REVOKED_COPY.includes("待同步")).toBe(false);
  });
});
