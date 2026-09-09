-- Migration 080: Evidence disposition, model rebuild runs, and assessment report revisions
-- Tables: model_rebuild_run, evidence_disposition, assessment_result_revision

CREATE TABLE IF NOT EXISTS model_rebuild_run (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('policy_only', 'evidence_revision')),
    base_active_version TEXT NOT NULL,
    target_projection_version TEXT NOT NULL,
    target_policy TEXT NOT NULL,
    base_revision_digest TEXT NOT NULL,
    fixed_watermark INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'aborted', 'aborted_maintenance')),
    requires_rebuild INTEGER NOT NULL DEFAULT 0 CHECK (requires_rebuild IN (0, 1)),
    predecessor_run_id TEXT UNIQUE REFERENCES model_rebuild_run(id),
    error_message TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_rebuild_run_status ON model_rebuild_run(status);

CREATE TABLE IF NOT EXISTS evidence_disposition (
    id TEXT PRIMARY KEY,
    question_version_id TEXT NOT NULL REFERENCES question_version(id),
    action TEXT NOT NULL CHECK (action IN ('disputed', 'invalidate', 'restore_validity')),
    reason TEXT NOT NULL,
    rebuild_run_id TEXT REFERENCES model_rebuild_run(id),
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_disposition_question ON evidence_disposition(question_version_id);

CREATE TABLE IF NOT EXISTS assessment_result_revision (
    id TEXT PRIMARY KEY,
    assessment_id TEXT NOT NULL REFERENCES assessment_result(id),
    session_id TEXT NOT NULL REFERENCES session(id),
    result_revision INTEGER NOT NULL,
    total_count INTEGER NOT NULL,
    correct_count INTEGER NOT NULL,
    omitted_count INTEGER NOT NULL,
    void_count INTEGER NOT NULL,
    ratio REAL,
    void_instances_json TEXT NOT NULL,
    reason TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(assessment_id, result_revision)
);

CREATE INDEX IF NOT EXISTS idx_assessment_rev_session ON assessment_result_revision(session_id);
