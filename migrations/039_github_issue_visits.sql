CREATE TABLE github_issue_visits (
    repository_id TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    issue_number INTEGER NOT NULL,
    visit_key TEXT NOT NULL,
    active INTEGER NOT NULL CHECK (active IN (0, 1)),
    last_seen_key TEXT NOT NULL,
    PRIMARY KEY (repository_id, issue_number)
);
