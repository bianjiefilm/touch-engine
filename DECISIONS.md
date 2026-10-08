# HUI-2628 finish-r3 决策记录（codex/20261008-hui2628-finish-r3，基线 184d0fc）

业主授权全程自答（任务书裁决预设）。本文件是 r3 全部中途决策的留痕，只报真正阻塞项。

## B0 盘点结论：r2 已交付什么（不推翻重做）

- 根页 `/` 已是 `TodayDesk`（MerchantGate 登录门 → 今日任务列表 + 下一步交接），票面头号验收「根页不再是工程说明」已达标。r3 只复核 + 补证据。
- inline style 全仓 0（`grep -rn 'style={{' web/src --include='*.tsx'` = 0 行）。
- 八代表页全部存在，均有 loading/error（多数含 empty）态；公共页五态 + loading 有 E2E 02 精确断言，未知奖励非绿有 E2E 04，公共页无 EcoTopNav 有 E2E 05。
- vitest 300 绿、e2e 6 spec 7 用例、axe blocking=0、17 张截图证据门（web/e2e/scripts/evidence-gate.mjs）。

## B1 r3 增量范围（gap → 任务）

| # | 差距（现状证据） | 决策 |
|---|---|---|
| G1 | 六处 error 态只有文字（surfaceLabel("error")="没有读到，请重试" 但页面无任何重试动作）：/、stores、campaigns、materials、rewards、analytics、campaigns/[id] | 补「重试」按钮（重新调用既有 load 逻辑），错误态恢复动作落地。TDD：product-pages.test.ts 增错误态含重试控件断言 |
| G2 | 390 碎片带：EcoTopNav `.context` 内 span/button 文本换行成两行被 56px bar 裁切（r2 finish-gate-slice known-issue，探针实测真实渲染） | **判定：窄修**。纯 CSS（`.context` overflow hidden + 子元素 nowrap/ellipsis），不动结构、不动交互、不改 DOM。e2e 探针断言 390 下 `.context` 子元素单行。若实施中证明 CSS 修不动 → 降级记录上报，不硬改 |
| G3 | 截图矩阵缺 1440（r2 仅 390/430×8 + home-1920；2625 门要跨栈矩阵，2626 口径 Desktop 1440 有合理密度） | spec 06 widths 增 1440，evidence-gate EXPECTED_SHOTS 17→25 |
| G4 | 2625 证据包 r2 散在 docs/evidence/hui-2628 | r3 证据新家 docs/audits/hui-2628/finish-r3/：矩阵精选 + axe 汇总 + 状态覆盖表 + 去 Logo 产品族材料 + finish-r3.md（如实写「证据包已交，门未过，不标 Done」） |
| G5 | Leads/Matrix 措辞一致性 | 见 B3 措辞审计表，措辞级对齐，不做跨仓开发 |

## B2 关键裁决（自答）

1. **根页**：不重做。r2 已 TodayDesk，复核后如实在证据包标注「已达标（r2 交付，本轮复核）」。
2. **390 碎片带**：窄修（CSS-only）。理由：成因单一（`.context` 子元素无 nowrap），修复面 1 个 CSS 文件 + e2e 探针；结构性窄口重排（如 context 收进溢出菜单）不做，留后续票。
3. **恢复动作最小语义**：error 态加一个「重试」按钮调用页面既有 load 函数；不引入 toast/全局错误中心（那是新架构，超票面）。
4. **去 Logo 产品族材料**：用 playwright 对 1440 商家 4 页（home/stores/campaign-detail/rewards）生成 brand-masked 变体（hide `[data-testid="eco-brand"]`、账户钮文本、`.badge`），存 finish-r3/blind-family/；标注 `verdict: not_run（评审由 Gate 所有者执行）`。诚实边界：只供盲评，不自称同族结论。
5. **unknown 不显示成功绿**：公共页逻辑不动（PR#31/r2 已守），r3 复跑 spec 02/04 回归即可。
6. **措辞对齐边界**：留资=顾客在公共页提交联系资料的域内词；线索=Leads 应用域内词。Touch 的 Leads 入口动词=「查看本活动线索」（TaskHandoffActions LABEL），与 2626「线索」「Today/Next」同源。只审计不改跨仓。

## B3 措辞一致性审计（G5，与 2626 对照）

| 语义 | Touch（本仓）现措辞 | 2626（Leads）措辞 | 判定 |
|---|---|---|---|
| 首屏组织词 | 「今天」（TodayDesk h1） | 「Today/Next」「今天要完成的客户工作」 | 同源，不改 |
| 进 Leads 入口 | 「查看本活动线索」 | 「线索」「Lead/Contact」 | 同源，不改 |
| 内容制作入口 | 「为本活动制作图片/视频」 | 2626 无对应（Matrix 域） | 同义保留 |
| 顾客提交的资料 | 「留资」「待同步留资」 | 2626 不涉顾客公共页 | 域内词，保留 |
| 恢复动作 | r3 新增「重试」 | 2626「Error/Recovery」 | 同源 |

结论：无需代码改动的措辞级对齐项 = 0；审计表入证据包。

## B4 环境与边界

- 走查/证据用 web/e2e 自含 harness（env.mjs 固定端口 18460/18461/18462，r2 既有编排，未改动）；任务书 29xxx 规则适用于临时手工起服，本轮未起临时服务，无需 29xxx。无第三方付费调用。
- 禁区未触碰：HUI-2622 leads 进程、D2 系列、Motion Consumer 链、Redis 票。
- 不合并 PR、不改 Linear 票状态、不发 Linear 评论（归 root）。

## B5 执行中追加决策

1. **G2 窄修成立（2026-10-08）**：修复前探针实测 390 下 `.context` 子元素高 **66px**（多行换行被 56px bar 裁切，碎片带复现）；`.context` overflow:hidden + 子元素 nowrap/ellipsis/min-width:0 后探针 GREEN（单行≤28px、scrollWidth 390、bar 56px、1440 无感）。未做结构性窄口重排（r2 既定留后续票）。
2. **G1 范围含活动详情两个子列表错误态**（短码/标签）：与主态同型各加重试（重调既有 load，幂等 GET）。
3. **E2E 探针定位用 testid 而非 CSS Modules 哈希类**：EcoTopNav context div 加 `data-testid="eco-nav-context"`，唯一 DOM 变更，非结构性。

## B6 接棒续记（2026-10-08，第二任）

1. **票面全文已拉取核对**：验收四条（根页非工程说明/商家首知今天/顾客页无 SaaS 自宣传/2625 门）与 r2 评论（Passed() 恒 false，不标 Done）一致，本轮范围不变。
2. **接棒基线非全绿——账本漂移修复**：接手时 `npx vitest run` 294/302，8 挂。根因：f5f5011 给 rewards/analytics 页面文件各加了 1 个原生 `<button>`（重试钮），`page-census.ts` markerBook 与 `page-census.test.ts` want 表仍记 `[]`（census 只扫 page 文件，today-desk 等组件文件不在扫描面）。修复：两处账本同步记 `["raw button"]`（对齐 r2「账本同步，命中只记账」先例）。修复后 302/302 全绿（=r2 基线 300+f5f5011 新增 2 条重试断言）。
3. **备选否决**：抽共享 RetryButton 组件可避免新增 raw button 记账，但全仓 7 个重试点只抽 2 个反而制造不一致，且 census 设计即被动账本非质量门——否决，留后续票。
4. **任务书「根页改造」条件不触发**：亲验 `web/src/app/page.tsx` = `<TodayDesk />`（MerchantGate 登录门→「今天」任务列表→下一步交接），inline style 全仓 0。票面头号验收 r2 已达标，本轮复核留证进证据包，不重做。
5. **origin/main 分叉 10 提交全是 HUI-2981（server/ Go 缓存票）**：与本分支 web/ 改动零重叠，不 rebase（禁区：Redis 票不碰），PR 直开，合并冲突风险≈0。

## B7 收口记录（2026-10-08，第二任续）

1. **e2e 全量真跑通过**：8 spec 10 用例全 pass（01 首屏/02 公共五态/03 留资/04 未知奖励/05 公共无导航/06 a11y+25 截图矩阵/07 390 探针/08 blind 材料），skipped=0，证据门全量模式 exit 0（25/25 截图 + axe critical+serious=0）。目检 work-stores-390（顶栏单行省略号，碎片带消除）与 work-stores-1440（全标签无截断）。
2. **证据包归档** docs/audits/hui-2628/finish-r3/：finish-r3.md（票面验收逐条对照，如实写「2625 门未过，不标 Done」）+ 17 张矩阵精选（8×{390,1440}+home-1920）+ blind-family/（4 张去 Logo 材料，verdict not_run）+ vitest-final.txt（302 绿）+ e2e-run.log + axe-summary.json。
3. **go test SKIP 留痕**：本轮 web-only 零 server/ 改动，沿用 r2 30 包 ok 基线；origin/main 的 HUI-2981 属并行工作不在本 PR 账内。
