-- Migration 000: Runtime foundation
-- Tables: schema_migrations, session, idempotency_record, projection_control, global_sequence

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS session (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('practice', 'exam')),
    status TEXT NOT NULL CHECK (status IN ('open', 'ended', 'submitted', 'abandoned')),
    bank_id TEXT,
    cursor INTEGER NOT NULL DEFAULT 0,
    normal_position INTEGER,
    revision INTEGER NOT NULL DEFAULT 0,
    last_ack_sequence INTEGER NOT NULL DEFAULT 0,
    deadline_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Unique partial index enforcing at most one open session across all modes
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_active ON session ( (1) ) WHERE status = 'open';

CREATE TABLE IF NOT EXISTS idempotency_record (
    operation TEXT NOT NULL,
    resource_scope TEXT NOT NULL,
    key TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    status TEXT NOT NULL,
    response_code INTEGER NOT NULL,
    response_body TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (operation, resource_scope, key)
);

CREATE TABLE IF NOT EXISTS projection_control (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    mode TEXT NOT NULL CHECK (mode IN ('active', 'rebuilding')) DEFAULT 'active',
    active_version TEXT NOT NULL DEFAULT 'v1',
    run_id TEXT,
    run_status TEXT,
    requires_rebuild INTEGER NOT NULL DEFAULT 0,
    fixed_watermark INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);

INSERT OR IGNORE INTO projection_control (id, mode, active_version, requires_rebuild, fixed_watermark, updated_at)
VALUES (1, 'active', 'v1', 0, 0, unixepoch() * 1000000);

CREATE TABLE IF NOT EXISTS global_sequence (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    event_name TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
