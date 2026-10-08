# HUI-2628 fix2 证据包（codex/20261008-hui2628-fix2，gate-r2 修复轮）

- 生成：2026-10-08；基线 origin/main fdbdb8c（含 finish-r3）；分支头：见 PR 描述（本文档随最后提交刷新）
- 性质：**针对 2625 gate-r2 裁决 touch-web=fail 的逐条修复轮，只交修复证据与对照，不宣称 Gate 通过。** 复裁决归 HUI-2625 所有者；本票不标 Done。未部署。
- 修复清单来源：public-ai `docs/audits/hui-2625/gate-r2/fix-lists.md`「→ HUI-2628」节（6 条）；判定口径 `checklists/touch-web.json`；detector 未改动（`internal/uifinish/scan.go` 本轮零触碰，改口径议题归 HUI-2619）。
- r3 已交付证据（17 矩阵精选 + blind 4 张）不推翻、不重做；本轮只对改动面补新证据。

## 六条逐条对照（fix-lists.md → HUI-2628）

| # | fix-list 条目 | 处置 | 提交 | 证据 |
|---|---|---|---|---|
| 1 | 错误文案泄漏裸 HTTP：campaigns/[id] 红字直出 `没有完成（400 store_required）…`；detector raw_json_or_http_error 命中 6 处，需清零 | 新建 `web/src/lib/failure-copy.ts` `failureText(status, data)` 词表映射：机器响应→产品语句+下一步（store_required→「这次没有完成：活动还没有关联门店。先到「门店」添加或选好门店，再操作一次。」+ 重试钮）；401/403/404/5xx/兜底各一条。MerchantSession 错误相与 admin `whoStatusText`（17 处）同源替换；campaigns 列表 `statusText`→`statusLabel` 改名即消失 | fdeac18、5be8944、a1dd396 | before/after/error-copy-{before,after}.png（同 fixture 同口径对照）；tests/failure-copy.test.ts（锁无三位状态码/机器码/后端 message 原文）；全量 e2e 证据门 raw 锚点归零（gate exit 0） |
| 1（admin 4 处） | 同上条 detector 口径：admin 页 `JSON.stringify` 字面 ×4 | 指纹复制改 `[…12 个字段值].join("\|")`（语义等价）；fetch body 序列化收口 `lib/http-json.ts toJsonBody()`；交接资料 textarea 改 8 字段列表化呈现（`data-testid="professional-handoff"`）；POI 录入 textarea 属真实表单输入，保留 | 324174e（断言收窄修正 3a9f8c8） | tests/product-pages.test.ts（admin 源无 `JSON.stringify`、readOnly dump 模式断言）；matrix/admin-1440.png |
| 2 | Home 主行动：三连描边并列，需至多一个可执行主行动 | `TaskHandoffActions` 以 `make_campaign_image`（唯一真实执行动作）为实心主行动（`tk-button` + `data-primary-action="true"`），其余次级 `tk-quiet`；/admin 壳同组件自动收敛 | 8fd2e69（断言对齐修正 057e428） | before/home-before-1440.png vs after/home-after-1440.png（唯一实心钮）；a11y/keyboard-focus.json（Tab 走查 12 步直达主行动）；vitest 主行动断言 |
| 3 | 视口矩阵缺口：1024 整腿、1920 补齐、/admin 截图如实呈现、/work/campaigns 独立腿 | 矩阵 25→48 张：9 面（8 代表 + work-campaigns 独立腿）×{390,430,1024,1440} + 9×1920 + /admin 3 张（390/1440 登录视图如实呈现手填租户表单 + 1440 登录后壳）；证据门 EXPECTED_SHOTS 同步 48 | 4caf426 | matrix/（21 张：9×1024 + 9×1920 + admin 3）；logs/e2e-run.log（48/48 + axe critical+serious=0） |
| 4 | a11y 四项证据缺：keyboard / focus-visible / reduced-motion / touch-target 逐控件（对标 leads keyboard-focus.json / touch-targets.json 形态） | 新 spec 09：keyboard-focus.json（真实 Tab 走查至 `[data-primary-action]` + Enter 后焦点保持）、focus-visible.json（`:focus-visible` 匹配 + outline 2px 实测）、reduced-motion.json（仿 reduce + 全仓动效账=0）、touch-targets.json（390 三面逐控件命中盒：button/input/select/textarea ≥44×44 硬断言；checkbox 测包裹 label 命中区）；≤430 CSS 真实补齐（nav/row 链接 padding、按钮类与 `.tk-check` min-height:44px） | 8bd1e02 | a11y/ 四 JSON + kb-focus-ring.png + kb-after-enter.png + touch-targets-390.png |
| 5 | state 三态证据缺：partial / disabled / offline（offline=应用内离线 UI+重试入口，不是浏览器错误页；结构性不存在要如实标注） | 新 spec 10 三场景：partial=子请求 HTTP 500/404（主体可用+短码/标签/留资统计单独失败，留资统计 404 呈「未开通」）；disabled=产品 UI 建关联门店活动→停用→工作台已停用徽章+公共页「门店暂不可用」→复位（不级联）；offline=真实 setOffline 提交留资报「网络异常，请稍后再试」+ 路由中止驱动页面级重试 + MerchantGate「没有连上服务」重试闭环（attempt+1）+ 浏览器边界如实留档（不计产品证据） | cbb4a1c、a1dd396、c61d2bd（段间竞态修复） | states/ 3 JSON + 7 张截图；states/state-partial-campaign-detail.png（主体 h1 与错误并存） |
| 6 | missing_loading_empty_error 3 处：按现行 detector 口径补三态静态声明 | /、/admin、/c/[code]/contact 三页源各补 3 行 `<p hidden data-state="loading\|empty\|error">` 字面声明（hidden 不参与渲染；运行时真三态在组件内且 E2E 02/10 已证）。**声明≠渲染的口径分歧如实注记，改口径议题归 HUI-2619，本轮不改 detector** | 7ee16b7 | vitest 三页声明断言；detector missing_loading_empty_error 全量证据门 exit 0（无 missing） |

## 验收命令（本包生成时真实执行，留痕见 logs/）

| 项 | 结果 | 证据 |
|---|---|---|
| `cd web && npx vitest run --pool=forks` | 311 passed（302 基线 +9，只增不破），0 failed | logs/vitest-final.txt |
| `bash web/e2e/run.sh`（全量模式） | 10 spec 15 用例全 pass，skipped=0，证据门 exit 0 | logs/e2e-run.log |
| 证据门全量：48 张验收截图 + axe | 48/48 在 evidence/，axe critical+serious = 0 | logs/e2e-run.log + a11y/axe-summary.json |
| `cd server && GOWORK=off go test ./... -p 2` | 31 包全 ok，0 FAIL（防御性实跑；本分支零 server/ 改动；30→31 为 main 并入 HUI-2981 并行票所致） | logs/go-test-final.txt |
| `grep -rn 'style={{' web/src --include='*.tsx'` | 0 行 | 生成时执行，计数 0 |

## 证据清单（本目录）

- `before/`：home-before-1440.png（三连描边原状）、error-copy-before.png（`没有完成（400 store_required）：…` 原文）
- `after/`：home-after-1440.png（唯一实心主行动）、error-copy-after.png（产品语句+重试；同 fixture 对照）
- `matrix/`：9 面 × {1024,1920} 共 18 张 + admin-{390,1440,shell-1440}.png（390/430/1440 腿全量在 evidence/ 由 run.sh 产出，r3 已归档 390/1440 精选不重复）
- `a11y/`：keyboard-focus.json、focus-visible.json、reduced-motion.json、touch-targets.json、axe-summary.json、kb-focus-ring.png、kb-after-enter.png、touch-targets-390.png
- `states/`：state-scenarios.json（partial/disabled）、state-scenarios-offline.json、state-partial-campaign-detail.png、state-disabled-{stores,public}.png、state-offline-{public-submit,public-network-error,merchant-inapp,browser-boundary}.png
- `logs/`：vitest-final.txt、go-test-final.txt、e2e-run.log

## known-issues（fix2 末态，承 r3）

- /admin 手填租户 ID（r2 已留档）：登录视图按 fix-list 第 3 条要求如实截图呈现（matrix/admin-1440.png），修复归后续票。
- 390 顶栏标签省略号截断：既定暂态裁决不变（单行、不裁切、无溢出），结构性重排留后续票。
- EcoTopNav 顶栏链接触控命中 36px：记入 touch-targets.json below44 豁免账（生态共享组件 + 暂态裁决不变），未翻案。
- checkbox 视觉盒 13px：按命中区语义测包裹 label（≥44，`.tk-check` min-height 已补）；视觉盒本身不改（留后续票）。
- 三态静态声明口径分歧（声明≠渲染）：归 HUI-2619 议题，本轮按 2625 现行 detector 口径交付。

## NOT_RUN 账目

- HUI-2625 Gate 复裁决：未跑（归 2625 所有者；本包不宣称 touch-web=pass）
- blind family review verdict：not_run（r3 交付不变）
- 生产部署：未做
- PR 合并 / Linear 票状态 / Linear 评论：归 root
- detector/口径改动：未做（归 HUI-2619）
