-- +goose Up
-- Scan-time product matching (docs/BACKEND-STATUS.md): the matcher runs when a receipt is processed.
--   match_source  how the line got its product when the matcher did it: 'alias' (exact learned text) or
--                 'score' (fuzzy score >= 0.92). NULL = not matched by the scan (a user link, a link made
--                 on confirm, or no link).
--   match_score   the score of that match (1 for an alias).
--   suggestions   JSON [{"product_id":1,"score":0.84},...]: the best 3 candidates between 0.75 and 0.92
--                 for a line that stayed unlinked. NULL = the matcher never ran on this line.
ALTER TABLE receipt_lines ADD COLUMN match_source TEXT CHECK (match_source IN ('alias', 'score'));
ALTER TABLE receipt_lines ADD COLUMN match_score REAL;
ALTER TABLE receipt_lines ADD COLUMN suggestions TEXT;

-- Any later write of product_id that does not set match_source in the same statement (the link endpoint,
-- a link made on confirm, an alias learned for other lines, an unlink) is a decision of the user or of the
-- confirm step, not of the scan: the line stops being "auto" and its suggestions are spent.
-- +goose StatementBegin
CREATE TRIGGER receipt_lines_link_changed AFTER UPDATE OF product_id ON receipt_lines
WHEN NEW.match_source IS OLD.match_source
BEGIN
  UPDATE receipt_lines SET match_source = NULL, match_score = NULL, suggestions = NULL WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER receipt_lines_link_changed;
ALTER TABLE receipt_lines DROP COLUMN suggestions;
ALTER TABLE receipt_lines DROP COLUMN match_score;
ALTER TABLE receipt_lines DROP COLUMN match_source;
