-- Migration 030: Practice session state
-- Tables: practice_session

CREATE TABLE IF NOT EXISTS practice_session (
    session_id TEXT PRIMARY KEY REFERENCES session(id),
    bank_id TEXT NOT NULL REFERENCES bank(id),
    unresolved_unit_count INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_practice_session_bank ON practice_session(bank_id);
