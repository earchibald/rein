CREATE TABLE IF NOT EXISTS costeventlog (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    event_name TEXT NOT NULL,
    event_time TEXT NOT NULL,
    project_id TEXT NOT NULL,
    issue_id TEXT,
    execution_id TEXT,
    workflow_id TEXT,
    adapter_id TEXT,
    currency TEXT NOT NULL,
    cost_micros INTEGER NOT NULL CHECK (cost_micros >= 0),
    input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_write_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    resource_attributes TEXT NOT NULL CHECK (json_valid(resource_attributes)),
    attributes TEXT NOT NULL CHECK (json_valid(attributes)),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_costeventlog_project_time ON costeventlog(project_id, event_time, sequence);
CREATE INDEX IF NOT EXISTS idx_costeventlog_issue_time ON costeventlog(issue_id, event_time, sequence);
CREATE INDEX IF NOT EXISTS idx_costeventlog_execution_time ON costeventlog(execution_id, event_time, sequence);

CREATE TABLE IF NOT EXISTS budgetstates (
    scope TEXT NOT NULL CHECK (scope IN ('project', 'issue', 'execution')),
    scope_id TEXT NOT NULL,
    currency TEXT NOT NULL,
    soft_limit_micros INTEGER NOT NULL DEFAULT 0 CHECK (soft_limit_micros >= 0),
    hard_limit_micros INTEGER NOT NULL DEFAULT 0 CHECK (hard_limit_micros >= 0),
    spent_micros INTEGER NOT NULL DEFAULT 0 CHECK (spent_micros >= 0),
    input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_write_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    event_count INTEGER NOT NULL DEFAULT 0 CHECK (event_count >= 0),
    last_event_id TEXT NOT NULL DEFAULT '',
    last_event_time TEXT NOT NULL DEFAULT '',
    soft_limit_hit_time TEXT NOT NULL DEFAULT '',
    hard_limit_hit_time TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (scope, scope_id),
    CHECK (hard_limit_micros = 0 OR soft_limit_micros = 0 OR soft_limit_micros <= hard_limit_micros)
);

CREATE INDEX IF NOT EXISTS idx_budgetstates_updated_at ON budgetstates(updated_at);
CREATE INDEX IF NOT EXISTS idx_budgetstates_hard_limit_hit_time ON budgetstates(hard_limit_hit_time);
