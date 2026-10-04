# HUI-2748 验收矩阵（r2 切片记录）

日期：2026-10-04。分支：hui-2748-r2（BASE=1635486）。Linear 不可达，本文件是本仓证据记录；本仓 commit/PR 链接留待 root 收口时填（标 `待填`）。

上游结论（2026-10-04 已核实）：真实 Motion 消费上游不可用——public-ai 实现在 `internal/motionref`（HUI-2737 已并入 d01ec88，但业务仓不可导入 internal）；`public-ai/sdk/go` 最新 tag `v0.1.3` 无任何 motion 导出且无导出时间表；HUI-2732/2733 在 public-ai 全分支历史无痕迹。依赖 Motion Studio 的验收项保持未满足，不用桩关闭。

## 票面 checkbox 逐项

### 1. 门店资料 → Motion 任务，无需重复录入 — PARTIAL

- 本域已达成：`GET /api/v1/campaigns/{id}/motion-handoff` 把商家声明全量投影。字段→来源：`campaign.*`←`campaigns`；`store.*`←`stores`；`brand.brand_id`←`tenants.brand_id`；`brand.published_brand_id`/`published_host`←`campaign_links` 铸码戳（0009）；`offer.*`/`cta`/`channels`/`aspect_ratios`/`params_version`←`store_motion_requests` 最新行；`landing.short_code`←`campaign_links`；`landing.extra_jump_kinds`←`extra_jump_actions`（排除 revoked）；`landing.authorized_return`←`authorized_returns`；`assets`←`campaign_assets`；品牌上下文复用 `internal/brandctx` 读取路径（上游不可用→`context_available=false`+reason，不伪造）。
- 未达成：「提交到 Motion 任务」。所需上游：public-ai sdk/go motion 导出、HUI-2732、HUI-2733。
- 证据：`server/internal/httpapi/handlers_motion_handoff.go`、`server/internal/httpapi/server_motion_handoff_test.go`（commit 待填）。

### 2. 高频字段更新 0 高价模型调用 — met

- 四层强制：`storemotion.Apply` 不调 probe（`_ = probe`）；生产 probe 恒 nil（`server.go` 的 `StoreMotionProbe` 注释）；SQL CHECK `model_calls=0`/`render_calls=0`（迁移 0018，本轮未动）；scan 回读断言（`store/store_motion.go` `scanStoreMotion`）。
- 测试：`TestStoreMotionParamChangeCallsNoModel`、`TestParamChangeDoesNotCallModelOrRender`、`TestPrivacyNeverBecomesModelPayload`、`TestStoreMotionRecordsParamChangeWithoutRerunClaim`、r2 新增 `TestStoreMotionChannelsRoundTripFormatAndPrivacy`（同样断言 probe 恒零）。

### 3. 一个模板可安全派生多门店 — unmet

- HUI-2733 未完成（public-ai 全分支无痕迹）。本仓只有 per-campaign 参数记录，无模板派生面。
- 所需上游：HUI-2733。

### 4. 顾客端不执行任意未审查代码 — met

- 顾客页零 motion 内容、无外部脚本：本票未改任何 `/c/[code]` 公共页；`web/tests/store-motion-note.test.ts` 锁住组件不 import `app/c` 与 `public-campaign`/`customer-publish`；公共面只读白名单见 `server/internal/httpapi/server.go`（`/api/v1/public` 无会话、GET-only）。

### 5. Motion Render/Web artifact 版本可追溯 — unmet

- 本域无 Motion artifact（上游不可用）。本票 `params_version` + `digest`（sha256，对内容字段稳定，`params_version` 变化必然改变 digest）是可追溯雏形，不宣称达标。
- 所需上游：Motion Render/Web 侧 artifact 记录（HUI-2732/2733 之后的消费面）。

### 6. 不复制 Motion Studio 编辑器 — met

- 本票零编辑器面：无画布、无模板编辑、无渲染管线；只有参数表单（r1）与只读 brief（r2）。

## 公共票切片记录

| 票 | 结论 | 理由 | 所需接口 |
| --- | --- | --- | --- |
| HUI-2812 prodtree | N/A | 本仓 grep 零痕迹；public-ai 实现在 internal，本仓不可导入；sdk/go 无导出 | sdk/go 导出 prodtree 消费面，或公共票所有者明确本仓消费点 |
| HUI-2813 mediause | N/A | 同上：internal 无本仓可导入消费面，sdk/go 无导出 | 同上 |
| HUI-2395 视频契约 | 切片存在，契约消费 N/A | 本仓有 video-templates 登记切片（`internal/videotpl`、`handlers_video_templates.go`），无渲染/成片契约消费点；本票未动它 | 待公共票所有者明确消费面 |
| HUI-2765 SHV | 待公共票所有者明确消费面 | 本仓 grep 无 SHV 消费面痕迹 | 待公共票所有者明确消费面 |
| HUI-2586-2591 UX-R4 | 待公共票所有者明确消费面 | 本仓无 UX-R4 标记；web 已有 product-finish/page-census 体验词汇但未引用 UX-R4 | 待公共票所有者明确消费面 |

## e2e 备注

2628-r2 的 e2e harness 不在本分支（2628 先合并）。Motion Handoff Brief 的 e2e 展示验证在 2628 合并后由该 harness 补，本票以 handler 测试 + vitest（`presentDeclaration` 真执行 + 组件源码级断言）为证据。

## 上游所需清单（解除 unmet 的条件）

1. public-ai sdk/go（v0.1.4+）导出 motion 契约（当前 v0.1.3 无 motion 导出）。
2. HUI-2732（未变门店是否重跑的诚实声明）。
3. HUI-2733（一模板 → 多门店安全派生）。
4. Motion 侧明确「提交到 Motion 任务」的接收面。

本仓 commit/PR：待填（root 收口）。
