# HUI-2628 finish-r3 证据包（codex/20261008-hui2628-finish-r3）

- 生成：2026-10-08；分支头：见 PR 描述（本文档随最后提交刷新）
- 性质：**只交证据，不宣称 Gate 通过。** HUI-2625 的 `Passed()` 恒为 false 属 Gate 所有者事实；本票按票面不标 Done。未部署。

## 做了什么（r3 增量，全部真实执行）

| # | 项 | 提交 | 结果 |
|---|---|---|---|
| 1 | 商家七个错误面补「重试」恢复动作（today-desk/stores/campaigns/materials/rewards/analytics/campaigns[id]，重调既有 load 或 attempt+1，幂等 GET） | f5f5011 | `data-action="retry"` 全数落地；product-pages.test.ts 增 2 断言 |
| 2 | EcoTopNav 390 碎片带窄修（`.context` overflow hidden + 子元素 nowrap/ellipsis/min-width:0；唯一 DOM 变更 = `data-testid="eco-nav-context"`） | 4d7b034 | 探针 07 断言 390 子元素单行≤28px、scrollWidth=390、bar 56px；r2 known-issue「碎片带」消除（见 work-stores-390.png 顶栏单行省略号） |
| 3 | 接棒修复：page-census 账本同步（rewards/analytics 各记 1 个 raw button，与 f5f5011 重试钮对应） | f6c01ed | vitest 294→302 全绿 |
| 4 | 截图矩阵补 1440 宽（8 代表页 × {390,430,1440} + home-1920 = 25 张全集，证据门同步 17→25） | 0e9a864 | gate 全量模式 25/25 |
| 5 | 去 Logo 产品族材料（08 spec：1440 商家四页 brand-masked 变体） | 本提交 | blind-family/ 4 张，verdict not_run |
| 6 | 本证据包归档 | 本提交 | docs/audits/hui-2628/finish-r3/ |

## 没做什么（如实）

- 根页未重做：`web/src/app/page.tsx` r2 已是 `<TodayDesk />`（MerchantGate 登录门 → 「今天」任务列表 → 下一步交接），票面验收「根页不再是工程说明」r2 已达标，本轮复核留证（见 home-390/1440/1920.png）。工程说明文案与 inline style 全仓均为 0。
- 顾客公共页逻辑未动（PR#31/r2 红线）：unknown 不显示成功绿态（E2E 04 复跑通过）、无 EcoTopNav（E2E 05）、无 SaaS 自我宣传（layout 只挂 `data-pn-surface`，文面复核 0 命中宣传文案）。
- 跨仓联动（Leads/Matrix）未做：措辞级审计结论「无需代码改动的对齐项 = 0」，审计表见 worktree DECISIONS.md B3（措辞同源：「今天」「查看本活动线索」「重试/恢复」）。
- 结构性窄口导航重排未做（390 顶栏标签省略号截断是既定裁决的暂态，留后续票）。
- 生产部署未做。

## 验收命令（本包生成时真实执行，留痕见同目录）

| 项 | 结果 | 证据 |
|---|---|---|
| `cd web && npx vitest run --pool=forks` | 302 passed (302)，0 failed | vitest-final.txt |
| `bash web/e2e/run.sh`（全量模式） | 8 spec 10 用例全 pass，skipped=0，证据门 exit 0 | e2e-run.log |
| 证据门全量：25 张验收截图 + axe | 25/25 在 evidence/，axe critical+serious = 0 | e2e-run.log + axe-summary.json |
| `grep -rn 'style={{' web/src --include='*.tsx'` | 0 行 | 生成时执行，计数 0 |
| `cd server && GOWORK=off go test ./... -p 2` | 30 包全 ok，0 FAIL（防御性实跑留痕；本分支 web-only 零 server/ 改动） | go test exit 0（DECISIONS.md B7.3） |

## viewport × 页面 × 状态矩阵（r3）

数据源：`web/e2e/evidence/playwright-report.json`、`axe-summary.json`（blocking=[]）、E2E 06（8 代表页 × 390/430/1440 + axe；1920 商家首页）。

| 页面 | 390 | 430 | 1440 | 状态覆盖（data-state） |
|---|---|---|---|---|
| /（TodayDesk 商家首屏） | 截图+axe pass | 截图+axe pass | 截图+axe pass | loading/error/ready；E2E 01（首屏「今天要做什么」+下一步交接） |
| /work/stores | 截图+axe pass | 截图+axe pass | 截图+axe pass | loading/empty/error+重试 |
| /work/campaigns | —（详情页为代表） | — | — | loading/empty/error+重试；expired/paused/ended 过滤态 |
| /work/campaigns/[id] | 截图+axe pass | 截图+axe pass | 截图+axe pass | loading/empty×2/error×3+重试；expired/paused/ended |
| /work/materials | 截图+axe pass | 截图+axe pass | 截图+axe pass | loading/empty/error+重试 |
| /work/rewards | 截图+axe pass | 截图+axe pass | 截图+axe pass | loading/empty/error+重试；未知奖励非成功绿：E2E 04 |
| /work/analytics | 截图+axe pass | 截图+axe pass | 截图+axe pass | loading/empty/error+重试 |
| /c/[code] | 截图+axe pass | 截图+axe pass | 截图+axe pass | available/expired/paused/ended/not_found+loading：E2E 02（data-testid="public-activity" 精确断言，值=Go ResolveLink machine state） |
| /c/[code]/contact | 截图+axe pass | 截图+axe pass | 截图+axe pass | pending/revoked：E2E 03 |

恢复动作：七商家错误面均有 `data-action="retry"` 重试钮（本轮 f5f5011+f6c01ed 账目）；公共页 not_found/expired 等终态按 r2 文案诚实呈现（不写成进行中，PR#31 先例保持）。

## 票面验收逐条对照

| 票面验收 | 判定 | 依据 |
|---|---|---|
| 根页不再是工程说明 | 达标（r2 交付，r3 复核） | page.tsx = TodayDesk；home-*.png；工程说明/inline style 计数 0 |
| 商家第一次进入即可知道今天要做什么 | 达标（r2 交付，r3 复核） | TodayDesk「今天」任务列表 + 「下一步」交接；E2E 01 |
| 顾客页不出现 SaaS 自我宣传 | 达标（r2 交付，r3 复核） | E2E 05 无 EcoTopNav；公共 layout/文面复核 0 命中 |
| 与 Leads/Matrix Handoff 一致任务感 | 措辞级同源（r2 交付，r3 审计 0 改动项） | DECISIONS.md B3 审计表 |
| 通过 HUI-2625 | **未过（如实）** | `Passed()` 恒 false 属 Gate 所有者事实；本包只交证据，不标 Done |

## 截图清单（本目录，17 张矩阵精选）

- 390：home / work-stores / work-campaign-detail / work-materials / work-rewards / work-analytics / public-campaign / public-contact
- 1440：同上 8 页
- 1920：home
- （430 宽 8 张与全套运行产物在 `web/e2e/evidence/`，由 web/e2e/.gitignore 约束不入库）
- blind-family/：4 张去 Logo 变体 + README（verdict: not_run）

## known-issues（r3 末态）

- /admin 手填租户 ID（r2 已留档，待后续票）：localStorage 写租户 ID，`hand_filled_tenant_or_internal_id` 风险点，不进文件级闸。
- work 五页 `.tk-nav` region(moderate) 未收口（非阻断，axe 闸只看 critical/serious）。
- 390 顶栏标签省略号截断：窄修后的既定暂态（单行、不裁切、无溢出），结构性重排留后续票。
- ~~390 碎片带~~ 已消除（4d7b034，探针 07 锁事实）。
- page-census rewards/analytics 各记 1 个 raw button（重试钮如实记账；全仓 7 个重试点抽共享组件的取舍见 DECISIONS.md B6.3）。

## NOT_RUN 账目

- blind family review verdict：not_run（Gate 所有者执行）
- HUI-2625 Gate Passed()：false（恒 false，Gate 所有者维护）
- 生产部署：未做
- 跨仓（Leads/Matrix）联动开发：未做（措辞级对齐 0 改动项）
- server/ go test：本分支未触碰 server/，防御性实跑 30 包 ok 留痕；origin/main 的 HUI-2981 属并行工作
- PR 合并 / Linear 票状态 / Linear 评论：归 root
