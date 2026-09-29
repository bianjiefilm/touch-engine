-- HUI-1672: expiry and revocation for configured jumps, plus one authorized return.
-- A return is not an arbitrary external URL. Clicks stay unknown.

ALTER TABLE extra_jump_actions ADD COLUMN revoked INTEGER NOT NULL DEFAULT 0 CHECK (revoked IN (0, 1));
ALTER TABLE extra_jump_actions ADD COLUMN expires_at TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS authorized_returns (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    campaign_id TEXT NOT NULL UNIQUE REFERENCES campaigns(id),
    href        TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    revoked     INTEGER NOT NULL CHECK (revoked IN (0, 1)),
    expires_at  TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS authorized_return_clicks (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    campaign_id     TEXT NOT NULL,
    recorded_as     TEXT NOT NULL CHECK (recorded_as = 'click'),
    success         INTEGER NOT NULL CHECK (success = 0),
    platform_result TEXT NOT NULL CHECK (platform_result = 'unknown'),
    created_at      TEXT NOT NULL
);
