import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { login, screenshot, seedCodes } from "../support/helpers";

type RouteDef = { slug: string; path: string; auth: boolean };

// 8 代表页（=MerchantHome、/work/stores、/work/campaigns/[id]、/work/materials、
// /work/rewards、/work/analytics、/c/[code]、/c/[code]/contact）× 390/430 截图 + axe；1920 商家首页 1 张。
const ROUTES: RouteDef[] = [
  { slug: "home", path: "/", auth: true },
  { slug: "work-stores", path: "/work/stores", auth: true },
  { slug: "work-campaign-detail", path: "", auth: true }, // 运行时用 seed active 活动 id 拼 /work/campaigns/{id}
  { slug: "work-materials", path: "/work/materials", auth: true },
  { slug: "work-rewards", path: "/work/rewards", auth: true },
  { slug: "work-analytics", path: "/work/analytics", auth: true },
  { slug: "public-campaign", path: "", auth: false }, // /c/{activeCode}
  { slug: "public-contact", path: "", auth: false }, // /c/{activeCode}/contact
];

async function resolvePath(def: RouteDef, campaignId: string, activeCode: string): Promise<string> {
  if (def.slug === "work-campaign-detail") return `/work/campaigns/${campaignId}`;
  if (def.slug === "public-campaign") return `/c/${activeCode}`;
  if (def.slug === "public-contact") return `/c/${activeCode}/contact`;
  return def.path;
}

test("a11y + screenshots across 8 representative pages", async ({ page }) => {
  // 浏览器不可用必须红（评审 A1）：不再 test.skip——skip 会被 playwright 判 exit 0 造成假绿。
  await login(page);
  const codes = await seedCodes();

  const results: Array<{ slug: string; width: number; violations: Array<{ id: string; impact: string | null }> }> = [];
  const widths = [390, 430];
  for (const def of ROUTES) {
    for (const width of widths) {
      await page.setViewportSize({ width, height: 900 });
      const target = await resolvePath(def, codes.activeId ?? "", codes.activeCode);
      await page.goto(target);
      await page.waitForLoadState("networkidle").catch(() => undefined);
      const axe = await new AxeBuilder({ page }).analyze();
      const serious = axe.violations.filter((v) => v.impact === "critical" || v.impact === "serious");
      for (const v of serious) {
        results.push({ slug: def.slug, width, violations: [{ id: v.id, impact: v.impact ?? null }] });
      }
      await screenshot(page, `${def.slug}-${width}`);
    }
  }

  // 1920 商家首页一张
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto("/");
  await screenshot(page, "home-1920");

  // 验收门：critical/serious 违规阻断
  expect(results).toEqual([]);
  mkdirSync(path.resolve(__dirname, "..", "evidence"), { recursive: true });
  writeFileSync(
    path.resolve(__dirname, "..", "evidence", "axe-summary.json"),
    JSON.stringify({ blocking: results, note: "moderate 及以下见 playwright json 报告" }),
    "utf8",
  );
});
