// Package categories manages product categories: unique case-insensitive names, colours, and deletion
// with reassignment of the products in them.
package categories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"shop/internal/store"
	"shop/internal/store/db"
)

// Colors are the ten tokens a category may use.
var Colors = []string{"blue", "green", "red", "orange", "yellow", "cyan", "purple", "teal", "pink", "grey"}

var (
	ErrNotFound  = errors.New("not found")
	ErrInvalid   = errors.New("invalid input")
	ErrNameTaken = errors.New("a category with this name exists")
)

// HasProductsError is returned by Delete when the category still has products and no move_to was given.
type HasProductsError struct{ Count int64 }

func (e HasProductsError) Error() string {
	return fmt.Sprintf("category has %d products; say where to move them", e.Count)
}

type Service struct {
	conn *sql.DB
	q    *db.Queries
}

func New(conn *sql.DB) *Service { return &Service{conn: conn, q: db.New(conn)} }

// Item is a category with the number of (non-deleted) products in it.
type Item struct {
	db.Category
	ProductCount int64
}

// List returns the categories alphabetically (case-insensitive) with product counts.
func (s *Service) List(ctx context.Context) ([]Item, error) {
	rows, err := s.q.ListCategoriesWithCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Item, len(rows))
	for i, r := range rows {
		out[i] = Item{Category: db.Category{ID: r.ID, Name: r.Name, Sort: r.Sort, DeletedAt: r.DeletedAt, Color: r.Color}, ProductCount: r.ProductCount}
	}
	return out, nil
}

// Get returns one category with its product count.
func (s *Service) Get(ctx context.Context, id int64) (Item, error) {
	c, err := s.q.GetCategory(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	} else if err != nil {
		return Item{}, err
	}
	n, err := s.q.CountCategoryProducts(ctx, sql.NullInt64{Int64: id, Valid: true})
	return Item{Category: c, ProductCount: n}, err
}

func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", fmt.Errorf("%w: name is required", ErrInvalid)
	}
	return name, nil
}

func checkColor(c *string) error {
	if c != nil && !slices.Contains(Colors, *c) {
		return fmt.Errorf("%w: color must be one of %s", ErrInvalid, strings.Join(Colors, ", "))
	}
	return nil
}

// firstUnusedColor picks the first token no active category uses, wrapping when all ten are taken.
func firstUnusedColor(ctx context.Context, q *db.Queries) (string, error) {
	cats, err := q.ListCategories(ctx)
	if err != nil {
		return "", err
	}
	used := map[string]int{}
	for _, c := range cats {
		used[c.Color.String]++
	}
	for _, c := range Colors {
		if used[c] == 0 {
			return c, nil
		}
	}
	return Colors[len(cats)%len(Colors)], nil
}

// Create adds a category. A soft-deleted category with the same name (any case) is brought back instead
// of failing on the UNIQUE constraint.
func (s *Service) Create(ctx context.Context, name string, color *string) (Item, error) {
	name, err := cleanName(name)
	if err != nil {
		return Item{}, err
	}
	if err := checkColor(color); err != nil {
		return Item{}, err
	}
	var out db.Category
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		same, err := q.FindCategoriesByName(ctx, name)
		if err != nil {
			return err
		}
		var dead *db.Category
		for i := range same {
			if !same[i].DeletedAt.Valid {
				return ErrNameTaken
			}
			if dead == nil {
				dead = &same[i]
			}
		}
		col := ""
		if color != nil {
			col = *color
		} else if dead != nil && dead.Color.Valid {
			col = dead.Color.String
		} else if col, err = firstUnusedColor(ctx, q); err != nil {
			return err
		}
		if dead != nil {
			out, err = q.ReviveCategory(ctx, db.ReviveCategoryParams{Name: name, Color: store.Str(col), ID: dead.ID})
		} else {
			out, err = q.CreateCategory(ctx, db.CreateCategoryParams{Name: name, Color: store.Str(col)})
		}
		return err
	})
	if err != nil {
		return Item{}, err
	}
	return s.Get(ctx, out.ID)
}

// Update renames and/or recolours. Other active categories with the same name (any case) conflict; a
// soft-deleted one is renamed out of the way.
func (s *Service) Update(ctx context.Context, id int64, name string, color *string) (Item, error) {
	name, err := cleanName(name)
	if err != nil {
		return Item{}, err
	}
	if err := checkColor(color); err != nil {
		return Item{}, err
	}
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		cur, err := q.GetCategory(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		same, err := q.FindCategoriesByName(ctx, name)
		if err != nil {
			return err
		}
		for _, o := range same {
			if o.ID == id {
				continue
			}
			if !o.DeletedAt.Valid {
				return ErrNameTaken
			}
			if err := q.TombstoneCategoryName(ctx, o.ID); err != nil {
				return err
			}
		}
		col := cur.Color
		if color != nil {
			col = store.Str(*color)
		}
		_, err = q.UpdateCategory(ctx, db.UpdateCategoryParams{Name: name, Color: col, ID: id})
		return err
	})
	if err != nil {
		return Item{}, err
	}
	return s.Get(ctx, id)
}

// Delete soft-deletes a category. Products in it are moved to moveTo (nil = not given; 0 = no category) in
// the same transaction. With products and no moveTo nothing is deleted and HasProductsError is returned.
func (s *Service) Delete(ctx context.Context, id int64, moveTo *int64) error {
	return store.InTx(ctx, s.conn, func(q *db.Queries) error {
		if _, err := q.GetCategory(ctx, id); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		n, err := q.CountCategoryProducts(ctx, sql.NullInt64{Int64: id, Valid: true})
		if err != nil {
			return err
		}
		now := store.Now()
		var to sql.NullInt64
		if moveTo != nil && *moveTo != 0 {
			if *moveTo == id {
				return fmt.Errorf("%w: move_to is the category being deleted", ErrInvalid)
			}
			if _, err := q.GetCategory(ctx, *moveTo); errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: move_to category %d does not exist", ErrInvalid, *moveTo)
			} else if err != nil {
				return err
			}
			to = sql.NullInt64{Int64: *moveTo, Valid: true}
		}
		if n > 0 && moveTo == nil {
			return HasProductsError{Count: n}
		}
		if n > 0 {
			src := sql.NullString{}
			if to.Valid {
				src = store.Str("manual")
			}
			if _, err := q.MoveCategoryProducts(ctx, db.MoveCategoryProductsParams{ToCategory: to, Source: src, UpdatedAt: now, FromCategory: sql.NullInt64{Int64: id, Valid: true}}); err != nil {
				return err
			}
		}
		// taught keywords follow the products, or go with the category
		if to.Valid {
			if err := q.RepointCategoryKeywords(ctx, db.RepointCategoryKeywordsParams{CategoryID: to.Int64, CategoryID_2: id}); err != nil {
				return err
			}
		} else if err := q.DeleteCategoryKeywords(ctx, id); err != nil {
			return err
		}
		_, err = q.SoftDeleteCategory(ctx, db.SoftDeleteCategoryParams{DeletedAt: store.Str(now), ID: id})
		return err
	})
}
