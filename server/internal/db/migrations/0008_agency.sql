-- HUI-1675 FEAT-0176: 代理与子账号(单级代理 v1,登记制开关 FEATURE_AGENCY)。
-- Additive 数据语义(0004 同款重建纪律:存量行/列逐列原样保留,零数据丢失)。
-- 纪律:
--   - 不复制租户、不动 tenants 语义:商家租户⇄代理两级,层级固定;
--     「多级代理」(代理下再挂代理)v1 不做,deferred 如实声明;
--   - 代理 = 既有成员/角色体系内的代理角色(role='agent'):members 重建仅为
--     扩展 role CHECK 白名单(SQLite 不能改 CHECK),列集/行集与 0004 完全一致;
--     代理的权威不在角色本身,而在激活的代管关系行;无激活关系的 agent 行在
--     authz 一律 fail-closed 拒绝(agency_inactive);
--   - 代管关系 = 显式行、只追加历史:解除 = 落 revoked_at/revoked_by 墓碑,
--     绝不物理删除;同 (tenant_id, agent_principal) 同时至多一条激活
--     (部分唯一索引);解除后再建立 = 新行(全程可回查);
--   - 代开留痕表 agency_subaccount_trail:代理(或 owner 直开)在商家租户内
--     开子账号时同事务落一行,append-only,可回查;留痕独立于成员行生命周期;
--   - 子账号 = 商家租户内的既有成员角色(staff/store_manager),不发明第二套
--     身份/权限体系;代理永不铸造 org_owner/agent(应用层把关)。

PRAGMA foreign_keys = ON;

-- members 重建:role 白名单扩展 'agent'。列集与 0004 完全一致,全部存量行
-- 原样迁回(逐列显式 INSERT SELECT)。
CREATE TABLE members_next (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    principal_ref TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('org_owner','store_manager','staff','agent')),
    store_scope   TEXT REFERENCES stores(id),
    enabled       INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    display_name  TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (tenant_id, principal_ref)
);

INSERT INTO members_next (id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at)
SELECT id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at
FROM members;

DROP TABLE members;
ALTER TABLE members_next RENAME TO members;

CREATE INDEX IF NOT EXISTS idx_members_tenant ON members(tenant_id);
CREATE INDEX IF NOT EXISTS idx_members_store_scope ON members(store_scope);

-- 代管关系行(商家租户 ⇄ 代理主体):只追加历史,解除 = 墓碑。
CREATE TABLE IF NOT EXISTS agency_relations (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    -- agent_principal:代理的身份主体 usr_*(身份归 platform-identity,不设外键)
    agent_principal TEXT NOT NULL,
    note            TEXT NOT NULL DEFAULT '',
    established_by  TEXT NOT NULL DEFAULT '',
    established_at  TEXT NOT NULL,
    revoked_by      TEXT NOT NULL DEFAULT '',
    revoked_at      TEXT
);

-- 同 (租户, 代理主体) 至多一条激活关系;解除后可再建立(新行)。
CREATE UNIQUE INDEX IF NOT EXISTS idx_agency_relations_active
    ON agency_relations(tenant_id, agent_principal) WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agency_relations_tenant ON agency_relations(tenant_id, established_at);
CREATE INDEX IF NOT EXISTS idx_agency_relations_agent ON agency_relations(agent_principal);

-- 代开留痕(append-only,可回查):开子账号时与成员行同事务落一行。
CREATE TABLE IF NOT EXISTS agency_subaccount_trail (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    member_id           TEXT NOT NULL REFERENCES members(id),
    -- 代开所依据的激活关系行;空 = owner 直开(不经代理)
    agency_rel_id       TEXT REFERENCES agency_relations(id),
    -- 代开代理主体 usr_*;空 = owner 直开(不冒充代理身份)
    issued_by_principal TEXT NOT NULL DEFAULT '',
    issued_by_member    TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agency_subaccount_trail_tenant ON agency_subaccount_trail(tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_agency_subaccount_trail_member ON agency_subaccount_trail(member_id);
CREATE INDEX IF NOT EXISTS idx_agency_subaccount_trail_issuer ON agency_subaccount_trail(tenant_id, issued_by_principal, created_at);
