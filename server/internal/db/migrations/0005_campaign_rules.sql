-- HUI-1676 FEAT-0177: 活动规则配置(确定性规则集,挂在既有 campaigns 维度上)。
-- Additive only:既有表零改动。纪律:
--   - 规则集是每活动一行(UNIQUE campaign_id);上限字段 NULL = 不限/未设,
--     非负整数才合法(应用层 campaignrules.Validate 双重把关);
--   - 变更不静默改写:每次 create/update/delete 都在 campaign_rule_revisions
--     落一条全量快照修订(revision 单调递增),历史版本可查;
--   - 服务端单点判定:本表只服务评估器输入与 owner 治理面,公共面不回显规则值。
--   - 不建指向不存在表的外键:campaigns/tenants 均为仓内既有表。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS campaign_rules (
    id                               TEXT PRIMARY KEY,
    tenant_id                        TEXT NOT NULL REFERENCES tenants(id),
    campaign_id                      TEXT NOT NULL REFERENCES campaigns(id),
    reward_threshold                 INTEGER,
    daily_publish_limit              INTEGER,
    duplicate_publish_window_hours   INTEGER,
    per_contact_daily_submission_cap INTEGER,
    version                          INTEGER NOT NULL DEFAULT 1,
    created_by                       TEXT NOT NULL DEFAULT '',
    created_at                       TEXT NOT NULL,
    updated_at                       TEXT NOT NULL,
    UNIQUE (campaign_id),
    CHECK (reward_threshold                 IS NULL OR reward_threshold                 >= 0),
    CHECK (daily_publish_limit              IS NULL OR daily_publish_limit              >= 0),
    CHECK (duplicate_publish_window_hours   IS NULL OR duplicate_publish_window_hours   >= 0),
    CHECK (per_contact_daily_submission_cap IS NULL OR per_contact_daily_submission_cap >= 0)
);

-- 修订表:每次规则变更(create/update/delete)一条全量快照,revision 按
-- 活动内单调递增;delete 修订的四个规则字段为 NULL(清除后的状态)。
CREATE TABLE IF NOT EXISTS campaign_rule_revisions (
    id                               TEXT PRIMARY KEY,
    tenant_id                        TEXT NOT NULL,
    campaign_id                      TEXT NOT NULL,
    revision                         INTEGER NOT NULL,
    action                           TEXT NOT NULL CHECK (action IN ('create','update','delete')),
    reward_threshold                 INTEGER,
    daily_publish_limit              INTEGER,
    duplicate_publish_window_hours   INTEGER,
    per_contact_daily_submission_cap INTEGER,
    changed_by                       TEXT NOT NULL DEFAULT '',
    created_at                       TEXT NOT NULL,
    UNIQUE (campaign_id, revision)
);

CREATE INDEX IF NOT EXISTS idx_campaign_rules_campaign ON campaign_rules(campaign_id);
CREATE INDEX IF NOT EXISTS idx_campaign_rule_revisions_campaign ON campaign_rule_revisions(campaign_id, revision);
