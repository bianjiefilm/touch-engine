# HUI-1896 公共访客收口 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 公共留资进度不再把未观察到的销售接收和负责人跟进写成已发生或未发生，并锁住暂停、结束、不存在和重复提交不会多写一条线索。

**Architecture:** 公共进度只从本地同步状态投影 CRM。`sales_received` 与 `owner_followed_up` 固定为字符串 `unknown`。访客文案在 CRM 已接收时承认接收，并说明跟进未知。页面不采用服务端传来的销售或跟进布尔值。

**Tech Stack:** Go net/http 测试，`GOWORK=off`；Next.js 公共页；Vitest。

**Spec:** `docs/superpowers/specs/2026-09-29-hui-1896-visitor-close-design.md`

## Global Constraints

- 不改商家工作台 `sales_received` 指标，不改「销售已收到 0」空态。
- 不改 leads-engine，不新增订单表，不引入 shadcn/ui。
- 不把 `unknown` 写成 `false` 或「销售已收到」。
- 拒绝营销许可不挡住公开浏览。
- 匿名提交不增加 `members`，不给商家 B 写线索。
- 企微、关注、导航点击保持 `success=false` 且 `platform_result=unknown`。
- Go 测试命令带 `GOWORK=off`。先红后绿。

---

### Task 1: 公共进度保持未知，关闭态不写线索

**Files:**
- Modify: `server/internal/httpapi/server_public_visitor_test.go`
- Modify: `server/internal/httpapi/handlers_public_leads.go`
- Test: `server/internal/httpapi/server_public_visitor_test.go`

**Interfaces:**
- Consumes: `leadsFixture.submitLead`, `publicLeadProgress`
- Produces: JSON `sales_received` and `owner_followed_up` equal the string `unknown`; `crm_received` stays bool

- [ ] **Step 1: Write the failing test**

在 `server_public_visitor_test.go` 增加 `TestVisitorCloseKeepsUnknownAndOneLead`。断言：

- 暂停、结束、不存在的短码提交留资不是 201，且 `lead_submissions` 行数不变，响应带 `paused`、`ended` 或 `not_found`。
- 同意但拒绝营销的提交：`sales_received == "unknown"`，`owner_followed_up == "unknown"`，`crm_received == false`，`creates_platform_account == false`，响应没有 `order` 字段。
- 同一号码把营销许可改成 true，并夹带 `tenant_id`，请求被拒绝或仍是重复；线索仍是 1 条，外发仍是 1 条，商家 B 的线索数仍是 0，`members` 不变，已存行的 `marketing_optin` 仍是 false。

已有 `TestSubmitSaysAcceptedNotSalesReceived` 和 `TestLeadStatusProjectsCRMPausedWithoutClaimingSales` 里的 `sales_received` / `owner_followed_up` 期望从 `false` 改为 `"unknown"`。这一步先只加新测试，先看新测试失败，再改旧断言，避免旧测试在红灯阶段被一起改掉后看不出原因。

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test -count=1 ./internal/httpapi/ -run TestVisitorCloseKeepsUnknownAndOneLead`

Expected: FAIL because `sales_received` is bool `false`, not `"unknown"`.

- [ ] **Step 3: Write minimal implementation**

`publicLeadProgress` 把 `sales_received` 和 `owner_followed_up` 改成字符串 `unknown`。`crm_received` 与 `creates_platform_account` 不变。

- [ ] **Step 4: Run test to verify it passes**

把两个旧测试的对应断言改成 `"unknown"`，再跑：

`GOWORK=off go test -count=1 ./internal/httpapi/ -run 'TestVisitorCloseKeepsUnknownAndOneLead|TestSubmitSaysAcceptedNotSalesReceived|TestLeadStatusProjectsCRMPausedWithoutClaimingSales'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/httpapi/server_public_visitor_test.go server/internal/httpapi/handlers_public_leads.go
git commit -m "fix(hui-1896): 公共进度不把未知跟进写成否定"
```

### Task 2: CRM 已接收的文案不宣称负责人跟进

**Files:**
- Modify: `web/src/lib/visitor-experience.ts`
- Modify: `web/src/app/c/[code]/page.tsx`
- Test: `web/tests/visitor-experience.test.ts`

**Interfaces:**
- Consumes: `leadOutcomeCopy`
- Produces: CRM 已接收文案包含「客户系统已接收」和「还不知道」，不包含「销售已收到」「负责人已跟进」「这还不是负责人跟进」。`salesReceived: true` 或 `ownerFollowedUp: true` 也不能改变这句。

- [ ] **Step 1: Write the failing test**

扩展 `leadOutcomeCopy` 的调用，传入 `salesReceived: true` 与 `ownerFollowedUp: true`，并断言 CRM 已接收文案。

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run tests/visitor-experience.test.ts`

Expected: FAIL on 「还不知道」或仍含「这还不是负责人跟进」。

- [ ] **Step 3: Write minimal implementation**

`leadOutcomeCopy` 增加可选参数并忽略它们。CRM 已接收句子改为：`已提交给${merchant}，对方客户系统已接收。负责人是否已经跟进，这里还不知道。`

页面调用时固定传入 `salesReceived: "unknown"` 和 `ownerFollowedUp: "unknown"`，不读取提交响应里的销售或跟进字段。

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run tests/visitor-experience.test.ts`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/visitor-experience.ts web/src/app/c/\[code\]/page.tsx web/tests/visitor-experience.test.ts
git commit -m "fix(hui-1896): 客户系统已接收不等于负责人已跟进"
```

### Task 3: 导航、关注、企微点击仍是未知

**Files:**
- Modify: `server/internal/httpapi/server_extra_jump_test.go`
- Test: `server/internal/httpapi/server_extra_jump_test.go`

**Interfaces:**
- Consumes: `POST /api/v1/public/links/{code}/extra-jumps/{kind}/clicks`
- Produces: 三条点击的 `platform_result` 都是 `unknown`，且不新增线索

- [ ] **Step 1: Write the failing test only if the current handler can record success**

若现有约束已经拒绝成功点击，这个测试会先绿。那时按回归循环：先看它通过，临时把响应改成成功并确认测试变红，再恢复。

配置三条正式 https 地址（企微、关注、导航），各点一次。断言响应和数据库都不是成功，线索数不变。

- [ ] **Step 2: Commit if the test is new**

```bash
git add server/internal/httpapi/server_extra_jump_test.go
git commit -m "test(hui-1896): 导航关注和企微点击保持未知"
```

### Task 4: 验证

- [ ] `cd server && GOWORK=off go test ./...`
- [ ] `cd web && npx vitest run`
- [ ] 窄屏浏览器两条路径，证据写入报告，不把截图或桩令牌提交进仓库，除非它只是不含秘密的点击记录。本计划不要求把浏览器日志提交进 git。
