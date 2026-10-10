-- +goose Up
INSERT INTO categories (name, sort) VALUES
  ('Zuivel', 10),
  ('Groente & fruit', 20),
  ('Vlees & vis', 30),
  ('Brood', 40),
  ('Voorraad', 50),
  ('Diepvries', 60),
  ('Dranken', 70),
  ('Huishouden', 80),
  ('Verzorging', 90),
  ('Overig', 100);

-- +goose Down
DELETE FROM categories WHERE name IN (
  'Zuivel', 'Groente & fruit', 'Vlees & vis', 'Brood', 'Voorraad',
  'Diepvries', 'Dranken', 'Huishouden', 'Verzorging', 'Overig');
