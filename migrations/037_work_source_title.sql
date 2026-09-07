ALTER TABLE sessions ADD COLUMN source_title TEXT NOT NULL DEFAULT ''
CHECK (length(CAST(source_title AS BLOB)) <= 1024);
