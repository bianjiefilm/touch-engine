import { expect, test } from "@playwright/test";
import { login } from "../support/helpers";

// 商家第一眼：登录会话后根页=今日台真实数据，且知道要做什么。
test("merchant first entry shows today desk with real data", async ({ page }) => {
  await login(page);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "今天" })).toBeVisible();
  // 种子数据：1 家门店 + 4 场活动（active/expired/paused/ended）。
  // planToday（web/src/lib/product-finish.ts）对这些种子至少产出 4 条活动项
  // （paused/expired/live/ended 各一），另加素材/留资/奖励的未知项——所以断言
  // 列表非空且四条活动项全部可见，不断言会随 workbench 输出漂移的总数。
  const list = page.locator("ol.tk-list li");
  await expect(list.first()).toBeVisible({ timeout: 15_000 });
  // 同一首项会同时出现在 tk-lead（items[0] 摘要）与列表里，所以断言收窄到列表内
  await expect(list.getByText(/处理暂停的活动「E2E 暂停活动」/)).toBeVisible();
  // 过期场必须被写成"窗口已过"，不得写成进行中（HUI-2628 行为红线）
  await expect(list.getByText(/窗口已过，不要当成今天还在进行/)).toBeVisible();
  await expect(list.getByText(/今天进行中：E2E 进行中活动/)).toBeVisible();
  await expect(list.getByText(/「E2E 结束活动」已结束，不要当成还在进行/)).toBeVisible();
  // 下一步交接区：不把过期活动当交接对象
  await expect(page.getByText(/获客和内容从当前活动交接/).or(page.getByText(/没有还在窗口里的活动/))).toBeVisible();
  const pageText = await page.locator("main").innerText();
  expect(pageText).not.toContain("E2E 过期活动交接");
});
