CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    lock_version INTEGER NOT NULL CHECK (lock_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_projects_updated_at ON projects(updated_at);

CREATE TABLE IF NOT EXISTS workflows (
    id TEXT PRIMARY KEY,
    lock_version INTEGER NOT NULL CHECK (lock_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_workflows_updated_at ON workflows(updated_at);

CREATE TABLE IF NOT EXISTS issues (
    id TEXT PRIMARY KEY,
    lock_version INTEGER NOT NULL CHECK (lock_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_issues_updated_at ON issues(updated_at);

CREATE TABLE IF NOT EXISTS executions (
    id TEXT PRIMARY KEY,
    lock_version INTEGER NOT NULL CHECK (lock_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_executions_updated_at ON executions(updated_at);

CREATE TABLE IF NOT EXISTS tasksteps (
    id TEXT PRIMARY KEY,
    lock_version INTEGER NOT NULL CHECK (lock_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tasksteps_updated_at ON tasksteps(updated_at);

CREATE TABLE IF NOT EXISTS sideeffects (
    id TEXT PRIMARY KEY,
    lock_version INTEGER NOT NULL CHECK (lock_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sideeffects_updated_at ON sideeffects(updated_at);

CREATE TABLE IF NOT EXISTS costevents (
    id TEXT PRIMARY KEY,
    lock_version INTEGER NOT NULL CHECK (lock_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_costevents_updated_at ON costevents(updated_at);

CREATE TABLE IF NOT EXISTS settings (
    id TEXT PRIMARY KEY,
    lock_version INTEGER NOT NULL CHECK (lock_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_settings_updated_at ON settings(updated_at);

CREATE TABLE IF NOT EXISTS featureflags (
    id TEXT PRIMARY KEY,
    lock_version INTEGER NOT NULL CHECK (lock_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_featureflags_updated_at ON featureflags(updated_at);
