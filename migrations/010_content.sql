-- Migration 010: Standard content, vocabulary, sources, banks and question versions
-- Tables: source, source_entry, lexeme, lexeme_alias, sense, sense_version,
--         bank, bank_sense, option_identity, question_form, question_version, question_instance

CREATE TABLE IF NOT EXISTS source (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    location TEXT NOT NULL,
    source_version TEXT NOT NULL,
    license TEXT NOT NULL,
    usage_scope TEXT NOT NULL,
    checksum TEXT,
    revision INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS source_entry (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES source(id),
    source_version TEXT NOT NULL,
    external_entry_id TEXT NOT NULL,
    raw_word TEXT NOT NULL,
    raw_translation TEXT NOT NULL,
    pos TEXT,
    tags TEXT,
    aliases TEXT,
    statistics TEXT,
    created_at INTEGER NOT NULL,
    UNIQUE (source_id, source_version, external_entry_id)
);

CREATE TABLE IF NOT EXISTS lexeme (
    id TEXT PRIMARY KEY,
    canonical_form TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS lexeme_alias (
    id TEXT PRIMARY KEY,
    form TEXT NOT NULL UNIQUE,
    lexeme_id TEXT NOT NULL REFERENCES lexeme(id),
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sense (
    id TEXT PRIMARY KEY,
    lexeme_id TEXT NOT NULL REFERENCES lexeme(id),
    part_of_speech TEXT NOT NULL,
    source_id TEXT NOT NULL REFERENCES source(id),
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sense_version (
    id TEXT PRIMARY KEY,
    sense_id TEXT NOT NULL REFERENCES sense(id),
    meaning TEXT NOT NULL,
    frequency REAL,
    version_number INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    UNIQUE (sense_id, version_number)
);

CREATE TABLE IF NOT EXISTS bank (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS bank_sense (
    bank_id TEXT NOT NULL REFERENCES bank(id),
    sense_id TEXT NOT NULL REFERENCES sense(id),
    added_at INTEGER NOT NULL,
    PRIMARY KEY (bank_id, sense_id)
);

CREATE TABLE IF NOT EXISTS option_identity (
    id TEXT PRIMARY KEY,
    canonical_key TEXT NOT NULL UNIQUE,
    language TEXT NOT NULL,
    normalized_text TEXT NOT NULL,
    sense_id TEXT REFERENCES sense(id),
    source_id TEXT REFERENCES source(id),
    source_version TEXT,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS question_form (
    id TEXT PRIMARY KEY,
    sense_id TEXT NOT NULL REFERENCES sense(id),
    task_family TEXT NOT NULL CHECK (task_family IN ('meaning_recognition', 'reverse_selection', 'context_selection')),
    dedup_hash TEXT NOT NULL UNIQUE,
    normalized_stem TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS question_version (
    id TEXT PRIMARY KEY,
    form_id TEXT NOT NULL REFERENCES question_form(id),
    sense_id TEXT NOT NULL REFERENCES sense(id),
    sense_version_id TEXT NOT NULL REFERENCES sense_version(id),
    task_family TEXT NOT NULL CHECK (task_family IN ('meaning_recognition', 'reverse_selection', 'context_selection')),
    stem TEXT NOT NULL,
    options_json TEXT NOT NULL,
    correct_option_id TEXT NOT NULL,
    explanation TEXT NOT NULL,
    candidate_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('published', 'draft', 'retired', 'disputed', 'invalidated')) DEFAULT 'published',
    revision INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS question_instance (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES session(id),
    ordinal INTEGER NOT NULL,
    normal_position INTEGER,
    question_version_id TEXT NOT NULL REFERENCES question_version(id),
    snapshot_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (session_id, ordinal)
);
