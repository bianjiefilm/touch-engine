-- HUI-1674 FEAT-0175: 连锁总部管控多门店(additive 数据语义:T0 数据零丢失)。
-- 纪律:
--   - 停用门店 = 禁止新建活动 + 公共页标注「门店暂不可用」;存量活动/短码/
--     标签逐个显式处理,绝不级联静默下线(应用层不传播任何删除/停用)。
--   - 角色扩展:owner(总部语义)→ org_owner 语义等价迁移;新增 store_manager
--     (门店经理,store_scope 非空)。SQLite 不能改 CHECK,按标准流程重建
--     members 表,全部存量行保留。
--   - store_scope 是服务端作用域事实:NULL=总部/全门店;非空=仅该门店。

PRAGMA foreign_keys = ON;

-- 门店启停:默认 active(存量门店不受影响)。
ALTER TABLE stores ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled'));

-- members 重建:role 白名单扩展 + store_scope(可空,引用门店)。
CREATE TABLE members_next (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    principal_ref TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('org_owner','store_manager','staff')),
    store_scope   TEXT REFERENCES stores(id),
    enabled       INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    display_name  TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (tenant_id, principal_ref)
);

-- 语义等价迁移:T0 的 owner(租户全权)即连锁模式下的 org_owner(总部)。
INSERT INTO members_next (id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at)
SELECT id,tenant_id,principal_ref,
       CASE role WHEN 'owner' THEN 'org_owner' ELSE role END,
       NULL,
       enabled,display_name,created_by,created_at,updated_at
FROM members;

DROP TABLE members;
ALTER TABLE members_next RENAME TO members;

CREATE INDEX IF NOT EXISTS idx_members_tenant ON members(tenant_id);
CREATE INDEX IF NOT EXISTS idx_members_store_scope ON members(store_scope);
