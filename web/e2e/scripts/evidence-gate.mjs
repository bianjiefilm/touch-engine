#!/usr/bin/env node
// HUI-2628 r2 E2E 证据汇总门（评审 A1）：playwright 结束后解析 JSON 报告，
// 防「BLOCKED 假绿」与「证据缺失也算过」：
//   1. stats.skipped > 0 → fail（skip 不是 PASS）；
//   2. 报告里任何用例非全 passed → fail；
//   3. 全量模式（run.sh 未带过滤参数）：25 张验收截图必须齐全（8 代表页 × 390/430/1440 + home-1920），
//      axe-summary.json 必须存在且 blocking=[]；
//   4. 收尾打印证据汇总：spec 数 / 用例数 / 截图计数 / axe critical+serious 数。
// 用法：node evidence-gate.mjs [--partial]（--partial 只盘点不设全集门）
import { existsSync, readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const evidenceDir = path.resolve(here, "..", "evidence");
const partial = process.argv.includes("--partial");

// 25 张验收截图全集（spec 06 的 ROUTES × widths + home-1920；r3 增 1440）
const EXPECTED_SHOTS = [
  "home-390", "home-430", "home-1440", "home-1920",
  "work-stores-390", "work-stores-430", "work-stores-1440",
  "work-campaign-detail-390", "work-campaign-detail-430", "work-campaign-detail-1440",
  "work-materials-390", "work-materials-430", "work-materials-1440",
  "work-rewards-390", "work-rewards-430", "work-rewards-1440",
  "work-analytics-390", "work-analytics-430", "work-analytics-1440",
  "public-campaign-390", "public-campaign-430", "public-campaign-1440",
  "public-contact-390", "public-contact-430", "public-contact-1440",
];

const failures = [];
const reportPath = path.join(evidenceDir, "playwright-report.json");
if (!existsSync(reportPath)) {
  console.error("[gate] 缺 evidence/playwright-report.json——playwright json reporter 未产出？");
  process.exit(1);
}
const report = JSON.parse(readFileSync(reportPath, "utf8"));
const stats = report.stats ?? {};

// 1+2. skip / 失败一律 fail
const skipped = Number(stats.skipped ?? 0);
if (skipped > 0) failures.push(`skipped=${skipped}（skip 不是 PASS——BLOCKED 必须红）`);

const suites = report.suites ?? [];
const specFiles = [];
let totalCases = 0;
const badSpecs = [];
for (const suite of suites) {
  if (suite.file) specFiles.push(suite.file);
  for (const spec of suite.specs ?? []) {
    for (const t of spec.tests ?? []) {
      totalCases += 1;
      const statuses = (t.results ?? []).map((r) => r.status);
      const ok = spec.ok === true && statuses.every((s) => s === "passed");
      if (!ok) badSpecs.push(`${suite.file ?? suite.title} :: ${spec.title} [${statuses.join(",") || "no-result"}]`);
    }
  }
}
for (const bad of badSpecs) failures.push(`未通过用例: ${bad}`);
if (totalCases === 0) failures.push("报告里没有任何用例——过滤参数写错或收集失败");

// 3. 截图与 axe（全量模式才要求全集；partial 模式（带过滤参数）只盘点不设门）
let axeBlocking = -1;
const onDisk = new Set(readdirSync(evidenceDir).filter((f) => f.endsWith(".png")).map((f) => f.replace(/\.png$/, "")));
const missing = EXPECTED_SHOTS.filter((name) => !onDisk.has(name));
if (!partial) {
  for (const name of missing) failures.push(`缺验收截图: ${name}.png`);
  const axePath = path.join(evidenceDir, "axe-summary.json");
  if (!existsSync(axePath)) {
    failures.push("缺 evidence/axe-summary.json（spec 06 未跑或未产出）");
  } else {
    axeBlocking = (JSON.parse(readFileSync(axePath, "utf8")).blocking ?? []).length;
    if (axeBlocking > 0) failures.push(`axe critical+serious blocking=${axeBlocking}（要求 0）`);
  }
}

// 4. 证据汇总
console.log("[gate] ---- 证据汇总 ----");
console.log(`[gate] spec 文件数: ${specFiles.length}（${specFiles.join(", ")}）`);
console.log(`[gate] 用例数: ${totalCases}，passed=${stats.expected ?? 0}，failed=${stats.unexpected ?? 0}，skipped=${skipped}`);
console.log(`[gate] 验收截图: ${EXPECTED_SHOTS.length - missing.length}/${EXPECTED_SHOTS.length} 在 evidence/（missing: ${missing.length ? missing.join(", ") : "无"}）`);
console.log(`[gate] axe critical+serious: ${axeBlocking < 0 ? "未盘点（partial）" : axeBlocking}`);
console.log("[gate] ----------------------");

if (failures.length > 0) {
  for (const f of failures) console.error(`[gate] FAIL: ${f}`);
  process.exit(1);
}
console.log("[gate] 证据门通过");
