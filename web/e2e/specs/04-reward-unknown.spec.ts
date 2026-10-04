import { expect, test } from "@playwright/test";
import { seedCodes } from "../support/helpers";

// 未知奖励不得显示成功绿（#065f46 = rgb(6, 95, 70)）。
test("unknown reward never shows success green", async ({ page }) => {
  const codes = await seedCodes();
  await page.goto(`/c/${codes.activeCode}`);
  await page.locator('[data-testid="public-activity"][data-state="available"]').waitFor();
  const rewardZone = page.locator("main");
  const okCount = await rewardZone.locator(".tk-ok").count();
  // 顾客未提交留资、奖励状态未知：主内容不得挂 tk-ok
  expect(okCount).toBe(0);
  const color = await rewardZone.evaluate((el) => getComputedStyle(el).color);
  expect(color).not.toBe("rgb(6, 95, 70)");
});
