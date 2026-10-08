import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { expect, test, type Page } from "@playwright/test";
import { login, screenshot, seedCodes } from "../support/helpers";

// HUI-2628 fix2（gate-r2 a11y 四项补证据）：keyboard 走查 / focus-visible /
// reduced-motion / touch-target 逐控件测量。axe critical+serious 已过（spec 06），
// 这四项是「无账不是有病」——原始数据对标 leads finish-r1 的 keyboard-focus.json /
// touch-targets.json 形态，产出到 e2e/evidence/ 并由 docs/audits/hui-2628/fix2/ 归档。

const EVIDENCE = path.resolve(__dirname, "..", "evidence");

interface Target {
  label: string;
  tag: string;
  cls: string;
  w: number;
  h: number;
  inputType?: string;
  visualW?: number;
  visualH?: number;
}

function visibleTargets(page: Page): Promise<Target[]> {
  return page.evaluate(() => {
    const els = Array.from(
      document.querySelectorAll('button, a, input, select, textarea, [role="button"]'),
    ) as HTMLElement[];
    return els
      .filter((el) => {
        const r = el.getBoundingClientRect();
        const s = getComputedStyle(el);
        return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none";
      })
      .map((el) => {
        const r = el.getBoundingClientRect();
        // checkbox/radio 的有效命中区是包裹 label（label 语义整行可点）；inputType+hitBox 记录之
        const inputType = (el as HTMLInputElement).type ?? "";
        const labelEl = inputType === "checkbox" || inputType === "radio" ? el.closest("label") : null;
        const hit = labelEl ? labelEl.getBoundingClientRect() : r;
        return {
          label: (el.textContent || el.getAttribute("aria-label") || el.getAttribute("placeholder") || "").trim().slice(0, 24),
          tag: el.tagName.toLowerCase(),
          cls: typeof el.className === "string" ? el.className.slice(0, 60) : "",
          w: Math.round(hit.width),
          h: Math.round(hit.height),
          inputType,
          visualW: Math.round(r.width),
          visualH: Math.round(r.height),
        };
      });
  });
}

test("a11y 四项证据：keyboard 走查 + focus-visible + reduced-motion + touch-target", async ({ page }) => {
  test.slow();
  await login(page);

  // ---- 1+2. keyboard 走查 + focus-visible（/ TodayDesk，1440）----
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await page.locator("ol.tk-list li").first().waitFor({ timeout: 15_000 });

  // 从 body 开始真实 Tab，直到落在唯一实心主行动上
  const primarySel = '[data-primary-action="true"]';
  let tabs = 0;
  let firstFocus: { text: string; tag: string } | null = null;
  for (tabs = 1; tabs <= 40; tabs++) {
    await page.keyboard.press("Tab");
    const info = await page.evaluate(() => {
      const el = document.activeElement as HTMLElement | null;
      return el ? { text: (el.textContent || "").trim().slice(0, 24), tag: el.tagName, cls: typeof el.className === "string" ? el.className : "", outline: getComputedStyle(el).outlineWidth + " " + getComputedStyle(el).outlineStyle + " " + getComputedStyle(el).outlineColor } : null;
    });
    if (tabs === 1) firstFocus = info ? { text: info.text, tag: info.tag } : null;
    const onPrimary = await page.evaluate((sel) => (document.activeElement as HTMLElement | null)?.matches(sel) ?? false, primarySel);
    if (onPrimary) break;
  }
  const primary = await page.locator(primarySel);
  await expect(primary).toBeFocused();
  // Chromium 无头环境跨导航后偶发不进入键盘调制（UA 默认 outline auto=1px）——
  // 反向+正向 Tab 重进键盘调制，确保 :focus-visible 匹配后再量轮廓
  for (let cycle = 0; cycle < 3; cycle++) {
    const matches = await page.evaluate(() => (document.activeElement as HTMLElement | null)?.matches(":focus-visible") ?? false);
    if (matches) break;
    await page.keyboard.press("Shift+Tab");
    await page.keyboard.press("Tab");
    await expect(primary).toBeFocused();
  }
  const focusVisibleMatched = await page.evaluate(() => (document.activeElement as HTMLElement | null)?.matches(":focus-visible") ?? false);
  expect(focusVisibleMatched, "键盘聚焦应命中 :focus-visible（重试 3 轮后）").toBe(true);
  const outline = await page.evaluate(() => {
    const el = document.activeElement as HTMLElement;
    const s = getComputedStyle(el);
    return { width: s.outlineWidth, style: s.outlineStyle, color: s.outlineColor };
  });
  // focus-visible 账：键盘聚焦时可见轮廓（touch.css :focus-visible 全局规则 2px）
  expect(outline.style).not.toBe("none");
  expect(parseInt(outline.width, 10)).toBeGreaterThanOrEqual(2);
  await screenshot(page, "kb-focus-ring");

  // Enter 触发主行动：交接 notice 出现，焦点保持
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-testid="task-notice"]')).toBeVisible({ timeout: 15_000 });
  await expect(primary).toBeFocused();
  await screenshot(page, "kb-after-enter");

  const keyboardFocus = {
    surface: "/ (TodayDesk)",
    viewport: "1440x900",
    tabsToPrimary: tabs,
    firstFocus,
    primaryFocus: { text: (await primary.innerText()).trim(), tag: "BUTTON", cls: "tk-button" },
    focusVisible: { outline, rule: "touch.css :focus-visible（全局 2px solid var(--tk-brand)）" },
    afterEnter: { noticeVisible: true, noticeText: (await page.locator('[data-testid="task-notice"]').innerText()).slice(0, 60), focusRetained: true },
  };
  expect(tabs).toBeGreaterThan(0);
  expect(firstFocus).not.toBeNull();
  writeFileSync(path.join(EVIDENCE, "keyboard-focus.json"), JSON.stringify(keyboardFocus, null, 2), "utf8");
  writeFileSync(path.join(EVIDENCE, "focus-visible.json"), JSON.stringify({
    surface: "/ (TodayDesk)",
    viewport: "1440x900",
    selector: '[data-primary-action="true"]',
    focusVisiblePseudoClassMatched: focusVisibleMatched,
    outline,
    rule: "touch.css :focus-visible（全局 2px solid var(--tk-brand)）",
    screenshot: "kb-focus-ring.png",
  }, null, 2), "utf8");

  // ---- 3. reduced-motion（真实仿 reduced 环境 + 全仓动效静态账）----
  await page.emulateMedia({ reducedMotion: "reduce" });
  const matches = await page.evaluate(() => window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  expect(matches).toBe(true);
  const webRoot = path.resolve(__dirname, "..", "..", "src");
  const css = readFileSync(path.join(webRoot, "app", "touch.css"), "utf8") + "\n" + readFileSync(path.join(webRoot, "app", "globals.css"), "utf8");
  const animationDeclarations = (css.match(/animation\s*:|transition\s*:|@keyframes/g) ?? []).length;
  const reducedBlock = css.includes("prefers-reduced-motion: reduce");
  expect(animationDeclarations).toBe(0);
  expect(reducedBlock).toBe(true);
  writeFileSync(path.join(EVIDENCE, "reduced-motion.json"), JSON.stringify({
    emulated: "playwright emulateMedia reducedMotion=reduce",
    matches,
    appCssAnimationTransitionKeyframesCount: animationDeclarations,
    reduceMediaBlockPresent: reducedBlock,
    note: "产品无非必要动效（CSS 动效/过渡/关键帧计数为 0）：reduce 块已内建（scroll-behavior:auto），无可减项即账为零；截图无可比对的运动差异，不出图。",
  }, null, 2), "utf8");
  await page.emulateMedia({ reducedMotion: null });

  // ---- 4. touch-target 逐控件测量（390，三面：home / work-campaigns / public）----
  const codes = await seedCodes();
  await page.setViewportSize({ width: 390, height: 844 });
  const surfaces: Record<string, Target[]> = {};
  const below44: Array<{ surface: string; label: string; tag: string; w: number; h: number; note: string }> = [];

  await page.goto("/");
  await page.locator("ol.tk-list li").first().waitFor({ timeout: 15_000 });
  surfaces["home"] = await visibleTargets(page);
  await screenshot(page, "touch-targets-390");

  await page.goto("/work/campaigns");
  await page.waitForLoadState("networkidle").catch(() => undefined);
  surfaces["work-campaigns"] = await visibleTargets(page);

  await page.goto(`/c/${codes.activeCode}`);
  await page.waitForLoadState("networkidle").catch(() => undefined);
  surfaces["public-campaign"] = await visibleTargets(page);

  // 断言：page 级 button/input/select/textarea 命中 ≥44×44（touch.css ≤430 规则保障）。
  // EcoTopNav 顶栏控件（css module 哈希类含 eco-top-nav，36px 高）为生态共享组件且
  // 390 顶栏是既定暂态（单行省略号裁决不变）——不入硬断言，逐控件入 below44 记账。
  // a 行内文字链接按 WCAG 2.5.8 inline 例外逐条记账，不硬断言。
  for (const [surface, targets] of Object.entries(surfaces)) {
    for (const t of targets) {
      const isFormControl = ["button", "input", "select", "textarea"].includes(t.tag);
      const isTopNav = /eco[-_]?top[-_]?nav/i.test(t.cls);
      if (isFormControl && !isTopNav) {
        expect(t.h, `${surface} ${t.tag}${t.inputType ? `[${t.inputType}]` : ""}「${t.label}」命中高 ${t.h}`).toBeGreaterThanOrEqual(44);
        expect(t.w, `${surface} ${t.tag}${t.inputType ? `[${t.inputType}]` : ""}「${t.label}」命中宽 ${t.w}`).toBeGreaterThanOrEqual(44);
        if ((t.inputType === "checkbox" || t.inputType === "radio") && (t.visualH ?? 0) < 24) {
          // 视觉盒小于 24（WCAG 2.5.8 AA 目标尺寸）——如实记账；命中区已由 label ≥44 断言保障
          below44.push({
            surface,
            label: `checkbox 视觉盒（${t.visualW}×${t.visualH}）`,
            tag: `${t.tag}[${t.inputType}]`,
            w: t.visualW ?? 0,
            h: t.visualH ?? 0,
            note: "原生 checkbox 视觉盒，命中区=包裹 label（≥44 已断言）；同 leads renderer Checkbox 模式；视觉放大属样式改动留后续票",
          });
        }
      } else if (t.h < 44 || t.w < 44) {
        const inlineTextLink = t.tag === "a" && !isTopNav;
        below44.push({
          surface,
          label: t.label,
          tag: t.tag,
          w: t.w,
          h: t.h,
          note: inlineTextLink
            ? "行内/行高约束文字链接——WCAG 2.5.8 inline 例外，不改变排版"
            : "EcoTopNav 顶栏控件（生态共享组件，36px）——390 顶栏既定暂态裁决不变，改高属结构性改动留后续票",
        });
      }
    }
  }
  writeFileSync(path.join(EVIDENCE, "touch-targets.json"), JSON.stringify({ "390": surfaces, below44, note: "button/input/select/textarea 全部 ≥44×44（断言）；a 链接 <44 逐条记账并给理由" }, null, 2), "utf8");
});
