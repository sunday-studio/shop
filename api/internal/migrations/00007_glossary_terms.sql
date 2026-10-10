-- +goose Up
-- The user's own glossary (docs/PLAN.md §6a), layered over the built-in one.
CREATE TABLE glossary_terms (
  dutch      TEXT PRIMARY KEY,           -- folded single word: lowercase, no accents
  english    TEXT,                       -- NULL when keep_as_is
  keep_as_is INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  CHECK (keep_as_is = 1 OR (english IS NOT NULL AND english != ''))
);

-- +goose Down
DROP TABLE glossary_terms;
