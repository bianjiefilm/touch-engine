import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

function walk(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const abs = path.join(dir, name);
    if (statSync(abs).isDirectory()) out.push(...walk(abs));
    else if (/\.(tsx|ts|css)$/.test(name)) out.push(abs);
  }
  return out;
}

const pages = [
  "app/page.tsx",
  "components/work/today-desk.tsx",
  "app/work/stores/page.tsx",
  "app/work/campaigns/page.tsx",
  "app/work/campaigns/[id]/page.tsx",
  "app/work/materials/page.tsx",
  "app/work/rewards/page.tsx",
  "app/work/analytics/page.tsx",
  "app/c/[code]/page.tsx",
  "app/c/[code]/public-campaign.tsx",
  "app/c/[code]/contact/page.tsx",
  "app/c/[code]/customer-publish.tsx",
];

describe("根页不是工程说明", () => {
  it("首页和今日台不写后台说明书，也不用内联样式", () => {
    const home = read("app/page.tsx");
    const desk = read("components/work/today-desk.tsx");
    const combined = home + "\n" + desk;
    expect(combined).not.toMatch(/平台生态|公共活动页|短码即入口|权限边界|touch-engine|独立应用|登录、门店、活动/);
    expect(combined).not.toMatch(/style=\{\{/);
    expect(desk).toContain("今天");
    expect(desk).toContain('href="/work/stores"');
    expect(desk).toContain('href="/work/campaigns"');
    expect(desk).toContain('href="/work/materials"');
    expect(desk).toContain('href="/work/rewards"');
    expect(desk).toContain('href="/work/analytics"');
  });
});

describe("代表页面是真实路由", () => {
  it("商家与顾客页面都存在，且这些页面没有内联样式", () => {
    for (const rel of pages) {
      const abs = path.join(root, rel);
      expect(existsSync(abs), rel).toBe(true);
      expect(read(rel), rel).not.toMatch(/style=\{\{/);
    }
  });

  it("活动、奖励、统计页面覆盖空、加载、错误和活动状态", () => {
    const campaign = read("app/work/campaigns/page.tsx");
    for (const state of ["empty", "loading", "error", "expired", "paused", "ended"]) {
      expect(campaign).toContain(`data-state="${state}"`);
    }
    expect(read("app/work/rewards/page.tsx")).toContain("tk-unknown");
    expect(read("app/work/analytics/page.tsx")).toContain("未知");
    expect(read("app/c/[code]/contact/page.tsx")).toContain("contact");
  });
});

describe("顾客页没有生态导航和平台宣传", () => {
  it("公共活动目录不引用生态顶栏，也不做平台自我宣传", () => {
    const files = walk(path.join(root, "app/c"));
    expect(files.length).toBeGreaterThan(0);
    for (const file of files) {
      const source = readFileSync(file, "utf8");
      expect(source, file).not.toMatch(/EcoTopNav|AdminShell|MerchantWorkbench|\/api\/workbench|平台生态|SaaS|独立应用|touch-engine|商家后台|生态导航/);
    }
  });

  it("未知奖励不走成功绿", () => {
    const source = read("app/c/[code]/customer-publish.tsx");
    expect(source).toContain("data-reward-tone");
    expect(source).toContain("tk-unknown");
    expect(source).not.toMatch(/#065f46|#059669|#16a34a/);
    const css = read("app/touch.css");
    expect(css).toMatch(/\.tk-unknown\s*\{[^}]*var\(--tk-warning\)/);
    expect(css).not.toMatch(/\.tk-unknown\s*\{[^}]*var\(--tk-ok\)/);
  });
});
