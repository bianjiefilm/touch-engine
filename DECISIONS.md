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
3. **go test 防御性实跑留痕**：本轮 web-only 零 server/ 改动，仍按 plan Task 5 跑一次留痕——30 包全 ok、0 FAIL、exit 0；origin/main 的 HUI-2981 属并行工作不在本 PR 账内。
4. **code-review 两轴自审（接棒裁决：不派并行子代理**——任务书明令控制请求频率、禁止孙代理，前任死于账户级限流 1302；两轴由本代理逐项直审，范围=184d0fc..HEAD 全 diff）。Standards 轴：禁区文件 0 触碰、inline style 0、无新增裸 hex、CSS-only 窄修带注释、重试钮两种模式（load()/attempt+1）全分支一致、`<button>` 位于 `<p>` 内合法（phrasing content）。Spec 轴：票面验收五条逐条对照过（见 finish-r3.md 表），公共页红线（unknown 非绿/无 EcoTopNav/无 SaaS 宣传）经 E2E 02/04/05 + layout/文面复核全守。
5. **验证四层全绿**：vitest 302/302；e2e 8 spec 10 用例+证据门 exit 0（25/25 截图、axe blocking=0）；go test 30 包 ok；目检 390（碎片带消除）与 1440（无截断）截图。

## B8 fix2 轮决策（2026-10-08，第三任，gate-r2 修复轮）

依据：public-ai `docs/audits/hui-2625/gate-r2/fix-lists.md`「→ HUI-2628」6 条（origin/main fdbdb8c 基线起步，分支 codex/20261008-hui2628-fix2）。证据落 docs/audits/hui-2628/fix2/。

1. **B8.1 错误文案词表收口**：新建 `web/src/lib/failure-copy.ts` `failureText(status, data)` 把机器响应映射为产品语句+下一步（store_required→「先到门店添加或选好门店再操作一次」；401/403/404/5xx/兜底各一条）。MerchantSession 错误改走 failureText；admin `whoStatusText`（17 处）同源替换。**后端原文不透传**是硬线：vitest 用例锁「无三位状态码/机器码/后端 message 原文」。
2. **B8.2 detector raw 锚点清零方式**：r2 点名的 6 处 raw_json_or_http_error = admin 4×`JSON.stringify` 字面 + campaigns 2×`statusText` 词界。处置：admin 指纹复制改 `[...].join("|")`（语义等价）；fetch body 收口 `lib/http-json.ts toJsonBody()`；交接资料 textarea 改 8 字段列表化呈现（`data-testid="professional-handoff"`，非 dump）；campaigns `statusText`→`statusLabel`（改名即消失，无行为变化）。admin 内 L867 POI 录入 textarea 是真实表单输入，保留（录入型≠机器 dump）。
3. **B8.3 Home 主行动收敛**：`TaskHandoffActions` 以 `make_campaign_image`（唯一真实执行动作）为实心主行动（`tk-button`+`data-primary-action="true"`），其余次级 `tk-quiet`。/admin 壳同组件自动收敛，/work/campaigns/[id] 无重复实心。
4. **B8.4 三态静态声明口径**：missing_loading_empty_error 3 处按 2625 现行 detector 口径补页面文件源字面 `data-state="loading|empty|error"`（三页各 3 行 `<p hidden>`，hidden 不参与渲染，运行时真三态在组件内且 E2E 02/10 已证）。**声明≠渲染**的口径分歧如实注记，改口径议题归 HUI-2619，本轮不改 detector。
5. **B8.5 视口矩阵腿定义 48 张**：9 面（8 代表 + List 独立腿 work-campaigns）×{390,430,1024,1440}（36）+ 9×1920 补齐（9）+ /admin 3 张（390/1440 登录视图如实呈现手填租户表单 + 1440 登录后壳）。axe 阻断面保持 r3 的 8 代表页（work-campaigns 记录性跑，实测 0 critical+serious；admin 不入阻断面，风险已留档）。
6. **B8.6 a11y 四项证据形态**（对标 leads keyboard-focus.json/touch-targets.json）：keyboard-focus.json=真实 Tab 走查 12 步直达 `[data-primary-action]` + Enter 后焦点保持；focus-visible.json=`:focus-visible` 匹配 + outline 2px 实测（`kb-focus-ring.png`/`kb-after-enter.png`）；reduced-motion.json=仿 reduce + 全仓 @keyframes/transition 动效账=0；touch-targets.json=390 三面逐控件命中盒测量，button/input/select/textarea ≥44×44 硬断言，checkbox 测包裹 label 命中区（leads 同款语义）。EcoTopNav 顶栏 36px 记账豁免（既定暂态裁决不变，r2 B5.1 结构性窄修留后续票，不在本票翻案）。
7. **B8.7 ≤430 真实 CSS 补齐**：`.tk-nav a/.tk-row > a` padding 0.7/0.4rem、按钮类 `min-height:44px`、`.tk-check min-height:44px`（带 fix2 注释；桌面无感，非仅测试造假）。
8. **B8.8 offline 两层口径**：真实断网（context.setOffline，SPA 已加载→应用内「网络异常，请稍后再试」文案）为产品证据；route-abort fixture 为页面级 degraded-state + 重试闭环驱动（网络层等价）；整页加载断网=浏览器边界，如实留档（`state-offline-browser-boundary.png`）不计产品证据。MerchantGate 补 `retryConnection`（attempt+1 重跑 whoami，与既有 disabled 重试同模式）。
9. **B8.9 partial fixture 语义**：子请求 HTTP 500/404（`route.fulfill`）而非 `route.abort`——abort 令 fetch reject 把 `Promise.all` 整体打翻成主 error，不是产品 partial 语义；fulfill 500 造出「主体可用+短码/标签/留资统计单独失败」真 partial，留资统计 404 呈「未开通」而非错误红。
10. **B8.10 disabled 场景全产品 UI 驱动**：建关联门店活动+生成短码→停用门店→工作台 `tk-unknown` 已停用徽章+公共页「门店暂不可用」→复位启用，不级联改已有活动（E2E 14 用例闭环）。
11. **B8.11 执行纪律教训**：(a) `env.mjs up` 会 rmSync 整个 evidence/——定向补截后必须重跑全量再归档（本轮实际发生，evidence 曾被清空后重建）；(b) env 运行中重建 `.next` 会混用构建（focus-visible 探针 `--tk-brand` 空值实锤），确立 down→build→fresh up 流程；(c) 两次 commit 带红（324174e/8fd2e69 提交时测试未绿）当轮即补提交修正（3a9f8c8/057e428 前后文见 git log），后续坚持 vitest 与 commit 分开跑。
12. **B8.12 验证基线只增不破**：vitest 302→311（+9：failure-copy 3、三态声明/主行动/产品文案/retry 断言 6）；e2e 10→15 用例（+5：06 拆 admin 独立 test、09 a11y 四项 1、10 三态 3）、验收截图 25→48、go test 30→31 包（B2981 并行票并入 main 使包数 +1，全 ok）。
