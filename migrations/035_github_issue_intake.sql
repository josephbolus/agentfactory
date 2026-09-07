ALTER TABLE repositories
ADD COLUMN issue_intake_enabled INTEGER NOT NULL DEFAULT 0
CHECK (issue_intake_enabled IN (0, 1));
