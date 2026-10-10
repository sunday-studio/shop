package shopsuggest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"shop/internal/logos"
	"shop/internal/match"
	"shop/internal/ocr"
	"shop/internal/receipt"
	"shop/internal/store"
	"shop/internal/store/db"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrConfirmed = errors.New("receipt is confirmed")
	ErrInvalid   = errors.New("invalid input")
)

// Suggestion is what the API returns.
type Suggestion struct {
	Kind     string // "existing" | "new"
	ShopID   *int64
	Name     string
	Chain    string
	Branch   string
	Conf     float64
	Evidence []string
	Waiting  []int64
}

type Service struct {
	conn *sql.DB
	q    *db.Queries
}

func NewService(conn *sql.DB) *Service { return &Service{conn: conn, q: db.New(conn)} }

// detectFromJSON reads the header of a stored OCR result.
func detectFromJSON(raw sql.NullString) *Detected {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	var res ocr.Result
	if err := json.Unmarshal([]byte(raw.String), &res); err != nil {
		return nil
	}
	rows := receipt.BuildRows(res.Lines)
	texts := make([]string, len(rows))
	for i, r := range rows {
		texts[i] = r.Raw
	}
	return Detect(texts)
}

// Suggest computes the suggestion for one receipt; nil when there is nothing safe to say.
func (s *Service) Suggest(ctx context.Context, receiptID int64) (*Suggestion, error) {
	rec, err := s.q.GetReceipt(ctx, receiptID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	d := detectFromJSON(rec.RawOcrJson)
	if d == nil || d.Key == "" {
		return nil, nil // no readable header, or no branch to key on
	}
	if n, err := s.q.IsShopSuggestionRejected(ctx, d.Key); err != nil {
		return nil, err
	} else if n > 0 {
		return nil, nil
	}
	shops, err := s.q.ListShops(ctx)
	if err != nil {
		return nil, err
	}
	sug := &Suggestion{Kind: "new", Name: d.Chain, Chain: d.Chain, Branch: d.Branch, Conf: d.Conf, Evidence: d.Evidence}
	if sh := findShop(shops, d); sh != nil {
		if sh.ID == rec.ShopID {
			return nil, nil // already on the right shop
		}
		sug.Kind, sug.ShopID, sug.Name = "existing", &sh.ID, sh.Name
	}
	all, err := s.q.ListReceiptsForShopSuggestion(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range all {
		if o.ID == rec.ID || o.ConfirmedAt.Valid || (sug.ShopID != nil && o.ShopID == *sug.ShopID) {
			continue
		}
		if SameBranch(d, detectFromJSON(o.RawOcrJson)) {
			sug.Waiting = append(sug.Waiting, o.ID)
		}
	}
	return sug, nil
}

// findShop returns the existing shop that is this chain (and branch, when the shop records one).
func findShop(shops []db.Shop, d *Detected) *db.Shop {
	chain := match.Fold(d.Chain)
	var byChain []*db.Shop
	for i := range shops {
		sh := &shops[i]
		name, ch := match.Fold(sh.Name), match.Fold(sh.Chain.String)
		if name == chain || ch == chain || strings.HasPrefix(name, chain+" ") {
			byChain = append(byChain, sh)
		}
	}
	if len(byChain) == 0 {
		return nil
	}
	// A shop whose recorded address/branch mentions the street + number is the branch.
	for _, sh := range byChain {
		hay := match.Fold(sh.Branch.String + " " + sh.Address.String)
		if d.Street != "" && strings.Contains(hay, d.Number) && strings.Contains(hay, strings.Fields(d.Street)[0]) {
			return sh
		}
	}
	// Otherwise a chain shop with no branch recorded stands for the chain.
	for _, sh := range byChain {
		if !sh.Branch.Valid && !sh.Address.Valid {
			return sh
		}
	}
	if len(byChain) == 1 {
		return byChain[0]
	}
	return nil
}

// Accept moves the receipt (and, with applyWaiting, the other unconfirmed receipts from the same
// branch) onto a shop: the given existing one, or a new one called name.
func (s *Service) Accept(ctx context.Context, receiptID int64, shopID *int64, name string, applyWaiting bool) (db.Shop, []int64, error) {
	rec, err := s.q.GetReceipt(ctx, receiptID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Shop{}, nil, ErrNotFound
	} else if err != nil {
		return db.Shop{}, nil, err
	}
	if rec.ConfirmedAt.Valid {
		return db.Shop{}, nil, ErrConfirmed
	}
	d := detectFromJSON(rec.RawOcrJson)

	var shop db.Shop
	switch {
	case shopID != nil:
		shop, err = s.q.GetShop(ctx, *shopID)
		if errors.Is(err, sql.ErrNoRows) {
			return db.Shop{}, nil, fmt.Errorf("%w: shop %d does not exist", ErrInvalid, *shopID)
		} else if err != nil {
			return db.Shop{}, nil, err
		}
	default:
		sug, err := s.Suggest(ctx, receiptID)
		if err != nil {
			return db.Shop{}, nil, err
		}
		name = strings.TrimSpace(name)
		if name == "" && sug != nil {
			name = sug.Name
		}
		if name == "" {
			return db.Shop{}, nil, fmt.Errorf("%w: no suggestion for this receipt; pass shop_id or name", ErrInvalid)
		}
		var chain, branch, address, city sql.NullString
		if d != nil && d.Known {
			chain = store.Str(d.Chain)
		}
		if d != nil && d.Branch != "" {
			branch = store.Str(d.Branch)
			address = store.Str(strings.TrimSpace(titleCase(d.Street) + " " + d.Number))
			if i := strings.LastIndex(d.Branch, ", "); i >= 0 {
				city = store.Str(d.Branch[i+2:])
			}
		}
		now := store.Now()
		shop, err = s.q.CreateShop(ctx, db.CreateShopParams{
			Name: name, Chain: chain, Branch: branch, Address: address, City: city,
			Domain: store.Str(logos.GuessDomain(name, chain.String)), CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return db.Shop{}, nil, err
		}
	}

	ids := []int64{rec.ID}
	if applyWaiting && d != nil {
		all, err := s.q.ListReceiptsForShopSuggestion(ctx)
		if err != nil {
			return db.Shop{}, nil, err
		}
		for _, o := range all {
			if o.ID != rec.ID && !o.ConfirmedAt.Valid && o.ShopID != shop.ID && SameBranch(d, detectFromJSON(o.RawOcrJson)) {
				ids = append(ids, o.ID)
			}
		}
	}
	now := store.Now()
	var moved []int64
	for _, id := range ids {
		if n, err := s.q.SetReceiptShop(ctx, db.SetReceiptShopParams{ShopID: shop.ID, UpdatedAt: now, ID: id}); err != nil {
			return db.Shop{}, nil, err
		} else if n > 0 {
			moved = append(moved, id)
		}
	}
	return shop, moved, nil
}

// Reject remembers the receipt's branch so it is not suggested again.
func (s *Service) Reject(ctx context.Context, receiptID int64) error {
	rec, err := s.q.GetReceipt(ctx, receiptID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if d := detectFromJSON(rec.RawOcrJson); d != nil && d.Key != "" {
		return s.q.RejectShopSuggestion(ctx, db.RejectShopSuggestionParams{BranchKey: d.Key, CreatedAt: store.Now()})
	}
	return nil
}
