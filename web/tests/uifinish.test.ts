import { rmSync, writeFileSync } from "node:fs";
import { readdirSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
  computedLayoutAllows,
  FILE_LEVEL_RULES,
  scanFiles,
  scanSource,
  type RuleId,
} from "@/lib/uifinish";

const WEB_ROOT = process.cwd();

// vitest 以 web/ 为 cwd；清单 = 8 代表页源文件 + 三个目录的全部 .tsx
const REP_FILES = [
  "src/app/page.tsx",
  "src/app/work/stores/page.tsx",
  "src/app/work/campaigns/[id]/page.tsx",
  "src/app/work/materials/page.tsx",
  "src/app/work/rewards/page.tsx",
  "src/app/work/analytics/page.tsx",
  "src/app/c/[code]/public-campaign.tsx",
  "src/app/c/[code]/contact/page.tsx",
];
const REP_DIRS = ["src/components/work", "src/components/admin", "src/app/admin"];

function collectScanTargets(): string[] {
  const files = REP_FILES.map((rel) => path.join(WEB_ROOT, rel));
  for (const dir of REP_DIRS) {
    const abs = path.join(WEB_ROOT, dir);
    for (const rel of readdirSync(abs, { recursive: true }) as string[]) {
      if (rel.endsWith(".tsx")) files.push(path.join(abs, rel));
    }
  }
  return files;
}

function ruleIds(src: string, kind: "fragment" | "main" = "fragment"): Set<RuleId> {
  return new Set(scanSource(src, kind).map((f) => f.ruleId));
}

describe("uifinish 12 规则逐条反例（能红）与正例（不误伤）", () => {
  it("1 inline_style：无标记的行内样式红；带标记+白名单属性绿", () => {
    expect(ruleIds(`<div style={{ color: "red" }} />`).has("inline_style")).toBe(true);
    expect(ruleIds(`<div data-uifinish-computed-layout style={{ width: 120, transform: "translateY(2px)" }} />`).has("inline_style")).toBe(false);
  });

  it("2 raw_brand_status_hex：裸 hex 红；token 类名绿", () => {
    expect(ruleIds(`<span style={{ color: "#059669" }}>启用</span>`).has("raw_brand_status_hex")).toBe(true);
    expect(ruleIds(`<span className="tk-ok">启用</span>`).has("raw_brand_status_hex")).toBe(false);
  });

  it("3 unstyled_native_control：裸 button 红；大写设计系统组件绿", () => {
    expect(ruleIds(`<button type="button">保存</button>`).has("unstyled_native_control")).toBe(true);
    expect(ruleIds(`<Button type="button">保存</Button>`).has("unstyled_native_control")).toBe(false);
  });

  it("4 todo_debug_test_copy：调试字样红；普通文案绿", () => {
    expect(ruleIds(`<p>调试用</p>`).has("todo_debug_test_copy")).toBe(true);
    expect(ruleIds(`<p>今日安排</p>`).has("todo_debug_test_copy")).toBe(false);
  });

  it("5 raw_json_or_http_error：JSON.stringify 渲染红；结构化文案绿", () => {
    expect(ruleIds(`<p>{JSON.stringify(err)}</p>`).has("raw_json_or_http_error")).toBe(true);
    expect(ruleIds(`<p>加载失败，请重试</p>`).has("raw_json_or_http_error")).toBe(false);
  });

  it("6 hand_filled_tenant_or_internal_id：租户 ID 展示红；普通标签绿", () => {
    expect(ruleIds(`<p>租户 ID: t_123</p>`).has("hand_filled_tenant_or_internal_id")).toBe(true);
    expect(ruleIds(`<p>门店编号: s_123</p>`).has("hand_filled_tenant_or_internal_id")).toBe(false);
  });

  it("7 unmarked_mock_stub_banner：未标注 mock 红；带 test-banner 标记绿", () => {
    expect(ruleIds(`<p>mock 数据示例</p>`).has("unmarked_mock_stub_banner")).toBe(true);
    expect(ruleIds(`<p data-uifinish-test-banner>mock 数据示例</p>`).has("unmarked_mock_stub_banner")).toBe(false);
  });

  it("8 window_alert_confirm：window.alert 红；普通按钮绿", () => {
    expect(ruleIds(`<button onClick={() => window.alert("x")}>go</button>`).has("window_alert_confirm")).toBe(true);
    expect(ruleIds(`<button onClick={save}>保存</button>`).has("window_alert_confirm")).toBe(false);
  });

  it("9 off_design_system_table_form_button：裸 table 红；大写设计系统组件绿", () => {
    // 对齐上游 Go 版：findTags 扫描原始源码（fail-close），注释内的标签也会命中，
    // 故正例改用大写组件（计划原「注释里的 table 绿」正例与上游语义矛盾，已修正）。
    expect(ruleIds(`<table><tr><td>1</td></tr></table>`).has("off_design_system_table_form_button")).toBe(true);
    expect(ruleIds(`<Table rows={rows} /><p>正文</p>`).has("off_design_system_table_form_button")).toBe(false);
  });

  it("10 missing_loading_empty_error：主页面缺三态红；渲染三态绿；注释里的 data-state 不算", () => {
    expect(ruleIds(`<main data-uifinish-surface="main"><p>只有正文</p></main>`, "fragment").has("missing_loading_empty_error")).toBe(true);
    expect(
      ruleIds(
        `<main><p data-state="loading">读取中</p><p data-state="empty">暂无</p><p data-state="error">出错了</p></main>`,
      ).has("missing_loading_empty_error"),
    ).toBe(false);
    expect(
      ruleIds(`<main data-uifinish-surface="main">{/* data-state="loading" data-state="empty" data-state="error" */}<p>正文</p></main>`).has(
        "missing_loading_empty_error",
      ),
    ).toBe(true);
  });

  it("11 expired_activity_as_today_live：expired 元素内写今天进行中红；兄弟节点不连坐", () => {
    expect(ruleIds(`<p data-state="expired">今天进行中：春季会员日</p>`).has("expired_activity_as_today_live")).toBe(true);
    expect(ruleIds(`<p data-state="expired">窗口已过</p>`).has("expired_activity_as_today_live")).toBe(false);
    expect(
      ruleIds(`<p data-state="expired">窗口已过</p>\n<p data-state="now">今天进行中：到店送一杯</p>`).has(
        "expired_activity_as_today_live",
      ),
    ).toBe(false);
  });

  it("12 unknown_reward_success_green：未知奖励 + tk-ok 红；未知奖励 + tk-unknown 绿", () => {
    expect(ruleIds(`<span data-reward-status="unknown" className="tk-ok">未知奖励</span>`).has("unknown_reward_success_green")).toBe(true);
    expect(ruleIds(`<span data-reward-status="unknown" className="tk-unknown">未知奖励</span>`).has("unknown_reward_success_green")).toBe(false);
  });
});

describe("计算布局白名单边界", () => {
  it("12 项白名单镜像上游：大小写与驼峰等价放行", () => {
    expect(computedLayoutAllows("WIDTH")).toBe(true);
    expect(computedLayoutAllows("minWidth")).toBe(true);
    expect(computedLayoutAllows("inset")).toBe(true);
  });

  it("color 与 font-size 一律拒绝（镜像上游，无本地扩展）；展开/计算属性名拒绝", () => {
    expect(ruleIds(`<div data-uifinish-computed-layout style={{ width: 10, color: "red" }} />`).has("inline_style")).toBe(true);
    expect(ruleIds(`<div data-uifinish-computed-layout style={{ width: 10, fontSize: 13 }} />`).has("inline_style")).toBe(true);
    expect(ruleIds(`<div data-uifinish-computed-layout style={{ ...layout }} />`).has("inline_style")).toBe(true);
    expect(ruleIds(`<div data-uifinish-computed-layout style={{ width: w, ["color"]: "red" }} />`).has("inline_style")).toBe(true);
    expect(computedLayoutAllows("color")).toBe(false);
    expect(computedLayoutAllows("font-size")).toBe(false);
  });
});

describe("scanFiles 文件级闸（代表页清单 0 命中）", () => {
  it("清单非空且包含 8 代表页与三个目录的全部 tsx", () => {
    const targets = collectScanTargets();
    expect(targets.length).toBeGreaterThan(20);
    for (const rel of REP_FILES) {
      expect(targets).toContain(path.join(WEB_ROOT, rel));
    }
  });

  it("代表页文件清单扫描 0 命中（依赖 Task 7/8 清理完成）", () => {
    const hits = scanFiles(collectScanTargets());
    expect(hits).toEqual([]);
  });

  it("文件级闸只适用 FILE_LEVEL_RULES 8 条", () => {
    expect(FILE_LEVEL_RULES).toHaveLength(8);
    expect(FILE_LEVEL_RULES).not.toContain("unstyled_native_control");
    expect(FILE_LEVEL_RULES).not.toContain("off_design_system_table_form_button");
    expect(FILE_LEVEL_RULES).not.toContain("hand_filled_tenant_or_internal_id");
    expect(FILE_LEVEL_RULES).not.toContain("raw_json_or_http_error");
  });

  it("fail-close 自检：真实违规临时文件必须能红", () => {
    const tmp = path.join(WEB_ROOT, "tests/.uifinish-fixture.tsx");
    writeFileSync(tmp, `export const Bad = () => <div style={{ color: "#fff" }} />;\n`);
    try {
      const hits = scanFiles([tmp]);
      expect(hits.some((h) => h.ruleId === "inline_style")).toBe(true);
      expect(hits.some((h) => h.ruleId === "raw_brand_status_hex")).toBe(true);
    } finally {
      rmSync(tmp);
    }
  });
});
