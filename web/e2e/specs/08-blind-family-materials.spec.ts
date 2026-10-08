import { expect, test } from "@playwright/test";
import { login, screenshot, seedCodes } from "../support/helpers";

// HUI-2628 r3：去 Logo 产品族材料（供 HUI-2625 blind family review）。
// 只产出材料，不产出结论：verdict 由 Gate 所有者填写（本票无权代填）。
// 屏蔽位 = EcoTopNav 的品牌/当前应用/付款主体/代理来源横幅 + 右侧账户昵称。
const MASK_STYLE = `
  [data-testid="eco-brand"], [data-testid="eco-current-app"], [data-testid="eco-payer"],
  [data-testid="eco-provenance"], [data-testid="eco-agent-banner"],
  [data-testid^="eco-tenant-"],
  [data-testid="eco-top-nav"] button[aria-label^="账户"]
  { visibility: hidden !important; }
`;

const TARGETS: Array<{ slug: string; path: (seed: Record<string, string>) => string }> = [
  { slug: "blind-home", path: () => "/" },
  { slug: "blind-work-stores", path: () => "/work/stores" },
  { slug: "blind-work-campaign-detail", path: (seed) => `/work/campaigns/${seed.activeId ?? ""}` },
  { slug: "blind-work-rewards", path: () => "/work/rewards" },
];

test("blind family 去品牌截图（1440，商家四页）", async ({ page }) => {
  await login(page);
  const seed = await seedCodes();
  await page.setViewportSize({ width: 1440, height: 900 });
  for (const target of TARGETS) {
    await page.goto(target.path(seed));
    await page.waitForLoadState("networkidle").catch(() => undefined);
    await page.addStyleTag({ content: MASK_STYLE });
    await screenshot(page, target.slug);
  }
  expect(TARGETS.length).toBe(4);
});
