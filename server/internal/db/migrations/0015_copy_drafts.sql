-- HUI-1668 FEAT-0169: campaign copy drafts and selected versions.
-- Additive only. A draft is not published copy. Selecting a draft inserts a
-- version row and does not update campaigns, links, rewards, or billing.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS copy_drafts (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    campaign_id     TEXT NOT NULL REFERENCES campaigns(id),
    idempotency_key TEXT NOT NULL,
    snapshot_hash   TEXT NOT NULL,
    payload         TEXT NOT NULL,
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_copy_drafts_tenant_created ON copy_drafts(tenant_id, created_at);

CREATE TABLE IF NOT EXISTS copy_versions (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    campaign_id TEXT NOT NULL REFERENCES campaigns(id),
    draft_id    TEXT NOT NULL REFERENCES copy_drafts(id),
    version     INTEGER NOT NULL CHECK (version >= 1),
    payload     TEXT NOT NULL,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    UNIQUE (campaign_id, version),
    UNIQUE (campaign_id, draft_id)
);

CREATE INDEX IF NOT EXISTS idx_copy_versions_campaign ON copy_versions(campaign_id, version);
