-- +goose Up
-- A shop suggestion the user turned down is remembered by branch key (chain|street number), so the
-- same header is not suggested again.
CREATE TABLE shop_suggestion_rejections (
  branch_key TEXT PRIMARY KEY,
  created_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE shop_suggestion_rejections;
