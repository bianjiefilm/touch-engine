import { expect, test } from "@playwright/test";
import { seedCodes } from "../support/helpers";

// 公共页五态 + loading：available / expired / paused / ended / 无效码 404 态。
test("public page states", async ({ page }) => {
  const codes = await seedCodes();

  await page.goto(`/c/${codes.activeCode}`);
  await expect(page.locator('[data-testid="public-activity"][data-state="available"]')).toBeVisible();
  await expect(page.locator('[data-testid="store-identity"]')).toBeVisible();

  for (const [code, headline] of [
    [codes.expiredCode, /活动已结束/],
    [codes.pausedCode, /暂停/],
    [codes.endedCode, /已结束|已停用|结束/],
  ] as const) {
    await page.goto(`/c/${code}`);
    await expect(page.locator('[data-testid="degraded-state"]')).toBeVisible();
    await expect(page.getByRole("heading").first()).toContainText(headline);
  }

  await page.goto("/c/ZZZZZZZZ");
  await expect(page.locator('[data-testid="degraded-state"]')).toBeVisible();
});
