DROP INDEX IF EXISTS idx_settings_scope;
DROP INDEX IF EXISTS idx_settings_updated_at;

ALTER TABLE settings RENAME TO settings_old;

CREATE TABLE IF NOT EXISTS settings (
    id TEXT PRIMARY KEY,
    lock_version INTEGER NOT NULL CHECK (lock_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO settings (id, lock_version, payload, created_at, updated_at)
SELECT id, lock_version, payload, created_at, updated_at
FROM settings_old;

CREATE INDEX IF NOT EXISTS idx_settings_updated_at ON settings(updated_at);

DROP TABLE settings_old;
