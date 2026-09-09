-- Migration 060: Generation tasks, leases, persistent budget ledger and candidates
-- Tables: generation_job, generation_item, generation_budget_ledger, generation_call,
--         question_candidate, quality_finding, quality_finding_resolution

CREATE TABLE IF NOT EXISTS generation_job (
    id TEXT PRIMARY KEY,
    bank_id TEXT NOT NULL REFERENCES bank(id),
    generation_config_version TEXT NOT NULL,
    quality_policy_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'needs_attention', 'budget_exhausted')) DEFAULT 'pending',
    fixed_input_digest TEXT NOT NULL,
    requested_count INTEGER NOT NULL DEFAULT 0,
    published_count INTEGER NOT NULL DEFAULT 0,
    rejected_count INTEGER NOT NULL DEFAULT 0,
    pending_count INTEGER NOT NULL DEFAULT 0,
    revision INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_generation_job_bank ON generation_job (bank_id);

CREATE TABLE IF NOT EXISTS generation_item (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES generation_job(id),
    bank_id TEXT NOT NULL REFERENCES bank(id),
    sense_id TEXT NOT NULL REFERENCES sense(id),
    sense_version_id TEXT NOT NULL REFERENCES sense_version(id),
    fixed_input_json TEXT NOT NULL,
    input_digest TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'claimed', 'completed', 'needs_attention', 'budget_exhausted', 'failed')) DEFAULT 'pending',
    lease_owner TEXT,
    lease_until INTEGER,
    fencing_token INTEGER NOT NULL DEFAULT 0,
    attempt_number INTEGER NOT NULL DEFAULT 1,
    revision INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_generation_item_job_status ON generation_item (job_id, status);

CREATE TABLE IF NOT EXISTS generation_budget_ledger (
    job_id TEXT PRIMARY KEY REFERENCES generation_job(id),
    max_requests INTEGER NOT NULL,
    max_tokens INTEGER NOT NULL,
    authorized_requests INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    budget_overrun INTEGER NOT NULL DEFAULT 0 CHECK (budget_overrun IN (0, 1)),
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS generation_call (
    reservation_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES generation_job(id),
    item_id TEXT NOT NULL REFERENCES generation_item(id),
    network_attempt_id TEXT NOT NULL,
    attempt_number INTEGER NOT NULL,
    stage TEXT NOT NULL CHECK (stage IN ('generator', 'evaluator')),
    candidate_index INTEGER NOT NULL DEFAULT 0,
    evaluation_index INTEGER DEFAULT 0,
    logical_call_key TEXT NOT NULL,
    profile_version TEXT NOT NULL,
    input_sha256 TEXT NOT NULL,
    input_token_estimate INTEGER NOT NULL,
    max_output_tokens INTEGER NOT NULL,
    reserved_tokens INTEGER NOT NULL,
    owner_worker_id TEXT NOT NULL,
    fencing_token INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('reserved', 'settled', 'unknown')),
    outcome TEXT CHECK (outcome IN ('response', 'network_error', 'unknown')),
    provider_request_id TEXT,
    response_sha256 TEXT,
    input_tokens INTEGER,
    output_tokens INTEGER,
    accounted_tokens INTEGER NOT NULL,
    settlement_digest TEXT,
    settled_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (job_id, network_attempt_id)
);

CREATE INDEX IF NOT EXISTS idx_generation_call_item_key ON generation_call (item_id, logical_call_key);

CREATE TABLE IF NOT EXISTS question_candidate (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES generation_item(id),
    job_id TEXT NOT NULL REFERENCES generation_job(id),
    attempt_number INTEGER NOT NULL,
    candidate_index INTEGER NOT NULL,
    parent_candidate_id TEXT REFERENCES question_candidate(id),
    content_json TEXT NOT NULL,
    generation_metadata_json TEXT NOT NULL,
    checks_json TEXT NOT NULL,
    disposition TEXT NOT NULL CHECK (disposition IN ('published', 'auto_passed', 'rejected', 'uncertain', 'pending_maintenance')),
    published_version_id TEXT REFERENCES question_version(id),
    revision INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (item_id, attempt_number, candidate_index)
);

CREATE INDEX IF NOT EXISTS idx_question_candidate_job ON question_candidate (job_id);

CREATE TABLE IF NOT EXISTS quality_finding (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES generation_job(id),
    candidate_id TEXT NOT NULL REFERENCES question_candidate(id),
    finding_type TEXT NOT NULL,
    details_json TEXT NOT NULL,
    checker_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('open', 'resolved', 'ignored')) DEFAULT 'open',
    resolution_id TEXT,
    revision INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_quality_finding_job_status ON quality_finding (job_id, status);

CREATE TABLE IF NOT EXISTS quality_finding_resolution (
    id TEXT PRIMARY KEY,
    finding_id TEXT NOT NULL REFERENCES quality_finding(id),
    action TEXT NOT NULL CHECK (action IN ('revise', 'exclude', 'regenerate', 'restore_validity')),
    reason TEXT NOT NULL,
    content_json TEXT,
    new_candidate_id TEXT REFERENCES question_candidate(id),
    created_at INTEGER NOT NULL
);
