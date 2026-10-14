-- Control database for the hosted mode. Separate file from every household database.
-- Times are RFC 3339 UTC text (seconds). Ids are UUID text except devices (int64, contract D1).

CREATE TABLE accounts (
    id                     TEXT PRIMARY KEY,
    apple_sub              TEXT NOT NULL UNIQUE,
    display_name           TEXT,
    email                  TEXT,
    email_is_private       INTEGER NOT NULL DEFAULT 0,
    email_deliverable      INTEGER NOT NULL DEFAULT 1,
    app_account_token      TEXT NOT NULL UNIQUE,
    support_code           TEXT NOT NULL UNIQUE,
    status                 TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','locked','deleted')),
    apple_refresh_token_enc BLOB,
    created_at             TEXT NOT NULL,
    last_login_at          TEXT,
    deletion_requested_at  TEXT,
    deleted_at             TEXT
);

CREATE TABLE households (
    id                     TEXT PRIMARY KEY,
    owner_account_id       TEXT NOT NULL REFERENCES accounts(id),
    name                   TEXT NOT NULL DEFAULT 'Home',
    status                 TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','tombstoned','deleted')),
    created_at             TEXT NOT NULL,
    tombstoned_at          TEXT,
    deleted_at             TEXT,
    deletion_requested_at  TEXT,
    forced_lapse_at        TEXT,
    plan_state_cache       TEXT NOT NULL DEFAULT '',
    read_only_since        TEXT,
    delete_after           TEXT,
    limits_override        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX households_owner ON households(owner_account_id);

CREATE TABLE memberships (
    household_id TEXT NOT NULL REFERENCES households(id),
    account_id   TEXT NOT NULL UNIQUE REFERENCES accounts(id),
    role         TEXT NOT NULL CHECK (role IN ('owner','member')),
    joined_at    TEXT NOT NULL,
    PRIMARY KEY (household_id, account_id)
);
-- exactly one owner per household
CREATE UNIQUE INDEX memberships_one_owner ON memberships(household_id) WHERE role = 'owner';

CREATE TABLE invites (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id),
    code_hash    TEXT NOT NULL UNIQUE,
    created_by   TEXT NOT NULL REFERENCES accounts(id),
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    used_by      TEXT REFERENCES accounts(id),
    used_at      TEXT,
    revoked_at   TEXT
);
CREATE INDEX invites_household ON invites(household_id);

CREATE TABLE devices (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id   TEXT NOT NULL REFERENCES accounts(id),
    token_hash   TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL DEFAULT 'device',
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);
CREATE INDEX devices_account ON devices(account_id);

CREATE TABLE grants (
    id              TEXT PRIMARY KEY,
    account_id      TEXT NOT NULL REFERENCES accounts(id),
    key             TEXT NOT NULL DEFAULT 'hosted',
    expires_at      TEXT NOT NULL,
    reason          TEXT NOT NULL,
    created_by      TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    revoked_at      TEXT,
    revoked_by      TEXT,
    revoked_reason  TEXT,
    idempotency_key TEXT UNIQUE
);
CREATE INDEX grants_account ON grants(account_id);

-- One row per Apple subscription (source apple) or per grant (source grant).
CREATE TABLE entitlements (
    id                      TEXT PRIMARY KEY,
    account_id              TEXT REFERENCES accounts(id),   -- NULL = unclaimed Apple purchase
    key                     TEXT NOT NULL DEFAULT 'hosted',
    source                  TEXT NOT NULL CHECK (source IN ('apple','grant')),
    state                   TEXT NOT NULL CHECK (state IN ('active','grace','billing_retry','expired','revoked')),
    is_trial                INTEGER NOT NULL DEFAULT 0,
    starts_at               TEXT,
    expires_at              TEXT,
    grace_until             TEXT,
    auto_renew              INTEGER,
    product_id              TEXT NOT NULL DEFAULT '',
    environment             TEXT NOT NULL DEFAULT '',
    original_transaction_id TEXT UNIQUE,
    latest_transaction_id   TEXT NOT NULL DEFAULT '',
    offer_type              INTEGER NOT NULL DEFAULT 0,
    offer_id                TEXT NOT NULL DEFAULT '',
    grant_id                TEXT UNIQUE REFERENCES grants(id),
    revoked_at              TEXT,
    last_signed_ms          INTEGER NOT NULL DEFAULT 0,
    updated_at              TEXT NOT NULL
);
CREATE INDEX entitlements_account ON entitlements(account_id);

CREATE TABLE usage_counters (
    household_id TEXT NOT NULL,
    period       TEXT NOT NULL,   -- 2026-10, 2026-10-14 or 'gauge'
    metric       TEXT NOT NULL,
    value        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (household_id, period, metric)
);

CREATE TABLE audit_log (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    at      TEXT NOT NULL,
    actor   TEXT NOT NULL,
    action  TEXT NOT NULL,
    subject TEXT NOT NULL,
    details TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_subject ON audit_log(subject);
CREATE TRIGGER audit_no_update BEFORE UPDATE ON audit_log BEGIN SELECT RAISE(ABORT, 'audit_log is append-only'); END;
CREATE TRIGGER audit_no_delete BEFORE DELETE ON audit_log BEGIN SELECT RAISE(ABORT, 'audit_log is append-only'); END;

-- Idempotency for App Store notifications and Sign in with Apple events.
CREATE TABLE processed_notifications (
    notification_uuid       TEXT PRIMARY KEY,
    type                    TEXT NOT NULL,
    subtype                 TEXT NOT NULL DEFAULT '',
    original_transaction_id TEXT NOT NULL DEFAULT '',
    signed_date_ms          INTEGER NOT NULL DEFAULT 0,
    received_at             TEXT NOT NULL,
    result                  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE siwa_nonces (
    nonce_hash TEXT PRIMARY KEY,
    expires_at TEXT NOT NULL,
    used_at    TEXT
);

-- "Reminder due" records and other lifecycle markers; unique per (household, kind, anchor).
CREATE TABLE lifecycle_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id TEXT NOT NULL,
    kind         TEXT NOT NULL,
    anchor       TEXT NOT NULL,
    due_at       TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    UNIQUE (household_id, kind, anchor)
);

-- Apple refresh tokens still to be revoked (retried by the lifecycle job).
CREATE TABLE pending_apple_revocations (
    account_id    TEXT PRIMARY KEY,
    token_enc     BLOB NOT NULL,
    created_at    TEXT NOT NULL,
    attempts      INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT NOT NULL DEFAULT '',
    done_at       TEXT
);
