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
	Domain   string
	Conf     float64
	Evidence []string
	Waiting  []int64
	Key      string // identity: "shop:<id>" or "new:<chain>|<street number>"; equal keys are one card
	Street   string // new shops: canonical street, number and town
	Number   string
	Town     string
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
//
// Identity rules (so the app can show one card per shop):
//   - kind=existing: name, branch, domain and shop_id come from the shop itself, never from the OCR of this
//     receipt, so every receipt that matches the shop gets the identical suggestion.
//   - kind=new: name and branch come from the lowest-numbered unconfirmed receipt of the same branch, so
//     every receipt of that branch gets the identical suggestion even when OCR spells the street differently.
//   - kind=new is only offered when no existing shop matches the chain or name.
func (s *Service) Suggest(ctx context.Context, receiptID int64) (*Suggestion, error) {
	rec, err := s.q.GetReceipt(ctx, receiptID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	d, err := s.headerOf(ctx, rec)
	if err != nil {
		return nil, err
	}
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
	sh := findShop(shops, d)
	if sh != nil && rec.ShopID.Valid && sh.ID == rec.ShopID.Int64 {
		return nil, nil // already on the right shop
	}
	// the receipts of this branch that this suggestion would move; for a new shop the cluster also
	// decides the canonical spelling
	type member struct {
		id int64
		d  *Detected
	}
	cluster := []member{{rec.ID, d}}
	var waiting []int64
	cands, err := s.q.ListHeaderCandidates(ctx, db.ListHeaderCandidatesParams{HeaderChain: sql.NullString{String: match.Fold(d.Chain), Valid: true}, HeaderNumber: sql.NullString{String: d.Number, Valid: true}, ID: rec.ID})
	if err != nil {
		return nil, err
	}
	for _, o := range cands {
		if sh != nil && o.ShopID.Valid && o.ShopID.Int64 == sh.ID {
			continue
		}
		if od := unmarshalHeader(o.HeaderJson.String); SameBranch(d, od) {
			waiting = append(waiting, o.ID)
			cluster = append(cluster, member{o.ID, od})
		}
	}

	sug := &Suggestion{Conf: d.Conf, Evidence: d.Evidence, Waiting: waiting}
	if sh != nil {
		sug.Kind, sug.ShopID, sug.Name, sug.Domain = "existing", &sh.ID, sh.Name, sh.Domain.String
		sug.Chain, sug.Branch = sh.Chain.String, sh.Branch.String
		sug.Key = fmt.Sprintf("shop:%d", sh.ID)
		return sug, nil
	}
	canon := cluster[0]
	for _, m := range cluster {
		if m.id < canon.id {
			canon = m
		}
	}
	cd := canon.d
	sug.Kind, sug.Name, sug.Chain, sug.Branch = "new", cd.Chain, cd.Chain, cd.Branch
	sug.Street, sug.Number, sug.Town = cd.Street, cd.Number, cd.town
	sug.Domain = logos.GuessDomain(cd.Chain, cd.Chain)
	sug.Key = "new:" + cd.Key
	return sug, nil
}

// sameShopName: two names are the same shop when they are equal ignoring case and accents, one is the
// other plus more words ("Dirk" / "Dirk van den Broek"), they are the same built-in chain ("AH" /
// "Albert Heijn"), or they are almost identical.
func sameShopName(a, b string) bool {
	fa, fb := match.Fold(a), match.Fold(b)
	if fa == "" || fb == "" {
		return false
	}
	if fa == fb || strings.HasPrefix(fa, fb+" ") || strings.HasPrefix(fb, fa+" ") {
		return true
	}
	if ca, ok := CanonicalChain(a); ok {
		if cb, ok := CanonicalChain(b); ok {
			return ca == cb
		}
	}
	return match.JaroWinkler(fa, fb) >= 0.94
}

// findShop returns the existing shop for the detected chain (and branch, when several shops share it).
func findShop(shops []db.Shop, d *Detected) *db.Shop {
	var byChain []*db.Shop
	for i := range shops {
		sh := &shops[i]
		if sameShopName(sh.Name, d.Chain) || (sh.Chain.Valid && sameShopName(sh.Chain.String, d.Chain)) {
			byChain = append(byChain, sh)
		}
	}
	if len(byChain) == 0 {
		return nil
	}
	if len(byChain) == 1 {
		return byChain[0] // the only shop of that chain, whatever branch OCR read
	}
	// several branches: the one whose recorded address is this street + number (typos tolerated)
	for _, sh := range byChain {
		if sameAddress(sh, d) {
			return sh
		}
	}
	// a chain shop with no branch recorded stands for the chain
	for _, sh := range byChain {
		if !sh.Branch.Valid && !sh.Address.Valid {
			return sh
		}
	}
	return nil
}

// sameAddress compares a shop's recorded address with the detected street and number.
func sameAddress(sh *db.Shop, d *Detected) bool {
	if d.Street == "" {
		return false
	}
	for _, text := range []string{sh.Address.String, sh.Branch.String} {
		f := glueRe.ReplaceAllString(match.Fold(text), "$1 $2")
		if m := streetRe.FindStringSubmatch(f); m != nil &&
			strings.ReplaceAll(strings.TrimSpace(m[2]), " ", "") == d.Number &&
			match.JaroWinkler(strings.TrimSpace(m[1]), d.Street) >= 0.9 {
			return true
		}
	}
	return false
}

// Accept moves the receipt (and, with applyWaiting, the other unconfirmed receipts from the same
// branch) onto a shop: the given existing one, or a new one called name.
func (s *Service) Accept(ctx context.Context, receiptID int64, shopID *int64, name, branchOverride, domainOverride string, applyWaiting bool) (db.Shop, []int64, error) {
	rec, err := s.q.GetReceipt(ctx, receiptID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Shop{}, nil, ErrNotFound
	} else if err != nil {
		return db.Shop{}, nil, err
	}
	if rec.ConfirmedAt.Valid {
		return db.Shop{}, nil, ErrConfirmed
	}
	d, err := s.headerOf(ctx, rec)
	if err != nil {
		return db.Shop{}, nil, err
	}

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
		if sug != nil && sug.Kind == "new" && sug.Branch != "" { // the cluster's canonical spelling
			branch = store.Str(sug.Branch)
			address = store.Str(strings.TrimSpace(titleCase(sug.Street) + " " + sug.Number))
			if sug.Town != "" {
				city = store.Str(titleCase(sug.Town))
			}
		} else if d != nil && d.Branch != "" {
			branch = store.Str(d.Branch)
			address = store.Str(strings.TrimSpace(titleCase(d.Street) + " " + d.Number))
			if i := strings.LastIndex(d.Branch, ", "); i >= 0 {
				city = store.Str(d.Branch[i+2:])
			}
		}
		if b := strings.TrimSpace(branchOverride); b != "" {
			branch = store.Str(b)
		}
		domain := logos.GuessDomain(name, chain.String)
		if dm := strings.TrimSpace(domainOverride); dm != "" {
			if domain, err = logos.NormaliseDomain(dm); err != nil {
				return db.Shop{}, nil, fmt.Errorf("%w: domain must look like ah.nl", ErrInvalid)
			}
		}
		now := store.Now()
		shop, err = s.q.CreateShop(ctx, db.CreateShopParams{
			Name: name, Chain: chain, Branch: branch, Address: address, City: city,
			Domain: store.Str(domain), CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return db.Shop{}, nil, err
		}
	}

	ids := []int64{rec.ID}
	if applyWaiting && d != nil {
		cands, err := s.q.ListHeaderCandidates(ctx, db.ListHeaderCandidatesParams{HeaderChain: sql.NullString{String: match.Fold(d.Chain), Valid: true}, HeaderNumber: sql.NullString{String: d.Number, Valid: true}, ID: rec.ID})
		if err != nil {
			return db.Shop{}, nil, err
		}
		for _, o := range cands {
			if (!o.ShopID.Valid || o.ShopID.Int64 != shop.ID) && SameBranch(d, unmarshalHeader(o.HeaderJson.String)) {
				ids = append(ids, o.ID)
			}
		}
	}
	now := store.Now()
	var moved []int64
	for _, id := range ids {
		if n, err := s.q.SetReceiptShop(ctx, db.SetReceiptShopParams{ShopID: sql.NullInt64{Int64: shop.ID, Valid: true}, UpdatedAt: now, ID: id}); err != nil {
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
	d, err := s.headerOf(ctx, rec)
	if err != nil {
		return err
	}
	if d != nil && d.Key != "" {
		return s.q.RejectShopSuggestion(ctx, db.RejectShopSuggestionParams{BranchKey: d.Key, CreatedAt: store.Now()})
	}
	return nil
}
