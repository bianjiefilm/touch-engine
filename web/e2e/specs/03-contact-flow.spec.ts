import { expect, test } from "@playwright/test";
import { seedCodes } from "../support/helpers";

// 留资提交成功态（待同步）与撤销（已撤销）。
// 现场 DOM（web/src/app/c/[code]/public-campaign.tsx）：留资 lane 是独立路由
// /c/{code}/contact（lane="contact" 才渲染 lead-disclosure）；输入 placeholder
// 是「怎么称呼你」「商家用来联系你」。
test("contact flow submit then revoke", async ({ page }) => {
  const codes = await seedCodes();
  await page.goto(`/c/${codes.activeCode}/contact`);
  await page.locator('[data-testid="public-activity"][data-state="available"]').waitFor();
  await page.locator('[data-testid="lead-disclosure"]').waitFor();
  await page.locator('[data-testid="consent"]').check();
  await page.getByPlaceholder("怎么称呼你").fill("测试顾客");
  await page.getByPlaceholder("商家用来联系你").fill("13800138000");
  await page.locator('[data-testid="lead-submit"]').click();
  const outcome = page.locator('[data-testid="lead-outcome"]');
  await expect(outcome).toBeVisible();
  // toHaveAttribute 的属性名参数不接受 RegExp（计划伪码笔误）；元素 data-state/data-tone 同值，双双断言
  await expect(outcome).toHaveAttribute("data-state", "pending");
  await expect(outcome).toHaveAttribute("data-tone", "pending");
  await outcome.getByRole("button", { name: /撤销我的授权/ }).click();
  await expect(outcome).toHaveAttribute("data-state", "revoked", { timeout: 15_000 });
  await expect(outcome).toHaveAttribute("data-tone", "revoked", { timeout: 15_000 });
  await expect(outcome.getByText(/已撤销/)).toBeVisible();
});
