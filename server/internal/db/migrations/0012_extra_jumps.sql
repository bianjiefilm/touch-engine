-- HUI-1672 FEAT-0173: 活动页附加跳转只保存配置。
-- 点击行不能写成添加成功、关注成功、留资、CRM 导入、发奖或发布成功。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS extra_jump_actions (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    campaign_id TEXT NOT NULL REFERENCES campaigns(id),
    kind        TEXT NOT NULL CHECK (kind IN ('wifi', 'navigate', 'review', 'wecom', 'follow')),
    href        TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    updated_at  TEXT NOT NULL,
    UNIQUE (campaign_id, kind)
);

CREATE TABLE IF NOT EXISTS extra_jump_clicks (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    campaign_id      TEXT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('wifi', 'navigate', 'review', 'wecom', 'follow')),
    recorded_as      TEXT NOT NULL CHECK (recorded_as = 'click'),
    success          INTEGER NOT NULL CHECK (success = 0),
    platform_result  TEXT NOT NULL CHECK (platform_result = 'unknown'),
    added            INTEGER NOT NULL CHECK (added = 0),
    followed         INTEGER NOT NULL CHECK (followed = 0),
    lead_created     INTEGER NOT NULL CHECK (lead_created = 0),
    crm_imported     INTEGER NOT NULL CHECK (crm_imported = 0),
    reward_triggered INTEGER NOT NULL CHECK (reward_triggered = 0),
    publish_success  INTEGER NOT NULL CHECK (publish_success = 0),
    created_at       TEXT NOT NULL
);
