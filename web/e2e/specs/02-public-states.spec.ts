import { expect, test } from "@playwright/test";
import { seedCodes } from "../support/helpers";

// 公共页五态 + loading：available / expired / paused / ended / 无效码 404 态 / loading。
// data-state 落在 main[data-testid="public-activity"]（web/src/app/c/[code]/public-campaign.tsx:507），
// 值 = Go ResolveLink machine state（server/internal/store/store.go:533-540）。
// expired 与 ended 文案相同（都是「活动已结束」），区分靠 data-state 精确断言。
test("public page states", async ({ page }) => {
  const codes = await seedCodes();

  await page.goto(`/c/${codes.activeCode}`);
  await expect(page.locator('[data-testid="public-activity"][data-state="available"]')).toBeVisible();
  await expect(page.locator('[data-testid="store-identity"]')).toBeVisible();

  for (const [code, state, headline] of [
    [codes.expiredCode, "expired", /活动已结束/],
    [codes.pausedCode, "paused", /暂停/],
    [codes.endedCode, "ended", /已结束|已停用|结束/],
  ] as const) {
    await page.goto(`/c/${code}`);
    await expect(page.locator(`[data-testid="public-activity"][data-state="${state}"]`)).toBeVisible();
    await expect(page.locator('[data-testid="degraded-state"]')).toBeVisible();
    await expect(page.getByRole("heading").first()).toContainText(headline);
  }

  await page.goto("/c/ZZZZZZZZ");
  await expect(page.locator('[data-testid="public-activity"][data-state="not_found"]')).toBeVisible();
  await expect(page.locator('[data-testid="degraded-state"]')).toBeVisible();
});

// loading 态：拦下公共接口使请求悬置，data-state 停在 "loading"（fetch 尚未 resolve）。
test("public page loading state while link fetch is pending", async ({ page }) => {
  const codes = await seedCodes();
  await page.route("**/api/public/links/*", () => new Promise<void>(() => undefined));
  await page.goto(`/c/${codes.activeCode}`);
  await expect(page.locator('main[data-testid="public-activity"][data-state="loading"]')).toBeVisible();
});
