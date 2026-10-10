-- +goose Up
-- Web domain of the shop (e.g. ah.nl), used to fetch its logo. New shops get
-- it guessed in Go (internal/logos); this backfills existing shops once.
ALTER TABLE shops ADD COLUMN domain TEXT;

UPDATE shops SET domain = CASE
    WHEN lower(COALESCE(chain, name)) = 'ah' OR lower(COALESCE(chain, name)) LIKE 'ah %'
      OR lower(COALESCE(chain, name)) LIKE 'albert heijn%' THEN 'ah.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'jumbo%' THEN 'jumbo.com'
    WHEN lower(COALESCE(chain, name)) LIKE 'lidl%' THEN 'lidl.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'aldi%' THEN 'aldi.nl'
    WHEN lower(COALESCE(chain, name)) = 'plus' OR lower(COALESCE(chain, name)) LIKE 'plus %' THEN 'plus.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'dirk%' THEN 'dirk.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'coop%' THEN 'coop.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'spar%' THEN 'spar.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'vomar%' THEN 'vomar.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'hoogvliet%' THEN 'hoogvliet.com'
    WHEN lower(COALESCE(chain, name)) LIKE 'ekoplaza%' THEN 'ekoplaza.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'kruidvat%' THEN 'kruidvat.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'etos%' THEN 'etos.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'hema%' THEN 'hema.nl'
    WHEN lower(COALESCE(chain, name)) LIKE 'action%' THEN 'action.com'
  END
WHERE domain IS NULL;

-- +goose Down
ALTER TABLE shops DROP COLUMN domain;
