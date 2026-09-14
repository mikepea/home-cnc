-- home-cnc schema. Designed to be portable to PostgreSQL later (issue #3):
-- integer unix-second timestamps, no SQLite-only column types.

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS devices (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    os         TEXT    NOT NULL,          -- linux | darwin
    token_hash TEXT    NOT NULL UNIQUE,   -- sha256(hex) of the bearer token
    created_at INTEGER NOT NULL,
    last_seen  INTEGER,
    last_ip    TEXT
);

CREATE TABLE IF NOT EXISTS commands (
    id         TEXT    PRIMARY KEY,
    device_id  TEXT    NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    type       TEXT    NOT NULL,          -- halt | lock
    status     TEXT    NOT NULL,          -- pending | acked | failed | expired
    signature  TEXT    NOT NULL,          -- base64 ed25519 over canonical payload
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    acked_at   INTEGER,
    result     TEXT
);

CREATE INDEX IF NOT EXISTS idx_commands_device_status ON commands(device_id, status);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
