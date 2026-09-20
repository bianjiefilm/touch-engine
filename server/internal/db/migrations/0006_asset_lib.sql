-- HUI-1666 FEAT-0167: 商家素材库 v1(素材登记与组织面)。
-- Additive only:既有表零改动。纪律:
--   - v1 不建物理文件仓库:lib_assets 是对 platform-upload 产物(asset_id)的
--     引用记录 + sha256 版本锚点;大文件零进本库;
--   - 授权语义 v1 = 引用级声明:source/purpose/grant_ref 必填(应用层
--     assetlib.ValidateRegistration 把关);grant_ref 在 HUI-1732(物理授权
--     资产版本面)落地前是不透明声明引用,本表不设外键到任何授权表;
--   - 登记唯一(引用显式登记,无隐式/通配资产):同租户内 (asset_ref, sha256,
--     candidate) 唯一 —— 正式行(candidate=0)与回流候选行(candidate=1)可并存
--     (同一引用先入候选、复核后另行正式登记),同键重复登记 = 唯一冲突
--     (应用层拒绝,绝不静默改写);候选标记只是分流标记,不动引用与版本字段;
--   - 池:租户内名称唯一;池内素材引用 (pool_id, asset_id) 唯一,同一素材
--     引用可进多个池(池间共享);
--   - 选择台账只追加:每次调取冻结 asset_id/asset_ref/sha256 快照,源素材
--     后续更新不自动替换已选内容;同键 (pool_id, select_key, day_utc,
--     candidate) 幂等;台账独立于池的生命周期(pool_id 不设外键,删池不删台账);
--     chosen_item_id 同样不设外键(池删除后台账行保持完整可审计)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS lib_assets (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    asset_ref   TEXT NOT NULL,
    sha256      TEXT NOT NULL,
    media_type  TEXT NOT NULL CHECK (media_type IN ('image','video','bgm')),
    source      TEXT NOT NULL CHECK (source IN ('product_photo','aicut','merchant_upload')),
    purpose     TEXT NOT NULL,
    grant_ref   TEXT NOT NULL,
    store_id    TEXT REFERENCES stores(id),
    tags        TEXT NOT NULL DEFAULT '',
    candidate   INTEGER NOT NULL DEFAULT 0 CHECK (candidate IN (0,1)),
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (tenant_id, asset_ref, sha256, candidate)
);

CREATE INDEX IF NOT EXISTS idx_lib_assets_tenant ON lib_assets(tenant_id, created_at);

CREATE TABLE IF NOT EXISTS lib_pools (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    name       TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS lib_pool_items (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    pool_id    TEXT NOT NULL REFERENCES lib_pools(id),
    asset_id   TEXT NOT NULL REFERENCES lib_assets(id),
    created_at TEXT NOT NULL,
    UNIQUE (pool_id, asset_id)
);

CREATE INDEX IF NOT EXISTS idx_lib_pool_items_pool ON lib_pool_items(pool_id, created_at, id);

CREATE TABLE IF NOT EXISTS lib_selections (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    pool_id        TEXT NOT NULL,
    select_key     TEXT NOT NULL,
    day_utc        TEXT NOT NULL,
    chosen_item_id TEXT NOT NULL,
    asset_id       TEXT NOT NULL REFERENCES lib_assets(id),
    asset_ref      TEXT NOT NULL,
    sha256         TEXT NOT NULL,
    candidate      INTEGER NOT NULL DEFAULT 0 CHECK (candidate IN (0,1)),
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    UNIQUE (pool_id, select_key, day_utc, candidate)
);

CREATE INDEX IF NOT EXISTS idx_lib_selections_tenant ON lib_selections(tenant_id, pool_id, created_at);
