import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

describe("Motion Handoff Brief 只读展示", () => {
  const brief = read("components/work/motion-handoff-brief.tsx");
  const page = read("app/work/campaigns/[id]/page.tsx");

  it("页面挂载 brief 且在门店参数区块之后", () => {
    expect(page).toContain('from "@/components/work/motion-handoff-brief"');
    expect(page).toContain("<MotionHandoffBrief");
    const storeAt = page.indexOf('id="store-motion"');
    const handoffAt = page.indexOf("<MotionHandoffBrief");
    expect(storeAt).toBeGreaterThan(-1);
    expect(handoffAt).toBeGreaterThan(storeAt);
  });

  it("拉取只读端点并明示上游不可用", () => {
    expect(brief).toMatch(/session\.api\(\s*"GET",\s*`campaigns\/\$\{campaignId\}\/motion-handoff`\s*\)/);
    expect(brief).toContain('data-field="disposition"');
    expect(brief).toContain("上游 Motion 消费不可用（");
    expect(brief).toContain("upstream_unavailable");
    expect(brief).toContain("digest.slice(-8)");
    expect(brief).toContain("这里只是交给上游的输入，不是生成结果");
  });

  it("拒绝把非 upstream_unavailable 的返回当真", () => {
    expect(brief).toContain("motion_consumable !== false");
    expect(brief).toContain('reason_code !== "upstream_unavailable"');
  });

  it("失败显示错误态，绝不显示生成或成功", () => {
    expect(brief).toContain('data-state="error"');
    expect(brief).toContain("tk-danger");
    expect(brief).not.toContain("已生成");
    expect(brief).not.toContain("生成成功");
    expect(brief).not.toContain("tk-ok");
    expect(brief).not.toContain('type="number"');
  });

  it("brand 上下文不可用时按原样显示原因，不显示成功", () => {
    expect(brief).toContain("context_available");
    expect(brief).toContain("context_unavailable_reason");
  });
});
