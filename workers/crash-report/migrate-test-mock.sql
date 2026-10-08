-- Apply: wrangler d1 execute reasonix-crash --remote --file=migrate-test-mock.sql
--
-- Task 642: lab mock-crash drill. The desktop lab entry sends clearly marked
-- synthetic reports through the real pipeline; the receiving end stores the
-- flag on every raw sample so the dashboard can tell the drill apart from a
-- real failure. Existing rows default to 0 (not a mock). Plain additive ALTER:
-- SQLite has no ADD COLUMN IF NOT EXISTS, so a duplicate run errors with
-- "duplicate column name" and is harmless (the column is already there).
ALTER TABLE reports ADD COLUMN test_mock INTEGER NOT NULL DEFAULT 0;
