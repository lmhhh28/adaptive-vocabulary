-- Migration 040: Exam session state, instance drafts and assessment results
-- Tables: exam_session, exam_instance_draft, assessment_result, assessment_item_result

CREATE TABLE IF NOT EXISTS exam_session (
    session_id TEXT PRIMARY KEY REFERENCES session(id),
    bank_id TEXT NOT NULL REFERENCES bank(id),
    question_count INTEGER NOT NULL,
    time_limit_seconds INTEGER,
    deadline_at INTEGER,
    task_quotas_json TEXT NOT NULL,
    tags_json TEXT,
    telemetry_incomplete INTEGER NOT NULL DEFAULT 0,
    assembly_seed TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_exam_session_bank ON exam_session(bank_id);

CREATE TABLE IF NOT EXISTS exam_instance_draft (
    session_id TEXT NOT NULL REFERENCES session(id),
    instance_id TEXT NOT NULL REFERENCES question_instance(id),
    selected_option_id TEXT,
    first_selected_option_id TEXT,
    first_response_event_id TEXT,
    final_response_event_id TEXT,
    revision_count INTEGER NOT NULL DEFAULT 0,
    is_first_reliable INTEGER NOT NULL DEFAULT 1,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (session_id, instance_id)
);

CREATE TABLE IF NOT EXISTS assessment_result (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL UNIQUE REFERENCES session(id),
    total_count INTEGER NOT NULL,
    correct_count INTEGER NOT NULL,
    omitted_count INTEGER NOT NULL,
    void_count INTEGER NOT NULL DEFAULT 0,
    ratio REAL,
    result_revision INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS assessment_item_result (
    id TEXT PRIMARY KEY,
    assessment_id TEXT NOT NULL REFERENCES assessment_result(id),
    instance_id TEXT NOT NULL REFERENCES question_instance(id),
    sense_id TEXT NOT NULL REFERENCES sense(id),
    task_family TEXT NOT NULL,
    selected_option_id TEXT,
    correct_option_id TEXT NOT NULL,
    is_correct INTEGER NOT NULL,
    response_history TEXT NOT NULL,
    first_correct INTEGER,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_assessment_item_instance ON assessment_item_result(instance_id);
