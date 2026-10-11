-- +goose Up
-- The shop-suggestion header (chain + branch) of a receipt, read once from its OCR text and stored, so
-- suggesting a shop does not re-parse every receipt's OCR on each request.
ALTER TABLE receipts ADD COLUMN header_json TEXT;
ALTER TABLE receipts ADD COLUMN header_chain TEXT;   -- folded chain name, '' when no header was readable
ALTER TABLE receipts ADD COLUMN header_number TEXT;  -- house number of the branch address
ALTER TABLE receipts ADD COLUMN header_version INTEGER; -- shopsuggest.DetectVersion it was computed with
CREATE INDEX receipts_header ON receipts(header_chain, header_number) WHERE confirmed_at IS NULL;

-- +goose Down
DROP INDEX receipts_header;
ALTER TABLE receipts DROP COLUMN header_version;
ALTER TABLE receipts DROP COLUMN header_number;
ALTER TABLE receipts DROP COLUMN header_chain;
ALTER TABLE receipts DROP COLUMN header_json;
