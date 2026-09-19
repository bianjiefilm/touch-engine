# 计划:HUI-1747 T1 活动授权留资 → 获客投递(touch-engine)

- 日期:2026-09-19;票:HUI-1747【平台生态 T1】;分支 `hui-1747-t1-leads-capture`(基于 main=ed37cfd)
- brainstorm:指挥者已代答(固定最小表单/真实 directed 客户端/新开关默认 off/单机去重限流/公开撤销入口)。
- 契约输入(只读):public-ai origin/main `docs/order-ecosystem/2026-09-19-hui-1734-directed-events.md`、`cmd/platform-notify/directed.go`、`examples/notify-directed-events/main.go`;leads-engine store.go 仅作字段命名参考。

## 1. 数据模型(additive 迁移 0002_leads.sql)

| 表 | 要点 |
| --- | --- |
| `lead_forms` | 每活动一行挂载点(notice_version/marketing_optin_enabled/enabled),HUI-1679 可视化搭建后续接 |
| `lead_submissions` | 联系方式仅存本表(授权源域);`submission_ref`(sub_,对外唯一引用)、`dedup_key` UNIQUE、consent(告知版本/同意时间)、`sync_state`、`source_version` |
| `leads_outbox` | 与线索行**同事务**写入(kind=submit/revoke,payload_json=事件封套);转发器唯一队列 |
| `lead_audit` | 最小审计:accept/sync_pending/sync_delivered/revoke/revoke_notice_sent/suppressed,无 PII |
| `public_view_stats` | 匿名浏览**纯聚合**计数(code,day,channel,views),结构上不可能进联系人池 |

## 2. 领域包 `internal/leads`(纯逻辑,先行测试)

- 手机号规范化(大陆号宽松校验)→ `PhoneFingerprint = sha256(PEPPER|phone)`;`DedupKey = sha256(PEPPER|campaign|fp|YYYYMMDD)`(单位时间=UTC 日桶;同键幂等返回原记录)。
- 状态机 `accepted → pending_sync → crm_received`;任意活动态 → `rejected`(目标死信/发布拒绝)或 `revoked`(撤销);撤销终态不可逆。
- 封套构造(directed-event/v1):`event_profile{event_id=submission_ref, event_type=lead.authorized_submitted, source_app=touch-engine(固定), target_app=LEADS_TARGET_APP_ID, tenant_scope=campaign.tenant_id(服务端决定), source_ref=submission_ref, source_version, correlation.ref=campaign_id, payload_ref{ref: touch://leads/<ref>, sha256=本地记录规范 JSON 哈希}}`;顶层 payload 仅 consent/provenance 引用与元数据(campaign/store/channel/asset/source_version),**无任何联系方式**。
- PII 预检(第二道防线,镜像 notify 侧启发式):联系方式键名/email/E.164/大陆手机号形态 → 拒发。
- 撤销事件:event_type `lead.consent_revoked`、source_version+1。**决策记录**:E4 v1 枚举冻结为 authorized_submitted/result_ready,扩枚举属新契约版本;本票以 PROVISIONAL 扩展实现,真实 notify 在枚举扩展前会拒绝(桩接受),列入 deferred。
- 内存 IP 滑窗限流(默认 5 次/分);固定 schema 严格解码(DisallowUnknownFields → body 出现 target_tenant/tenant_id/source_app 即 400)。

## 3. notifytask 升级为真实 directed 客户端

`DirectedClient.PostEvent`(POST {base}/internal/v1/notify/events,头 X-PilotSeaView-Internal-Token + X-Notify-App-ID + Idempotency-Key)、`GetDeliveries`(GET .../events/{id}/deliveries)。开关 off/缺配置 → ErrFeatureDisabled/ErrNotConfigured(fail-closed),T0 语义零破坏。

## 4. 配置

`FEATURE_LEADS_CAPTURE`(默认 off)。on 时 Gate 要求:PLATFORM_NOTIFY_BASE_URL、PLATFORM_NOTIFY_TOKEN、`LEADS_TARGET_APP_ID`、`LEADS_PHONE_PEPPER`(指纹椒盐)。off → 四条公开留资路由与两条后台路由一律 404(不可见)。

## 5. 转发器 `leads.Forwarder`

`Tick(ctx)`:outbox 待发行 → 读线索状态(revoked→标记 suppressed 不发)→ PII 预检 → PostEvent → 2xx 记 pending_sync;查 deliveries:delivered→crm_received、dead→rejected;传输失败记账退避(待同步,不阻塞公共页)。`Run(ctx,interval)` 供 main;测试手动 Tick 保证确定性。重启=同库重开,outbox 仍在。

## 6. HTTP 面

公开(feature off→404):`GET .../lead-form`、`POST .../lead-submissions`(201 新建/200 幂等)、`POST .../lead-revocations`(submission_ref+手机号,验证失败统一 404 防枚举)、`POST .../view-events`(匿名聚合)。后台:`POST /api/v1/campaigns/{id}/lead-form`(owner/staff)、`GET .../leads`、`GET .../lead-stats`。

## 7. Web `/c/[code]`

available 态拉 lead-form → 渲染固定轻表单(姓名/手机号/可选微信号)+ 同意确认 + **后续营销独立勾选**;成功页展示 submission_ref 与撤销渠道文案(可发起撤销);加载时发匿名 view 事件。BFF 既有 `/api/*` 透传无需改动;新增 vitest 中继用例。

## 8. TDD 顺序

migration+store(原子性/幂等/撤销)→ 领域纯函数 → notifytask 客户端 → config gate → forwarder → httpapi → E2E(stub notify:字段断言/无手机号明文/待同步→确认/并发/重启/负例/撤销矩阵/浏览隔离)→ web → 全量门禁。

## 9. 验收映射(验收→测试)

| 验收 | 测试 |
| --- | --- |
| 授权提交→桩收到事件→待同步→确认→CRM已接收 | `TestE2EHappyPathDeliversDirectedEvent` |
| 载荷无手机号明文/联系方式键 | 断言封套 JSON 不含 PII(键名+形态值) |
| 同键并发/重投/重启仅一个 submission | 并发 12 goroutine 同手机号;关开重启后 Tick |
| 响应丢失恢复 | 幂等重 POST 返回原 ref |
| 无同意/过期/错商家门店/伪造 target_tenant/撤销后补投 | httpapi 负例矩阵 |
| 撤销:未投递阻止/已投递发撤销事件/重放旧事件不恢复 | E2E 撤销矩阵(桩 inbox 版本守卫) |
| 匿名浏览与线索池隔离 | view 计数 + submissions 为空 + 聚合表无身份列 |
| go test 不劣化(47 全保)/vitest/next build | 门禁 |
