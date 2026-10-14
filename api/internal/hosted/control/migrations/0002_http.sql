-- HTTP layer tables: idempotency keys (24 h replay), export jobs and a small lock table for the scheduler.

CREATE TABLE idempotency_keys (
    scope        TEXT NOT NULL,            -- account id (or 'admin')
    key          TEXT NOT NULL,
    method       TEXT NOT NULL,
    path         TEXT NOT NULL,
    body_hash    TEXT NOT NULL,
    status       INTEGER NOT NULL DEFAULT 0,
    content_type TEXT NOT NULL DEFAULT '',
    location     TEXT NOT NULL DEFAULT '',
    response     BLOB,
    created_at   TEXT NOT NULL,
    completed_at TEXT,
    PRIMARY KEY (scope, key)
);
CREATE INDEX idempotency_created ON idempotency_keys(created_at);

CREATE TABLE export_jobs (
    id            TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL,
    requested_by  TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('queued','running','ready','failed','expired')),
    created_at    TEXT NOT NULL,
    started_at    TEXT,
    finished_at   TEXT,
    expires_at    TEXT,
    size_bytes    INTEGER,
    path          TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX export_jobs_household ON export_jobs(household_id, created_at);

CREATE TABLE job_locks (
    name       TEXT PRIMARY KEY,
    holder     TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
