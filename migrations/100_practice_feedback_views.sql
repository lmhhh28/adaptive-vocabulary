-- Migration 100: Practice feedback views audit
-- Tables: practice_feedback_view

CREATE TABLE IF NOT EXISTS practice_feedback_view (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES session(id),
    instance_id TEXT NOT NULL REFERENCES question_instance(id),
    client_event_id TEXT NOT NULL UNIQUE,
    presentation TEXT NOT NULL CHECK (presentation IN ('history', 'restored_feedback')),
    question_version_id TEXT NOT NULL REFERENCES question_version(id),
    sense_id TEXT NOT NULL REFERENCES sense(id),
    lexeme_id TEXT NOT NULL REFERENCES lexeme(id),
    form_id TEXT NOT NULL REFERENCES question_form(id),
    client_displayed_at TEXT,
    recorded_at TEXT NOT NULL,
    event_sequence INTEGER NOT NULL REFERENCES global_sequence(seq),
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_practice_feedback_view_session
ON practice_feedback_view(session_id, instance_id);
