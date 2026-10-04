import { mkdirSync } from "node:fs";
import path from "node:path";
import { expect, type Page } from "@playwright/test";

const EVIDENCE = path.resolve(__dirname, "..", "evidence");

export const OWNER = { email: "owner@e2e.test", password: "e2e-pass-123", tenantName: "磁石科技" };

/** 从 evidence/seed.json 读 env.mjs 写入的 seed 短码/租户 ID。 */
export async function seedCodes(): Promise<Record<string, string>> {
  const data = await import("node:fs/promises").then((f) => f.readFile(path.join(EVIDENCE, "seed.json"), "utf8"));
  return JSON.parse(data) as Record<string, string>;
}

/**
 * 走 /admin 登录表单建立商家会话。
 * 真实表单是三字段（邮箱/密码/租户 ID，web/src/app/admin/page.tsx）：
 * handleLogin 在 res.ok 且 tenantId 非空时直接 refresh()，所以三个字段一次填齐、一次提交。
 */
export async function login(page: Page): Promise<void> {
  const seed = await seedCodes();
  if (!seed.tenantId) throw new Error("[helpers] seed.json 缺 tenantId（env.mjs seed 未写入？）");
  await page.goto("/admin");
  await page.getByPlaceholder("平台账号邮箱").fill(OWNER.email);
  await page.getByPlaceholder("密码", { exact: true }).fill(OWNER.password);
  await page.getByPlaceholder(/租户 ID/).fill(seed.tenantId);
  await page.getByRole("button", { name: "登录" }).click();
  // 登录视图是 main.tk-admin-narrow；会话建立后渲染 AdminShell 的 main.tk-admin-shell
  await expect(page.locator("main.tk-admin-shell")).toBeVisible({ timeout: 15_000 });
}

/** 全页截图统一落 evidence/，文件名 = name.png。 */
export async function screenshot(page: Page, name: string): Promise<string> {
  mkdirSync(EVIDENCE, { recursive: true });
  const file = path.join(EVIDENCE, `${name}.png`);
  await page.screenshot({ path: file, fullPage: true });
  return file;
}
