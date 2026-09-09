-- Migration 050: Sources, vocabulary import and exception resolution
-- Tables: import_job, import_item

CREATE TABLE IF NOT EXISTS import_job (
    id TEXT PRIMARY KEY,
    target_type TEXT NOT NULL CHECK (target_type IN ('source', 'bank')),
    target_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    source_version TEXT,
    resolution_policy_version TEXT NOT NULL DEFAULT 'v1',
    raw_input_summary TEXT,
    total_count INTEGER NOT NULL DEFAULT 0,
    resolved_count INTEGER NOT NULL DEFAULT 0,
    ambiguous_count INTEGER NOT NULL DEFAULT 0,
    not_found_count INTEGER NOT NULL DEFAULT 0,
    invalid_count INTEGER NOT NULL DEFAULT 0,
    excluded_count INTEGER NOT NULL DEFAULT 0,
    revision INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS import_item (
    id TEXT PRIMARY KEY,
    import_id TEXT NOT NULL REFERENCES import_job(id),
    bank_id TEXT REFERENCES bank(id),
    ordinal INTEGER NOT NULL,
    raw_input TEXT NOT NULL,
    normalized_word TEXT,
    specified_sense_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('resolved', 'ambiguous', 'not_found', 'invalid', 'excluded')),
    candidates_json TEXT NOT NULL DEFAULT '[]',
    resolved_sense_ids_json TEXT NOT NULL DEFAULT '[]',
    disposition_reason TEXT,
    revision INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (import_id, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_import_item_status ON import_item(import_id, status);
CREATE INDEX IF NOT EXISTS idx_import_job_target ON import_job(target_type, target_id);
