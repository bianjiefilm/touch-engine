-- HUI-1892: platform-notify assigns its own event id. Confirmation polls that
-- id, not the source submission ref. Empty means notify has not accepted yet.

ALTER TABLE leads_outbox ADD COLUMN notify_event_id TEXT NOT NULL DEFAULT '';
