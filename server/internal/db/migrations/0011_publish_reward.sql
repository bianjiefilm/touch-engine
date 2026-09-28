-- HUI-1671 FEAT-0172: 发布奖励只消费已确认的发布事实。
-- 本部署四个渠道都无法证明发布成功，所以：
--   grant_enabled / issuance_enabled 不能写成 1；
--   账本不能记入订单服务款、工具余额或未结算款，也不能记下已发券或已扣费；
--   人工证明的 platform_confirmed 不能写成 1，post_id 必须为空。
-- 领取、核销、撤销、超限分 op 记账，并用活动/主体/规则版本/事实 ID 去重。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS publish_reward_rules (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    campaign_id       TEXT NOT NULL REFERENCES campaigns(id),
    version           INTEGER NOT NULL,
    trigger           TEXT NOT NULL,
    evidence_level    TEXT NOT NULL,
    status            TEXT NOT NULL CHECK (status IN ('pending_verification', 'disabled')),
    grant_enabled     INTEGER NOT NULL DEFAULT 0 CHECK (grant_enabled = 0),
    issuance_enabled  INTEGER NOT NULL DEFAULT 0 CHECK (issuance_enabled = 0),
    user_notice       TEXT NOT NULL DEFAULT '',
    reason            TEXT NOT NULL DEFAULT '',
    created_by        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    UNIQUE (campaign_id, version)
);

CREATE TABLE IF NOT EXISTS publish_reward_subjects (
    fact_id     TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    campaign_id TEXT NOT NULL,
    subject_id  TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS publish_reward_ledger (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    campaign_id      TEXT NOT NULL,
    subject_id       TEXT NOT NULL,
    rule_version     INTEGER NOT NULL,
    fact_id          TEXT NOT NULL,
    op               TEXT NOT NULL CHECK (op IN ('claim', 'redeem', 'revoke', 'over_limit')),
    account_class    TEXT NOT NULL CHECK (account_class = 'marketing_reward'),
    deduct_from      TEXT NOT NULL DEFAULT '' CHECK (deduct_from = ''),
    outcome          TEXT NOT NULL,
    reason           TEXT NOT NULL DEFAULT '',
    eligible         INTEGER NOT NULL DEFAULT 0 CHECK (eligible IN (0, 1)),
    granted          INTEGER NOT NULL DEFAULT 0 CHECK (granted = 0),
    coupons_issued   INTEGER NOT NULL DEFAULT 0 CHECK (coupons_issued = 0),
    fees_charged     INTEGER NOT NULL DEFAULT 0 CHECK (fees_charged = 0),
    outbound_calls   INTEGER NOT NULL DEFAULT 0 CHECK (outbound_calls = 0),
    platform_post_id TEXT NOT NULL DEFAULT '' CHECK (platform_post_id = ''),
    created_at       TEXT NOT NULL,
    UNIQUE (campaign_id, subject_id, rule_version, fact_id, op)
);

CREATE TABLE IF NOT EXISTS publish_reward_proofs (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    campaign_id         TEXT NOT NULL,
    fact_id             TEXT NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('pending_review', 'reviewed_not_confirmed')),
    platform_confirmed  INTEGER NOT NULL DEFAULT 0 CHECK (platform_confirmed = 0),
    granted             INTEGER NOT NULL DEFAULT 0 CHECK (granted = 0),
    post_id             TEXT NOT NULL DEFAULT '' CHECK (post_id = ''),
    note                TEXT NOT NULL DEFAULT '',
    review_actor        TEXT NOT NULL DEFAULT '',
    review_basis        TEXT NOT NULL DEFAULT '',
    audit_decision      TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL,
    reviewed_at         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_publish_reward_rules_campaign
    ON publish_reward_rules(tenant_id, campaign_id, version);
CREATE INDEX IF NOT EXISTS idx_publish_reward_ledger_key
    ON publish_reward_ledger(campaign_id, subject_id, rule_version, fact_id, op);
