CREATE TABLE github_pr_wakes (
    repository_id TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    pull_request_url TEXT NOT NULL,
    active INTEGER NOT NULL CHECK (active IN (0, 1)),
    last_seen_key TEXT NOT NULL,
    PRIMARY KEY (repository_id, pull_request_url)
);
