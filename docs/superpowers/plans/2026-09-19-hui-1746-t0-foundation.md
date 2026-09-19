# 计划:HUI-1746【平台生态 T0】碰一碰独立应用与活动底座

- 日期:2026-09-19
- 票:HUI-1746;分支 `hui-1746-t0-foundation`(本地,不 push)
- 边界输入(只读,经 `git show origin/main:<path>`,不 checkout 其他仓):
  - public-ai `docs/integration-guide.md`(identity login/refresh/session resolve/revocations 端点、内部令牌头 `X-PilotSeaView-Internal-Token` + `X-App-ID`、`*_APP_TOKENS` 每 app × 每服务一把、legacy token 禁用)
  - public-ai `docs/adr/0001-platform-ecosystem-e0-incremental-baseline.md`(D3 所有权:门店/活动/奖励归碰一碰应用;D4 两链不混账;D5 身份≠租户成员≠资源授权≠付费权限;D1 三规划名待立项→本票即 touch-engine 立项底座)
  - public-ai `docs/order-ecosystem/2026-09-19-hui-1723-repos-sha-inventory.md` §4(平台服务配置键名;`UPLOAD_APP_TOKENS` / `NOTIFY_APP_TOKENS` / `IDENTITY_APP_TOKENS`)
  - public-ai `docs/contracts/app-registry/v1/README.md`(app-registry/v1 静态事实;`supported_source_kinds` 含 `campaign`;`DecideLaunch` 四条件分离——registry/本应用均不自 derive 授权或付费权益)
  - public-ai `examples/identity-asset-adaptation/main.go`(E2 适配姿态:服务端派生 tenant scope、伪造声明折叠为裸 unknown、fail-closed 三态、无 mock/共享目录/万能 token 路径)
  - 姊妹仓 `D:/Projects/product-image-engine`(59af1a0)与 `D:/Projects/leads-engine`(3ffd157)仅作布局参考;三仓代码独立,不共享模块。

## 定位

碰一碰核心 = 商家线下活动 / 内容分发,不是接单子模块。本票填补 FEAT-0163~0178 十五张功能票之前缺失的独立应用、公共服务适配与领域骨架。

## 目标(本票只做)

独立碰一碰应用的 T0 底座:独立部署单元 + 平台接入面(Identity 登录 + Upload 素材引用;Notify/Task 仅脚手架)+ 领域根骨架 + 公共活动页/商家后台权限完全分离 + 受控短码路由五态。

**不做**:多门店治理、素材文件复制、真实事件订阅(Notify/Task 真实现)、顾客触达触发的任何生成/付费调用(代码路径上不存在)、奖励/核销(FEAT 后续票)。

## 架构拍板(票面已定,此处落为工程事实)

1. `server/`:Go,`net/http`(1.22+ ServeMux,不引路由库)+ `modernc.org/sqlite`(纯 Go,无 cgo,Windows 可测)+ `embed` 迁移 + env 配置。监听 `127.0.0.1:18240`(避开 18101-18107 / 18220 / 18230 / 18320)。
2. `web/`:Next.js app router;`src/app/admin/*` 商家后台路由区、`src/app/c/[code]` 公共活动页路由区;BFF 即 `src/app/api/*`,页面只打 `/api/*`;dev/start 端口 `18340`。
3. `deploy/`:env 样例(只有键名与注释,无真值)。
4. 身份链:浏览器 Cookie → Next BFF 转发 → Go server 校验 `X-Internal-Token` + 自行向 platform-identity `POST /internal/v1/identity/session/resolve` 解析 session(不信任 BFF 转述的 principal)。解析失败/未配置 → fail-closed 显式 503。
5. 公共活动页完全无会话:只读明确公开字段(标题/有效期/公开内容);游客写操作一律 401/403;公共响应不含后台存在性信息(租户 id、principal、内部 asset id、订单引用一律不出现在公共 payload)。
6. 短码:服务端生成的随机不透明码(无意义 payload);`campaign_links.code` 全局唯一;路由解析只输出五态之一;非有效态统一 HTTP 404 + 明确状态文案(统一状态码防止"探测存在性")。
7. 重型生成是商家主动行为:代码里公共路由到任何生成/付费调用之间不存在调用边(FEAT 票也必须在后台会话下实现)。本票自检项。
8. E2E 身份/素材桩:仿 platform-identity / platform-upload 的独立小程序,**只放 `_reports/hui-1746-t0/evidence/`**,不进生产代码路径(server 生产路径只有平台 HTTP 一种)。报告标注 limitation。

## 领域根(最小表;每行 tenant 作用域 + created_at/updated_at/created_by 审计)

| 表 | 最小字段 | 后续票扩展点 |
|---|---|---|
| tenants | id,name | — |
| members | tenant_id, principal_ref(平台 principal,不可变), role(owner/staff), enabled | 组织结构 |
| stores | tenant_id, name, address(单门店最小字段;多门店治理不做) | 门店治理 |
| campaigns | tenant_id, title, public_content, status(draft/active/paused/ended), starts_at, ends_at, store_id, order_ref(NULL 可选,不透明引用) | FEAT-0163~0178 活动详情/奖励 |
| campaign_links | tenant_id, campaign_id, code(全局唯一), enabled(可停用) | 渠道/投放维度 |
| campaign_assets | tenant_id, campaign_id, asset_id+version(引用平台 upload 资产,不复制文件) | 素材治理 |

## 身份纪律(硬边界,测试先行)

1. members 只能经 owner 管理端点以 identity 解析出的 principal_ref 创建;不接受用户输入的 email/手机号派生 principal。
2. principal 无 member 行 → 403,不自动入租户;disabled member → 403;跨租户 → 403;伪造 X-Tenant-ID 只会得到同一裸 403。
3. members.principal_ref 不可变(PATCH 拒改)。
4. 游客(无会话)打任何后台 API → 401;游客对公共面只有 GET;公共面永不写库。

## 权限矩阵(服务端强制;测试逐格覆盖)

| 动作 | owner | staff | 游客(无会话) |
|---|---|---|---|
| 建/改门店、活动、素材引用、短码 | ✅ | ✅ | ❌401 |
| 读本租户列表/详情 | ✅ | ✅ | ❌401 |
| 成员管理 | ✅ | ❌403 | ❌401 |
| 暂停/恢复/结束活动 | ✅ | ✅ | ❌401 |
| 跨租户任何读写 | ❌403 | ❌403 | —(无会话先 401) |
| 公共活动页 GET(有效短码) | — | — | ✅ 只读公开字段 |
| 公共面写操作 | — | — | ❌401/405 |

## 短码路由五态(公共面;非有效态统一 404,状态文案区分)

| 态 | 条件 | 页面文案 |
|---|---|---|
| 有效 | link.enabled ∧ campaign=active ∧ 窗口含 now | 活动进行中 + 公开字段 |
| 停用 | link.enabled=0 | 链接已停用 |
| 过期 | now>ends_at(或 campaign=ended,或 now<starts_at 未开始) | 活动已结束/未开始 |
| 暂停 | campaign=paused(或 draft 未发布) | 活动暂停 |
| 不存在 | 短码查无 | 活动不存在 |

短码载荷断言:码本为无分隔 base32 风格随机串,不含租户/principal/URL/token 等任何结构化信息(测试断言)。

## 平台接入(本票范围)

- Identity:登录/刷新/登出/会话解析走 BFF→server→identity;`TOUCH_APP_ID=touch-engine`(平台侧 `AUTH_APPS` 登记);专用 identity 令牌(平台侧 `IDENTITY_APP_TOKENS`)。
- Upload:素材引用校验客户端;`FEATURE_UPLOAD` 默认 off——off 时创建素材引用显式 503 `upload_disabled`(不伪造授权);on 时调 platform-upload 校验 asset_id+version,失败显式 503 `upload_unavailable`。
- Notify/Task:仅 client 脚手架 + env 配置 + `FEATURE_NOTIFY`/`FEATURE_TASK` 开关(默认 off);off 或缺 token 时调用返回显式错误,不伪造事件。
- 配置门:缺 identity base URL / 内部令牌 → 启动仍可(健康探针可见缺键清单),但一切鉴权动作 fail-closed 显式报错(503 `config_gate` / `identity_unavailable`),绝不伪造成功。

## 安全基线

- 公共响应白名单字段(title/public_content/starts_at/ends_at/状态文案),测试断言无 tenant_id/principal/asset 内部字段/order_ref 泄漏。
- SQL 全参数化;审计字段全表必备;日志不含 token/短码原文之外的用户敏感信息。
- 短码格式校验(字符集+长度),防注入与路径拼接。

## 任务序(TDD:先测试后实现)

1. Go 模块脚手架 + `go.mod`(modernc.org/sqlite)+ 配置加载与配置门测试。
2. db:embed 迁移 + 迁移测试(表/约束存在)。
3. campaign 状态机 + 短码生成/载荷断言测试;authz 租户/角色矩阵测试(纯域逻辑)。
4. identity / upload / notifytask client(httptest 契约测试)。
5. store CRUD + 租户隔离测试。
6. httpapi:auth、admin CRUD、公共路由五态;后台×游客×租户矩阵×HTTP 集成测试;公开字段白名单断言。
7. web/:Next.js BFF 代理;admin 与 public 两路由区页面;vitest:代理语义 + 后台×游客×租户矩阵经 BFF 层复验。
8. `npm run build` 绿;`go test ./...` 绿。
9. E2E(_reports/hui-1746-t0/evidence):identity+upload stub + server 起停;验收:登录→建无订单活动→选授权素材引用→保存→重登继续→暂停/恢复;A/B 商家互不串;游客只读公开字段、写 401/403;短码五态矩阵;短码载荷断言。
10. README、deploy 样例、REPORT.md、自查 code-review、提交(带 HUI-1746,不 push)。

## 验收对照(E2E,本机非生产)

- [ ] 平台登录(evidence 桩,limitation 已标注)→ 创建无订单活动 → 选授权素材引用 → 保存 → 重登继续;暂停/恢复
- [ ] A/B 两商家互不串门店/活动/素材;游客只读公开字段,写操作 401/403
- [ ] 短码路由五态;短码不含敏感信息(payload 断言)
- [ ] `go test ./...`(server)全绿;BFF 测试覆盖后台×游客×租户矩阵;`npm run build` 成功
