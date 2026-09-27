-- HUI-1670 FEAT-0171: 顾客预览与手动发布准备。
-- 发布主体只能是参与活动的顾客。本票不授权生产外发,也不保存平台回执:
--   status 只能是 previewed / exported / unknown;
--   platform_post_id、receipt_source 必须为空;
--   outbound_calls 与 reward_triggered 必须为 0。
-- 因此预览、导出、自报都不可能被记成 publish_confirmed。
-- 适配器登记表只留名字,没有任何「启用发布」列。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS customer_publish_attempts (
    id                         TEXT PRIMARY KEY,
    tenant_id                  TEXT NOT NULL REFERENCES tenants(id),
    campaign_id                TEXT NOT NULL REFERENCES campaigns(id),
    link_code                  TEXT NOT NULL,
    platform                   TEXT NOT NULL,
    publisher_subject          TEXT NOT NULL CHECK (publisher_subject = 'activity_customer'),
    copy_text                  TEXT NOT NULL,
    content_version            TEXT NOT NULL,
    account_label              TEXT NOT NULL,
    status                     TEXT NOT NULL CHECK (status IN ('previewed', 'exported', 'unknown')),
    asset_use_accepted         INTEGER NOT NULL DEFAULT 0 CHECK (asset_use_accepted IN (0, 1)),
    confirmed_content_version  TEXT NOT NULL DEFAULT '',
    confirmed_account_label    TEXT NOT NULL DEFAULT '',
    self_reported              INTEGER NOT NULL DEFAULT 0 CHECK (self_reported IN (0, 1)),
    platform_post_id           TEXT NOT NULL DEFAULT '' CHECK (platform_post_id = ''),
    receipt_source             TEXT NOT NULL DEFAULT '' CHECK (receipt_source = ''),
    outbound_calls             INTEGER NOT NULL DEFAULT 0 CHECK (outbound_calls = 0),
    query_count                INTEGER NOT NULL DEFAULT 0 CHECK (query_count >= 0),
    reward_triggered           INTEGER NOT NULL DEFAULT 0 CHECK (reward_triggered = 0),
    created_at                 TEXT NOT NULL,
    updated_at                 TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_customer_publish_campaign
    ON customer_publish_attempts(tenant_id, campaign_id, created_at);

CREATE TABLE IF NOT EXISTS publish_adapter_notes (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    platform     TEXT NOT NULL,
    adapter_name TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    UNIQUE (tenant_id, platform, adapter_name)
);
