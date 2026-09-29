# HUI-1672 跳转矩阵 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让已登记的店内目标、活动目标和授权回跳点到保存的地址，并拒绝未登记外部 URL；三种证据分开，原生唤起保持未验证。

**Architecture:** `extrajump` 继续决定哪些正式地址可展示。过期、撤销和授权回跳是同一包里的纯规则。HTTP 只负责保存、品牌裁决和未知点击。访客页与商家页消费这些字段，不另做一套跳转器。

**Tech Stack:** Go net/http + sqlite 迁移；Next.js 现有内联样式；vitest；`GOWORK=off`。

**Spec:** `docs/superpowers/specs/2026-09-29-hui-1672-jump-matrix-design.md`

## Global Constraints

- 不推倒 PR #19：未配置和非正式地址不展示，拒绝地址不回显，点击保持 unknown。
- `evidence.client_launch` 只能是 `unverified`。`evidence.platform_action` 只能是 `unknown`。
- 不自动关注、加群或付费。
- 不引入 shadcn。不改 leads-engine。
- 不把 Linear 标成 Done。不合并，不 force-push。
- 浏览器端口优先 18260 与 18360。禁用 18101、18230、18330、18120、18084。

---

### Task 1: 纯规则

**Files:**
- Modify: `server/internal/extrajump/extrajump.go`
- Test: `server/internal/extrajump/extrajump_test.go`

**Interfaces:**
- Produces: `PresentAt`、`AllowReturn`、`DecideReturn`、`CanonicalHref`、`SupportMatrix`、`BlockReason`、`ClassOf`、`RecordClick` 的证据字段。

- [ ] 先写失败测试：过期和撤销不带 href；任意外部回跳被拒绝；矩阵里的原生唤起是 unverified、平台动作是 unknown、三个自动开关都是 false；`CanonicalHref` 忽略调用方传入的 URL。
- [ ] 跑 `GOWORK=off go test ./internal/extrajump/`，确认失败原因是函数不存在。
- [ ] 按规格实现最小规则。`Present` 改为调用 `PresentAt(items, time.Now().UTC())`。
- [ ] 再跑同一命令，原有用例和新用例都通过。

### Task 2: 保存、品牌门和未知点击

**Files:**
- Create: `server/internal/db/migrations/0013_jump_matrix.sql`
- Modify: `server/internal/db/db_test.go`
- Modify: `server/internal/store/store_extra_jump.go`
- Modify: `server/internal/httpapi/handlers_extra_jump.go`
- Modify: `server/internal/httpapi/handlers_private_domain.go`
- Modify: `server/internal/httpapi/server.go`
- Test: `server/internal/httpapi/server_extra_jump_test.go`

**Interfaces:**
- Consumes: Task 1 的规则。
- Produces: `PUT/GET /api/v1/campaigns/{id}/extra-jumps`，公共 `extra-jumps` 增加 `return`、`support`、`evidence`；`POST /api/v1/public/links/{code}/returns/clicks`。

- [ ] 先写失败的 HTTP 测试：过期、撤销、回跳、未登记 URL、结束活动、点击体换地址、跨品牌、未授权生命周期。
- [ ] 跑 `GOWORK=off go test ./internal/httpapi/ -run 'TestJumpMatrix|TestExtraJumps'`，确认失败。
- [ ] 加迁移和存储，再接处理器。撤销或过期的企微附加配置不再借道私域展示。
- [ ] 再跑该测试，然后 `GOWORK=off go test ./...`。

### Task 3: 访客页、商家页和 PUT 转发

**Files:**
- Create: `web/src/lib/jump-matrix.ts`
- Create: `web/tests/jump-matrix.test.ts`
- Create: `web/src/components/admin/ExtraJumpPanel.tsx`
- Modify: `web/src/app/c/[code]/page.tsx`
- Modify: `web/src/app/admin/page.tsx`
- Modify: `web/src/app/api/[...path]/route.ts`
- Modify: `web/tests/bff.test.ts`

**Interfaces:**
- Consumes: 公共载荷的 `actions`、`closed`、`return`、`support`，以及点击响应里的 `href` 和 `evidence`。
- Produces: `canonicalHref`、`capabilityGapLines`、`closedNotices`、`authorizedReturnHref`。

- [ ] 先写失败的 vitest：服务端地址不一致就不打开；关闭原因只有失效、撤销、未授权、跨品牌；缺口文案含「原生唤起未验证」且不含自动成功。
- [ ] 跑 `npx vitest run tests/jump-matrix.test.ts`，确认失败。
- [ ] 实现纯函数、访客展示和商家配置。BFF 导出 PUT。
- [ ] `npm test` 通过。

### Task 4: 浏览器轨迹

- [ ] 18260 跑 Go，18360 跑 Next。窄屏 390×844 打开访客页和商家页。
- [ ] 记录点击后的页面原文。网页打开不得写成平台动作成功。报告写「原生唤起未验证」。
- [ ] 退出前停掉本轮进程。
