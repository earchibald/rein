ALTER TABLE settings ADD COLUMN scope_layer TEXT NOT NULL DEFAULT 'daemon-global' CHECK (scope_layer IN ('daemon-global', 'project', 'workflow', 'execution'));
ALTER TABLE settings ADD COLUMN scope_id TEXT NOT NULL DEFAULT 'daemon-global';

UPDATE settings
SET
    scope_layer = CASE
        WHEN id = 'daemon-global:daemon-global' THEN 'daemon-global'
        WHEN id LIKE 'project:%' AND length(id) > length('project:') THEN 'project'
        WHEN id LIKE 'workflow:%' AND length(id) > length('workflow:') THEN 'workflow'
        WHEN id LIKE 'execution:%' AND length(id) > length('execution:') THEN 'execution'
        ELSE scope_layer
    END,
    scope_id = CASE
        WHEN id = 'daemon-global:daemon-global' THEN 'daemon-global'
        WHEN id LIKE 'project:%' AND length(id) > length('project:') THEN substr(id, length('project:') + 1)
        WHEN id LIKE 'workflow:%' AND length(id) > length('workflow:') THEN substr(id, length('workflow:') + 1)
        WHEN id LIKE 'execution:%' AND length(id) > length('execution:') THEN substr(id, length('execution:') + 1)
        ELSE scope_id
    END;

UPDATE settings
SET id = scope_layer || ':' || scope_id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_settings_scope ON settings(scope_layer, scope_id);
