-- HUI-2053 WL-T1: tenant brand binding, lifecycle, and export jobs.
-- Additive only. Brand display itself is NOT stored here — touch reads
-- public-ai Brand Registry. brand_id is a foreign reference, not a brand row.
-- published_* on a short code records the brand/host at mint time so a later
-- display rename or domain move does not delete the code.

PRAGMA foreign_keys = ON;

ALTER TABLE tenants ADD COLUMN brand_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN lifecycle TEXT NOT NULL DEFAULT 'active';
ALTER TABLE tenants ADD COLUMN charge_hold INTEGER NOT NULL DEFAULT 0;

ALTER TABLE campaign_links ADD COLUMN published_brand_id TEXT NOT NULL DEFAULT '';
ALTER TABLE campaign_links ADD COLUMN published_host TEXT NOT NULL DEFAULT '';

-- Export jobs are bound to the brand+tenant+requester that created them.
-- The manifest is tenant-scoped JSON. It never includes another tenant's rows
-- or full CRM contact fields.
CREATE TABLE IF NOT EXISTS tenant_exports (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    brand_id            TEXT NOT NULL,
    requester_principal TEXT NOT NULL,
    purpose             TEXT NOT NULL,
    expires_at          TEXT NOT NULL,
    revoked             INTEGER NOT NULL DEFAULT 0 CHECK (revoked IN (0,1)),
    manifest_json       TEXT NOT NULL,
    created_at          TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tenant_exports_tenant ON tenant_exports(tenant_id, created_at);
