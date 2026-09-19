# 计划:HUI-1674 FEAT-0175 连锁总部管控多门店(touch-engine)

- 日期:2026-09-19;票:HUI-1674(P1);分支 `hui-1674-feat-0175-multi-store`(基于 main=0def155)
- brainstorm:指挥者已代答(角色模型 org_owner/store_manager + members.store_scope;权限矩阵见 §2;停用门店=禁新建活动+公共页标注「门店暂不可用」,存量活动逐个显式处理、不做级联静默下线;活动/素材/标签按 store 作用域自然隔离、不复制文件;web 门店管理页+作用域感知;不做多级代理 1675/门店独立计费/跨租户连锁/门店间素材共享池)。
- 红线:additive 演进不推翻 T0 stores 单门店模型;服务端强制作用域(伪造头/参数无效);跨门店读取 404 掩码;存量单门店租户升级后数据与权限不变(owner→org_owner 语义等价迁移);现有 109 个 go 测试函数全保留。

## 1. 迁移 `0004_multi_store.sql`(additive 数据语义)

- `stores` ADD COLUMN `status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled'))`。
- `members` 角色扩展无法 ALTER CHECK → 重建表(数据保留):
  新表 `role CHECK IN ('org_owner','store_manager','staff')` + `store_scope TEXT NULL REFERENCES stores(id)` + `UNIQUE(tenant_id, principal_ref)`;
  `INSERT … SELECT … CASE role WHEN 'owner' THEN 'org_owner' ELSE role END` 迁移存量行(owner→org_owner 语义等价;staff 不变);重建 idx_members_tenant。
- 更新 `db_test.go` 表清单期望值不变(无新表)。

## 2. 鉴权 `internal/authz`(TDD 先行)

- 角色:`RoleOrgOwner="org_owner"`、`RoleStoreManager="store_manager"`、`RoleStaff="staff"`;`"owner"` 仅作为输入别名(CanonicalRole 映射到 org_owner),迁移后不再存储。
- `Member` 增 `StoreScope string`(""=全门店/总部;非空=仅该门店)。
- `RecordScope` 增 `StoreID`(记录所属门店,""=租户级/未绑定);`ReasonOutOfScope="out_of_scope"`;新 `ActionManageStores="manage_stores"`。
- 矩阵:
  - manage_members / manage_stores / export_qr:仅 org_owner;
  - manage_tags:org_owner 全量;store_manager 且 rec.StoreID==member.StoreScope(非空)→ 允许,否则 out_of_scope;staff 拒绝;
  - create/read_list/read_record/update:org_owner/staff 全租户(向后兼容);store_manager 仅 rec.StoreID==StoreScope,否则 out_of_scope。
- HTTP 掩码规则:out_of_scope 且对象已存在(按租户取到)→ 404 not_found(跨店不可见);create/参数级越权 → 403 out_of_scope。

## 3. 存储层 `internal/store`

- `Member`:增 StoreScope(CreateMember/scanMember/UpdateMember/ListMembers);`StoreRecord`:增 Status。
- 门店管理:`UpdateStore(id,tenantID,name,address)`、`SetStoreStatus(id,tenantID,status)`(无级联:活动/短码/标签逐条保持原状)。
- 作用域查询:`ListCampaignsByStore(tenantID, storeID)`(门店经理活动列表)。
- 标签面复用现有 TagFilter.StoreID;`ResolveLink` 增 `StoreUnavailable bool`(仅 available 态且活动绑定门店已停用时置位,公共面零新增模型)。

## 4. HTTP 面 `internal/httpapi`

- 新路由: `PATCH /api/v1/stores/{id}`(org_owner 改名/地址)、`POST /api/v1/stores/{id}/status`(org_owner 停用/启用)。
- 门店读写:POST /stores 仅 org_owner;GET /stores org_owner/staff 全量、store_manager 仅本店;GET /stores/{id} 作用域校验(跨店 404)。
- 活动面:create 校验 store 存在且**未停用**(停用店新建/改绑入 → 400 store_disabled;移出停用店允许);store_manager 的 create 必须 store_id==本店;get/patch/status/assets/links/qrcode/leads 先按租户取活动再按 StoreID 作用域校验(跨店 404);list store_manager 强制本店过滤(未绑定门店的活动=总部,不可见)。
- NFC 面:tag-groups 保持 org_owner;tags 列表/导出 store_manager 强制 StoreID=本店过滤;get/patch/status/delete 逐条作用域校验(跨店 404);batch:create 的 store_id 空则默认本店、显式他店 403,且目标活动必须本店;patch 换绑 link 的目标活动必须本店;store_manager 不参与分组(group_id 提供即 403)。
- 成员面(仅 org_owner):role 输入接受 owner→org_owner 别名 + store_manager(必带 store_scope,门店须存在)+ staff;非 store_manager 带 store_scope 400;PATCH 同规则,store_manager 清空 scope 拒绝。whoami 增 store_scope。
- 公共面:`publicLinkView` 增 `store_notice`(白名单常量 "store_unavailable",仅 available 且绑定门店停用时出现;不出现门店内部字段)。

## 5. provision

`provision-member`:-role 接受 owner(别名)/org_owner/store_manager/staff;store_manager 必带 `-store <store_id>`(须存在于租户);org_owner/staff 拒绝 -store。operator 工具,不进 HTTP。

## 6. web

- `/admin`:门店区显示状态徽标;org_owner 可改店(名称/地址)、停用/启用;store_manager 仅见本店、无建店表单;创建活动门店下拉仅列可用店(manager 锁定本店);NFC 区 manager 隐藏分组管理、门店锁定;whoami 展示作用域(总部/门店名)。
- `/c/[code]` + `lib/public-state.ts`:available 态携带 store_notice → 页面标注「门店暂不可用」,五态与其余逻辑零改动;vitest 覆盖 notice 映射。

## 7. TDD 顺序

authz 矩阵扩展(org_owner/store_manager×store 作用域×伪造)→ store 层(成员 scope/门店状态/作用域查询/ResolveLink notice)→ db 迁移(存量 owner 行→org_owner、stores 默认 active)→ httpapi 作用域矩阵(3 店:org_owner 全量、A 店 manager 仅 A、跨店 404、停用店拒新建、伪造 store 头无效)→ 公共 notice → provision → web。

## 8. 真实非生产 E2E(Go httptest,真 sqlite + stub identity)

- org_owner 建 3 店 → 各建活动/短码/标签 → A 店 manager 仅见/操作 A 店(列表过滤+记录 404);org_owner 全量可见。
- store_manager 建店/删店 403;跨店活动 get/patch/status 404;batch 显式他店 403;伪造 X-Store 头/查询参数不改变服务端裁决。
- 停用 B 店:新建绑 B 店活动 400 store_disabled;B 店存量活动仍在列表(不静默消失)、公共页 available+store_notice;启用恢复。
- 存量兼容:迁移后 owner 行为 org_owner、staff 权限不变、未绑定门店活动归总部。

## 9. 门禁

`go test ./... -count=1` 全绿(≥109 函数全保留);`vitest run` 全过;`next build` 成功;提交(不 push)。
