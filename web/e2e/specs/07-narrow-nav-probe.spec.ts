import { expect, test } from "@playwright/test";
import { login } from "../support/helpers";

// HUI-2628 r3：390 碎片带回归探针（r2 known-issue 的判定收口）。
// 修复前 .context 子元素文本在按钮/span 内换行成两行（~40px），被 56px bar 裁切成碎片带。
// 本探针锁修复后事实：context 子元素单行（≤28px）、页面无横向溢出、bar 高 56px。
test("390 下 EcoTopNav context 单行、无横向溢出、bar 56px", async ({ page }) => {
  await login(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/work/stores");
  await page.waitForLoadState("networkidle").catch(() => undefined);
  const probe = await page.evaluate(() => {
    const bar = document.querySelector('[data-testid="eco-top-nav"]');
    const context = document.querySelector('[data-testid="eco-nav-context"]');
    if (!(bar instanceof HTMLElement) || !(context instanceof HTMLElement)) return null;
    return {
      scrollW: document.documentElement.scrollWidth,
      barH: bar.getBoundingClientRect().height,
      kidH: (Array.from(context.children) as HTMLElement[]).map((el) => el.getBoundingClientRect().height),
    };
  });
  expect(probe, "探针要求 eco-top-nav 与 eco-nav-context 都已渲染").not.toBeNull();
  expect(probe!.scrollW).toBe(390);
  expect(probe!.barH).toBe(56);
  for (const h of probe!.kidH) expect(h, "context 子元素必须单行").toBeLessThanOrEqual(28);
});

test("1440 下同一探针不受窄修影响", async ({ page }) => {
  await login(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/work/stores");
  await page.waitForLoadState("networkidle").catch(() => undefined);
  const probe = await page.evaluate(() => ({
    scrollW: document.documentElement.scrollWidth,
    barH: document.querySelector<HTMLElement>('[data-testid="eco-top-nav"]')?.getBoundingClientRect().height ?? 0,
  }));
  expect(probe.scrollW).toBe(1440);
  expect(probe.barH).toBe(56);
});
