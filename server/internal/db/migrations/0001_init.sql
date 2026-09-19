-- HUI-1746 T0 domain root for the touch-engine app (碰一碰:商家线下活动/内容分发).
-- Minimal fields only; extensions belong to FEAT-0163..0178. Every business row is
-- tenant-scoped and audited. Material rows are REFERENCES to platform upload assets
-- (asset_id + version); files are never copied into this app.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS tenants (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- Platform members: a reference to a platform-identity principal plus the tenant role.
-- 身份纪律:principal_ref 只能来自 identity 会话解析,不可变;绝不从手机号/邮箱派生;
-- 本表不代表平台账户的存在或创建。
CREATE TABLE IF NOT EXISTS members (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    principal_ref TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('owner','staff')),
    enabled       INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    display_name  TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (tenant_id, principal_ref)
);

-- Stores: single-store minimal fields only; multi-store governance is out of T0 scope.
CREATE TABLE IF NOT EXISTS stores (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    name       TEXT NOT NULL,
    address    TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Campaigns: 商家线下活动。四态 draft/active/paused/ended;有效期窗口(starts_at/ends_at,
-- RFC3339,可空=不设界);store_id 可空;order_ref 是可选的不透明来源私有引用(order-handoff
-- v1 语义),本应用永不解释其内容。
CREATE TABLE IF NOT EXISTS campaigns (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    title          TEXT NOT NULL,
    public_content TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','active','paused','ended')),
    starts_at      TEXT NOT NULL DEFAULT '',
    ends_at        TEXT NOT NULL DEFAULT '',
    store_id       TEXT REFERENCES stores(id),
    order_ref      TEXT,
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

-- Controlled locating short codes: opaque random code -> campaign mapping.
-- 红线:短码/链接不嵌服务凭证、客户信息或任意跳转 URL —— 本表没有承载这些的字段,
-- 码由服务端随机生成(见 internal/campaign shortcode)。
CREATE TABLE IF NOT EXISTS campaign_links (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    campaign_id TEXT NOT NULL REFERENCES campaigns(id),
    code        TEXT NOT NULL UNIQUE,
    enabled     INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- Material references: platform upload asset_id + immutable version. 不复制文件;
-- 授权事实归平台 upload 侧,本表只是租户作用域的引用记录。
CREATE TABLE IF NOT EXISTS campaign_assets (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    campaign_id TEXT NOT NULL REFERENCES campaigns(id),
    asset_id    TEXT NOT NULL,
    version     TEXT NOT NULL,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    UNIQUE (campaign_id, asset_id, version)
);

CREATE INDEX IF NOT EXISTS idx_members_tenant ON members(tenant_id);
CREATE INDEX IF NOT EXISTS idx_stores_tenant ON stores(tenant_id);
CREATE INDEX IF NOT EXISTS idx_campaigns_tenant ON campaigns(tenant_id);
CREATE INDEX IF NOT EXISTS idx_links_tenant ON campaign_links(tenant_id);
CREATE INDEX IF NOT EXISTS idx_links_campaign ON campaign_links(campaign_id);
CREATE INDEX IF NOT EXISTS idx_assets_tenant ON campaign_assets(tenant_id);
CREATE INDEX IF NOT EXISTS idx_assets_campaign ON campaign_assets(campaign_id);
