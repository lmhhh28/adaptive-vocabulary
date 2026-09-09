-- Migration 020: Learning, observation, FSRS memory and selection state
-- Tables: attempt_event, response_event, exposure_event, review_event, event_revision,
--         evidence_projection, memory_projection, confusion_projection, correction_item

CREATE TABLE IF NOT EXISTS attempt_event (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL UNIQUE REFERENCES question_instance(id),
    session_id TEXT NOT NULL REFERENCES session(id),
    sense_id TEXT NOT NULL REFERENCES sense(id),
    task_family TEXT NOT NULL CHECK (task_family IN ('meaning_recognition', 'reverse_selection', 'context_selection')),
    source TEXT NOT NULL CHECK (source IN ('practice', 'exam')),
    purpose TEXT NOT NULL CHECK (purpose IN ('normal', 'correction', 'early_review')),
    response_history TEXT NOT NULL CHECK (response_history IN ('single_response', 'revised', 'telemetry_incomplete', 'omitted')),
    selected_option_id TEXT,
    final_option_id TEXT,
    first_response_event_id TEXT,
    final_response_event_id TEXT,
    revision_count INTEGER NOT NULL DEFAULT 0,
    client_active_duration_ms INTEGER,
    interruption_flags TEXT,
    grading_version TEXT NOT NULL,
    is_correct INTEGER NOT NULL CHECK (is_correct IN (0, 1)),
    observed_at INTEGER NOT NULL,
    event_sequence INTEGER NOT NULL REFERENCES global_sequence(seq),
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS response_event (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES session(id),
    client_event_id TEXT NOT NULL,
    client_sequence INTEGER NOT NULL,
    instance_id TEXT NOT NULL REFERENCES question_instance(id),
    action TEXT NOT NULL CHECK (action IN ('select', 'clear', 'noop')),
    option_id TEXT,
    expected_revision INTEGER NOT NULL,
    accepted_revision INTEGER NOT NULL,
    rebased_from TEXT,
    observed_at INTEGER NOT NULL,
    event_sequence INTEGER NOT NULL REFERENCES global_sequence(seq),
    created_at INTEGER NOT NULL,
    UNIQUE (session_id, client_event_id),
    UNIQUE (session_id, client_sequence)
);

CREATE TABLE IF NOT EXISTS exposure_event (
    id TEXT PRIMARY KEY,
    source_instance_id TEXT NOT NULL REFERENCES question_instance(id),
    kind TEXT NOT NULL CHECK (kind IN ('reserved', 'feedback_available', 'reported_display', 'same_form_reserved', 'recent_related_exposure')),
    sense_id TEXT NOT NULL REFERENCES sense(id),
    lexeme_id TEXT NOT NULL REFERENCES lexeme(id),
    form_id TEXT NOT NULL REFERENCES question_form(id),
    observed_at INTEGER NOT NULL,
    event_sequence INTEGER NOT NULL REFERENCES global_sequence(seq),
    client_displayed_at INTEGER,
    display_event_id TEXT,
    created_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_exposure_instance_kind
ON exposure_event (source_instance_id, kind)
WHERE kind IN ('reserved', 'feedback_available');

CREATE INDEX IF NOT EXISTS idx_exposure_lookup
ON exposure_event (lexeme_id, form_id, observed_at, event_sequence);

CREATE TABLE IF NOT EXISTS review_event (
    id TEXT PRIMARY KEY,
    attempt_id TEXT NOT NULL REFERENCES attempt_event(id),
    sense_id TEXT NOT NULL REFERENCES sense(id),
    task_family TEXT NOT NULL CHECK (task_family IN ('meaning_recognition', 'reverse_selection', 'context_selection')),
    observation_policy_version TEXT NOT NULL,
    rating INTEGER NOT NULL CHECK (rating IN (1, 2, 3, 4)),
    review_time INTEGER NOT NULL,
    fsrs_state INTEGER NOT NULL,
    fsrs_step INTEGER NOT NULL DEFAULT 0,
    stability REAL NOT NULL,
    difficulty REAL NOT NULL,
    due_at INTEGER NOT NULL,
    reps INTEGER NOT NULL DEFAULT 0,
    lapses INTEGER NOT NULL DEFAULT 0,
    elapsed_days REAL NOT NULL DEFAULT 0,
    scheduled_days REAL NOT NULL DEFAULT 0,
    fsrs_version TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (attempt_id, observation_policy_version)
);

CREATE TABLE IF NOT EXISTS event_revision (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('attempt_event', 'question_instance', 'response_event')),
    revision_kind TEXT NOT NULL CHECK (revision_kind IN ('disputed', 'invalidate', 'restore_validity')),
    reason TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS evidence_projection (
    projection_version TEXT NOT NULL,
    sense_id TEXT NOT NULL REFERENCES sense(id),
    task_family TEXT NOT NULL CHECK (task_family IN ('meaning_recognition', 'reverse_selection', 'context_selection')),
    ordinary_samples INTEGER NOT NULL DEFAULT 0,
    ordinary_correct INTEGER NOT NULL DEFAULT 0,
    normal_samples INTEGER NOT NULL DEFAULT 0,
    normal_correct INTEGER NOT NULL DEFAULT 0,
    early_review_samples INTEGER NOT NULL DEFAULT 0,
    early_review_correct INTEGER NOT NULL DEFAULT 0,
    exam_single_samples INTEGER NOT NULL DEFAULT 0,
    exam_single_correct INTEGER NOT NULL DEFAULT 0,
    correction_samples INTEGER NOT NULL DEFAULT 0,
    correction_correct INTEGER NOT NULL DEFAULT 0,
    exam_revised_samples INTEGER NOT NULL DEFAULT 0,
    exam_telemetry_incomplete_samples INTEGER NOT NULL DEFAULT 0,
    omitted_samples INTEGER NOT NULL DEFAULT 0,
    last_ordinary_observed_at INTEGER,
    last_ordinary_correct INTEGER CHECK (last_ordinary_correct IS NULL OR last_ordinary_correct IN (0, 1)),
    earliest_ordinary_observed_at INTEGER,
    needs_consolidation INTEGER NOT NULL DEFAULT 0 CHECK (needs_consolidation IN (0, 1)),
    applied_event_sequence INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (projection_version, sense_id, task_family)
);

CREATE TABLE IF NOT EXISTS memory_projection (
    projection_version TEXT NOT NULL,
    sense_id TEXT NOT NULL REFERENCES sense(id),
    task_family TEXT NOT NULL CHECK (task_family IN ('meaning_recognition', 'reverse_selection', 'context_selection')),
    fsrs_state INTEGER NOT NULL DEFAULT 0,
    fsrs_step INTEGER NOT NULL DEFAULT 0,
    stability REAL NOT NULL DEFAULT 0,
    difficulty REAL NOT NULL DEFAULT 0,
    due_at INTEGER NOT NULL DEFAULT 0,
    last_review_at INTEGER,
    reps INTEGER NOT NULL DEFAULT 0,
    lapses INTEGER NOT NULL DEFAULT 0,
    elapsed_days REAL NOT NULL DEFAULT 0,
    scheduled_days REAL NOT NULL DEFAULT 0,
    applied_event_sequence INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (projection_version, sense_id, task_family)
);

CREATE TABLE IF NOT EXISTS confusion_projection (
    projection_version TEXT NOT NULL,
    target_sense_id TEXT NOT NULL REFERENCES sense(id),
    task_family TEXT NOT NULL CHECK (task_family IN ('meaning_recognition', 'reverse_selection', 'context_selection')),
    distractor_identity_id TEXT NOT NULL REFERENCES option_identity(id),
    stat_group TEXT NOT NULL CHECK (stat_group IN ('ordinary', 'correction', 'uncertain')),
    eligible_offered_count INTEGER NOT NULL DEFAULT 0,
    wrong_selected_count INTEGER NOT NULL DEFAULT 0,
    last_offered_at INTEGER,
    last_selected_at INTEGER,
    applied_event_sequence INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (projection_version, target_sense_id, task_family, distractor_identity_id, stat_group)
);

CREATE TABLE IF NOT EXISTS correction_item (
    session_id TEXT NOT NULL REFERENCES session(id),
    sense_id TEXT NOT NULL REFERENCES sense(id),
    task_family TEXT NOT NULL CHECK (task_family IN ('meaning_recognition', 'reverse_selection', 'context_selection')),
    triggering_attempt_id TEXT NOT NULL REFERENCES attempt_event(id),
    triggering_event_sequence INTEGER NOT NULL,
    retries_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK (status IN ('pending', 'completed', 'exhausted', 'abandoned')) DEFAULT 'pending',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (session_id, sense_id, task_family)
);
