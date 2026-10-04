# HUI-2625 Product Finish Gate — 消费者切片证据（touch-engine @ hui-2628-r2）

- 生成：2026-10-04；commit：8535af2（hui-2628-r2 评审 A1-A7 修复 + rebase origin/main 831d9f8 后的合并树全量重跑；证据文件由本切片提交刷新）
- 声明：本切片只提供证据，不宣称 Gate 通过。blind family review = not_run（由 Gate 所有者执行）。

## 验收命令（全部真实执行，留痕见同目录）

| 项 | 结果 | 证据 |
|---|---|---|
| `cd web && npx vitest run` | 300 passed (300)（本分支 291 + hui-2748-r2 合入 9） | vitest-final.txt |
| `cd server && GOWORK=off go test ./...` | 30 包全 ok，0 FAIL | gotest-final.txt |
| `bash web/e2e/run.sh` | 6 spec 7 用例全 pass，证据门 exit 0（17/17 截图、axe blocking=0） | e2e-run.log |
| `grep -rc 'style={{' web/src --include='*.tsx' \| grep -v ':0$' \| wc -l` | 0 | 本行命令在生成时执行，计数 0 |

## viewport × 页面 × 状态矩阵

数据源：`web/e2e/evidence/playwright-report.json`、`web/e2e/evidence/axe-summary.json`（blocking=[]）与 E2E 06 spec（8 代表页 × 390/430 截图 + axe；1920 商家首页截图）。

| 页面 | 390 | 430 | 1920 | 状态覆盖 |
|---|---|---|---|---|
| /（MerchantHome） | 截图+axe pass | 截图+axe pass | 截图 | ready（今日台真实数据）；E2E 01 |
| /work/stores | 截图+axe pass | 截图+axe pass | — | ready；E2E 05 对照组 |
| /work/campaigns/[id] | 截图+axe pass | 截图+axe pass | — | E2E 03 |
| /work/materials | 截图+axe pass | 截图+axe pass | — | — |
| /work/rewards | 截图+axe pass | 截图+axe pass | — | 未知奖励非成功绿：E2E 04 |
| /work/analytics | 截图+axe pass | 截图+axe pass | — | — |
| /c/[code] | 截图+axe pass | 截图+axe pass | — | available/expired/paused/ended/not_found：E2E 02 |
| /c/[code]/contact | 截图+axe pass | 截图+axe pass | — | pending/revoked：E2E 03 |

公共页状态实测（E2E 02，评审 A2 后）：五态全部按 `main[data-testid="public-activity"][data-state=…]` 精确断言——available / expired / paused / ended / not_found（值 = Go ResolveLink machine state，server/internal/store/store.go:533-540）；expired 与 ended 文案相同，靠 data-state 区分。另增 loading 态用例（page.route 拦 `/api/public/links/*` 使 fetch 悬置，断言 `data-state="loading"`）。

留资流实测（E2E 03）：提交后 `lead-outcome` data-state/tone=pending（待同步文案），撤销后 =revoked 且渲染 LEAD_REVOKED_COPY（已撤销）。

未知奖励（E2E 04）：available 公共页 main 无 `.tk-ok` 元素，文字色 ≠ rgb(6, 95, 70)。

## uifinish 源码扫描

- 代表页文件清单扫描 0 命中（vitest：`web/tests/uifinish.test.ts`「代表页文件清单扫描 0 命中（依赖 Task 7/8 清理完成）」，随 291 全绿通过）
- 12 规则逐条反例可红/正例不误伤（同文件 12 个 it）；文件级闸 FILE_LEVEL_RULES 8 条 + snippet 级 4 条（spec §4.3 排除表）；fail-close 自检通过

## a11y

- 阻断线：critical/serious = 0（E2E 06 全部 16 格 axe 通过，axe-summary.json blocking=[]）
- moderate（不阻断，闸只看 critical/serious）：region(1) × work 五页（`.tk-nav` 列表未包进 landmark；生成于 390 宽实测探针，见下）。home 与公共两页 0 条。
- 修复记录：`113aa58`——`.tk-button` 白字/薄荷绿底对比 1.44:1（axe serious）改墨色字（约 10.7:1）；`/work/campaigns`、`/work/materials` 两处裸 `<select>` 补 aria-label（axe critical select-name）。修复后闸清零。

## known-issues（已知半成品点）

- /admin 手填租户 ID：登录后切换租户时把租户 ID 写入 localStorage（`web/src/app/admin/page.tsx` 的 `TENANT_KEY = "touch_admin_tenant"`，:150/:297/:618）。这是 `hand_filled_tenant_or_internal_id` 的真实风险点；该规则经 root 2026-10-04 追认不进文件级闸，在此留档，待后续票收口。
- build 基线红由本分支修复（`web/src/app/api/eco-nav/campaign-image/route.ts` 的 ProcessEnv 弱类型，commit 48dc83a；allowlist.ts 两处同类 7ed77b1），复现证据见 hui-2748-r2 分支回报。
- work 五页 `.tk-nav` 的 region(moderate) 未收口（非阻断）；留待后续票把导航包进 landmark。
- ~~390 视口 work 五页横向溢出（截图实际 428px 宽）~~ 已修复（评审 A7，commit 9cf47de）：溢出源是 EcoTopNav 右侧账户钮 `.popover` 的 `min-width:auto` 拒缩；改 `.right .popover { min-width: 0 }` + 账户钮截断后，五张 work 390 截图实测回到 390px（本目录五张已刷新），宽屏无感。
- work 五页 390 截图顶部的「碎片带」为残留（非阻断）：探针实测（视口裁剪 / fullPage / 视口加高三种捕获产物一致 + 活布局量测）确认它是 EcoTopNav 在 390 下的真实渲染——`.context` 区「额度需确认」「预览上下文·未接公共身份」等文本在按钮/span 内换行成两行、被 56px 条高裁切。功能完好、无横向溢出（scrollWidth=390、`.bar` 56px@top:0）；helpers.screenshot 已带回顶后再截（确定性防御）。窄口导航重排属新设计决策，留待后续票。

## 截图清单

docs/evidence/hui-2628/：
- home-390.png / home-1920.png
- work-stores-390.png
- work-campaign-detail-390.png
- work-materials-390.png
- work-rewards-390.png
- work-analytics-390.png
- public-campaign-390.png
- public-contact-390.png
（另有 430 宽 8 张与全套运行日志在 `web/e2e/evidence/`，由 web/e2e/.gitignore 约束不入库；入库以本目录 9 张为准。）

## blind family review

- verdict: not_run（Gate 所有者执行；本票无权代填）
