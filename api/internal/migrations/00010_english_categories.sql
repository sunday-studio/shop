-- +goose Up
-- The app is English; the seed categories were Dutch. Rename in place so ids, product links
-- and any user-made categories are untouched.
-- name is UNIQUE, so a rename is skipped when the user already made a category with the English
-- name (the Dutch seed row then stays as it is rather than failing the migration).
UPDATE categories SET name = CASE name
  WHEN 'Zuivel' THEN 'Dairy'
  WHEN 'Groente & fruit' THEN 'Produce'
  WHEN 'Vlees & vis' THEN 'Meat & fish'
  WHEN 'Brood' THEN 'Bakery'
  WHEN 'Voorraad' THEN 'Pantry'
  WHEN 'Diepvries' THEN 'Frozen'
  WHEN 'Dranken' THEN 'Drinks'
  WHEN 'Huishouden' THEN 'Household'
  WHEN 'Verzorging' THEN 'Personal care'
  WHEN 'Overig' THEN 'Other'
  ELSE name END
WHERE name IN ('Zuivel', 'Groente & fruit', 'Vlees & vis', 'Brood', 'Voorraad',
               'Diepvries', 'Dranken', 'Huishouden', 'Verzorging', 'Overig')
  AND NOT EXISTS (
    SELECT 1 FROM categories o WHERE o.name = CASE categories.name
      WHEN 'Zuivel' THEN 'Dairy'
      WHEN 'Groente & fruit' THEN 'Produce'
      WHEN 'Vlees & vis' THEN 'Meat & fish'
      WHEN 'Brood' THEN 'Bakery'
      WHEN 'Voorraad' THEN 'Pantry'
      WHEN 'Diepvries' THEN 'Frozen'
      WHEN 'Dranken' THEN 'Drinks'
      WHEN 'Huishouden' THEN 'Household'
      WHEN 'Verzorging' THEN 'Personal care'
      WHEN 'Overig' THEN 'Other' END);

-- +goose Down
UPDATE categories SET name = CASE name
  WHEN 'Dairy' THEN 'Zuivel'
  WHEN 'Produce' THEN 'Groente & fruit'
  WHEN 'Meat & fish' THEN 'Vlees & vis'
  WHEN 'Bakery' THEN 'Brood'
  WHEN 'Pantry' THEN 'Voorraad'
  WHEN 'Frozen' THEN 'Diepvries'
  WHEN 'Drinks' THEN 'Dranken'
  WHEN 'Household' THEN 'Huishouden'
  WHEN 'Personal care' THEN 'Verzorging'
  WHEN 'Other' THEN 'Overig'
  ELSE name END
WHERE name IN ('Dairy', 'Produce', 'Meat & fish', 'Bakery', 'Pantry', 'Frozen', 'Drinks',
               'Household', 'Personal care', 'Other');
