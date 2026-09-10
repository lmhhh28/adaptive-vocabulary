-- Migration 090: Add lookup indexes for event_revision
CREATE INDEX IF NOT EXISTS idx_event_revision_lookup ON event_revision(event_id, created_at DESC, id DESC);
