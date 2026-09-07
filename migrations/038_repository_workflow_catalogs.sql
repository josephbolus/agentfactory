CREATE TABLE repository_workflow_catalogs (
    repository_id TEXT PRIMARY KEY REFERENCES repositories(id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('healthy', 'invalid', 'unavailable')),
    error TEXT NOT NULL DEFAULT '',
    synced_at INTEGER NOT NULL DEFAULT 0,
    total_bytes INTEGER NOT NULL DEFAULT 0 CHECK (total_bytes >= 0)
);

CREATE TABLE repository_workflow_entries (
    repository_id TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    workflow_id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    digest TEXT NOT NULL,
    labels_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(labels_json) AND json_type(labels_json) = 'array'),
    instructions TEXT NOT NULL,
    PRIMARY KEY (repository_id, workflow_id),
    UNIQUE (repository_id, path)
);

CREATE TABLE repository_workflow_diagnostics (
    repository_id TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    issue_number INTEGER NOT NULL CHECK (issue_number > 0),
    issue_url TEXT NOT NULL,
    code TEXT NOT NULL,
    message TEXT NOT NULL,
    workflow_ids_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(workflow_ids_json) AND json_type(workflow_ids_json) = 'array'),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (repository_id, issue_number)
);

ALTER TABLE tasks ADD COLUMN repository_workflow_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN repository_workflow_snapshot TEXT NOT NULL DEFAULT '{}'
    CHECK (json_valid(repository_workflow_snapshot) AND json_type(repository_workflow_snapshot) = 'object');

CREATE INDEX repository_workflow_entries_path
    ON repository_workflow_entries(repository_id, path);
CREATE INDEX repository_workflow_diagnostics_updated
    ON repository_workflow_diagnostics(repository_id, updated_at DESC);
