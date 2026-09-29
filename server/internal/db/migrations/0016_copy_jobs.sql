-- HUI-1668 close: quote → confirm → generate jobs.
-- Additive. A job is not a published campaign and not a GoBoost project.
-- charge_count is the number of accepted provider submits, capped at one.

CREATE TABLE IF NOT EXISTS copy_jobs (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    campaign_id     TEXT NOT NULL REFERENCES campaigns(id),
    idempotency_key TEXT NOT NULL,
    snapshot_hash   TEXT NOT NULL,
    state           TEXT NOT NULL,
    charge_count    INTEGER NOT NULL DEFAULT 0 CHECK (charge_count >= 0 AND charge_count <= 1),
    payload         TEXT NOT NULL,
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_copy_jobs_campaign ON copy_jobs(campaign_id, created_at);

-- Explicit save of a usable draft. Title and intro still live on campaigns
-- only when the merchant has not typed over them. Topics have no campaign column.
CREATE TABLE IF NOT EXISTS campaign_saved_copy (
    campaign_id    TEXT PRIMARY KEY REFERENCES campaigns(id),
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    title_written  INTEGER NOT NULL DEFAULT 0 CHECK (title_written IN (0, 1)),
    intro_written  INTEGER NOT NULL DEFAULT 0 CHECK (intro_written IN (0, 1)),
    topics         TEXT NOT NULL DEFAULT '[]',
    source_job_id  TEXT NOT NULL,
    model_id       TEXT NOT NULL DEFAULT '',
    model_version  TEXT NOT NULL DEFAULT '',
    source_hash    TEXT NOT NULL DEFAULT '',
    saved_at       TEXT NOT NULL
);
