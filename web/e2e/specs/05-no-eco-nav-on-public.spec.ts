import { expect, test } from "@playwright/test";
import { seedCodes } from "../support/helpers";

// 公共页无生态导航；商家页允许有（对照组防误伤）。
test("no eco nav on public page", async ({ page }) => {
  const codes = await seedCodes();
  await page.goto(`/c/${codes.activeCode}`);
  await expect(page.locator('[data-testid="eco-top-nav"]')).toHaveCount(0);
  await expect(page.locator('[data-testid^="eco-app-"]')).toHaveCount(0);

  await page.goto("/work/stores");
  await expect(page.locator('[data-testid="eco-top-nav"]')).toHaveCount(0); // touch 商家壳也不用 EcoTopNav
});
