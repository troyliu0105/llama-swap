-- SQLite does not support DROP COLUMN before 3.35.0.
-- On older SQLite this migration is a no-op; on newer versions the column
-- can be removed with: ALTER TABLE captures DROP COLUMN seq_num;
SELECT 1;
