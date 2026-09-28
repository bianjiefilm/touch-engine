-- HUI-1992: marketing benefits stay on the activity ledger.
-- Face value is not platform cash. This table has no wallet balance and
-- cannot store a platform user id or a cash expense.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS activity_benefit_facts (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    campaign_id        TEXT NOT NULL REFERENCES campaigns(id),
    kind               TEXT NOT NULL CHECK (kind IN ('coupon', 'points', 'lottery', 'group_buy_voucher')),
    face_minor         INTEGER NOT NULL CHECK (face_minor >= 0),
    op                 TEXT NOT NULL CHECK (op IN ('claim', 'redeem')),
    visitor_ref        TEXT NOT NULL,
    ledger             TEXT NOT NULL CHECK (ledger = 'activity'),
    cash_expense_minor INTEGER NOT NULL DEFAULT 0 CHECK (cash_expense_minor = 0),
    platform_user_id   TEXT NOT NULL DEFAULT '' CHECK (platform_user_id = ''),
    created_at         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_activity_benefit_facts_tenant
    ON activity_benefit_facts(tenant_id, campaign_id);
