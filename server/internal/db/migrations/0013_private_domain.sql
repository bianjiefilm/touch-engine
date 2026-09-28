-- HUI-1673 FEAT-0174: 私域入口只保存商家配置。
-- 点击不能写成添加成功、进群成功、新联系人、留资、CRM、发奖、核销或已接通。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS private_domain_entries (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    campaign_id TEXT NOT NULL REFERENCES campaigns(id),
    kind        TEXT NOT NULL CHECK (kind IN ('wecom', 'community')),
    href        TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    updated_at  TEXT NOT NULL,
    UNIQUE (campaign_id, kind)
);

CREATE TABLE IF NOT EXISTS private_domain_clicks (
    id                   TEXT PRIMARY KEY,
    tenant_id            TEXT NOT NULL REFERENCES tenants(id),
    campaign_id          TEXT NOT NULL,
    kind                 TEXT NOT NULL CHECK (kind IN ('wecom', 'community')),
    event_name           TEXT NOT NULL CHECK (event_name IN ('click_wecom', 'click_community')),
    recorded_as          TEXT NOT NULL CHECK (recorded_as = 'click'),
    success              INTEGER NOT NULL CHECK (success = 0),
    platform_result      TEXT NOT NULL CHECK (platform_result = 'unknown'),
    added                INTEGER NOT NULL CHECK (added = 0),
    joined               INTEGER NOT NULL CHECK (joined = 0),
    contact_created      INTEGER NOT NULL CHECK (contact_created = 0),
    lead_created         INTEGER NOT NULL CHECK (lead_created = 0),
    crm_imported         INTEGER NOT NULL CHECK (crm_imported = 0),
    reward_triggered     INTEGER NOT NULL CHECK (reward_triggered = 0),
    connected            INTEGER NOT NULL CHECK (connected = 0),
    redemption           TEXT NOT NULL CHECK (redemption = 'unknown'),
    followed             INTEGER NOT NULL CHECK (followed = 0),
    silent_add           INTEGER NOT NULL CHECK (silent_add = 0),
    forced_join          INTEGER NOT NULL CHECK (forced_join = 0),
    background_marketing INTEGER NOT NULL CHECK (background_marketing = 0),
    created_at           TEXT NOT NULL
);
