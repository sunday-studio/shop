-- +goose Up
-- date_locked: purchased_at came from the user (upload or edit); the parser must not overwrite it.
-- header_edited: shop/date/total were edited by the user; reparse needs force.
ALTER TABLE receipts ADD COLUMN date_locked INTEGER NOT NULL DEFAULT 0;
ALTER TABLE receipts ADD COLUMN header_edited INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE receipts DROP COLUMN header_edited;
ALTER TABLE receipts DROP COLUMN date_locked;
