-- +goose Up
-- "Keep both": the user looked at a possible duplicate and decided it is a separate receipt.
-- Remembered per (newer) receipt; the duplicate itself is never stored, it is computed on demand.
CREATE TABLE receipt_duplicate_dismissals (
  receipt_id   INTEGER PRIMARY KEY REFERENCES receipts(id) ON DELETE CASCADE,
  dismissed_at TEXT NOT NULL
);
-- The duplicate lookup starts from the printed total.
CREATE INDEX receipts_total ON receipts(total_cents) WHERE total_cents IS NOT NULL;

-- +goose Down
DROP INDEX receipts_total;
DROP TABLE receipt_duplicate_dismissals;
