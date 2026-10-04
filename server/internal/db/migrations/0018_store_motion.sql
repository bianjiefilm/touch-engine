-- HUI-2748: one recorded parameter request for a store activity.
-- This is not a render, not a model call, and not a Motion revision.
-- revision_id stays NULL. model_calls and render_calls stay 0.
-- unchanged_store_rerun stays not_claimed because HUI-2732 is not done.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_motion_requests (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id),
    store_id              TEXT NOT NULL REFERENCES stores(id),
    campaign_id           TEXT NOT NULL REFERENCES campaigns(id),
    version               INTEGER NOT NULL CHECK (version >= 1),
    store_name            TEXT NOT NULL,
    activity_time         TEXT NOT NULL,
    price                 TEXT NOT NULL,
    address               TEXT NOT NULL,
    offer_copy            TEXT NOT NULL,
    cta                   TEXT NOT NULL,
    origin_app            TEXT NOT NULL CHECK (origin_app = 'touch'),
    origin_context_ref    TEXT NOT NULL,
    revision_id           TEXT CHECK (revision_id IS NULL),
    verified              INTEGER NOT NULL DEFAULT 0 CHECK (verified = 0),
    status                TEXT NOT NULL CHECK (status = '还没生成成片'),
    model_calls           INTEGER NOT NULL DEFAULT 0 CHECK (model_calls = 0),
    render_calls          INTEGER NOT NULL DEFAULT 0 CHECK (render_calls = 0),
    unchanged_store_rerun TEXT NOT NULL CHECK (unchanged_store_rerun = 'not_claimed'),
    unchanged_store_note  TEXT NOT NULL,
    created_by            TEXT NOT NULL DEFAULT '',
    created_at            TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id, version)
);

CREATE INDEX IF NOT EXISTS idx_store_motion_campaign
    ON store_motion_requests(tenant_id, campaign_id, version);
