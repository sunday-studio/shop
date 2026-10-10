// Package english manages English product names: their status, the user's
// glossary (teach a word / keep as is), re-translation when the glossary
// changes, previews, and the "needs English" review queue (docs/PLAN.md §6a).
package english

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"shop/internal/store"
	"shop/internal/store/db"
	"shop/internal/translate"
)

var (
	ErrInvalid  = errors.New("invalid input")
	ErrNotFound = errors.New("not found")
)

type Service struct {
	conn *sql.DB
	q    *db.Queries
	mu   sync.Mutex // serialises glossary changes + re-translation
}

func New(conn *sql.DB) *Service { return &Service{conn: conn, q: db.New(conn)} }

// Load installs the stored user glossary into the translator.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.q.ListGlossaryTerms(ctx)
	if err != nil {
		return err
	}
	terms := make(map[string]translate.Term, len(rows))
	for _, r := range rows {
		terms[r.Dutch] = translate.Term{English: r.English.String, KeepAsIs: r.KeepAsIs != 0}
	}
	translate.SetUserTerms(terms)
	return nil
}

// ------------------------------------------------------------- status

// Status describes a product's English name for the UI.
type Status struct {
	Status  string           // manual | openfoodfacts | dictionary | partial | missing
	Unknown []translate.Span // over name_en (partial) or name (missing)
}

// ProductStatus classifies a product's English name.
func ProductStatus(p db.Product) Status {
	if src := p.NameEnSource.String; p.NameEnSource.Valid && src != "dictionary" {
		return Status{Status: src}
	}
	return statusFor(translate.Translate(p.Name))
}

func statusFor(r translate.Result) Status {
	st := Status{Status: r.Status()}
	switch st.Status {
	case translate.StatusPartial:
		st.Unknown = r.Unknown
	case translate.StatusMissing:
		st.Unknown = r.UnknownInput
	}
	return st
}

// LineGloss is the English rendering of a receipt line (computed, not stored).
type LineGloss struct {
	TextEN  *string
	Status  string
	Unknown []translate.Span // over TextEN, or raw_text when there's no gloss
}

func Gloss(rawText string) LineGloss {
	r := translate.Translate(rawText)
	g := LineGloss{Status: r.Status()}
	if r.OK() {
		g.TextEN = &r.Text
		if !r.Complete {
			g.Unknown = r.Unknown
		}
		return g
	}
	g.Unknown = r.UnknownInput
	return g
}

// --------------------------------------------------------- re-translate

// autoName is what an automatic product gets stored as.
func autoName(r translate.Result) (sql.NullString, sql.NullString) {
	if r.OK() {
		return store.Str(r.Text), store.Str("dictionary")
	}
	return sql.NullString{}, sql.NullString{}
}

// Retranslate refreshes every automatic English name with the current
// glossaries and returns how many changed. Manual and Open Food Facts
// names are never touched.
func (s *Service) Retranslate(ctx context.Context) (int, error) {
	rows, err := s.q.ProductsForTranslation(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		name, src := autoName(translate.Translate(r.Name))
		if name == r.NameEn && src == r.NameEnSource {
			continue
		}
		if err := s.q.SetProductEnglish(ctx, db.SetProductEnglishParams{NameEn: name, NameEnSource: src, ID: r.ID}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ---------------------------------------------------------------- terms

// TermInput is what the user teaches for one word.
type TermInput struct {
	English  string
	KeepAsIs bool
}

// NormaliseKey validates a single word and returns its glossary key.
func NormaliseKey(word string) (string, error) {
	key := translate.Key(word)
	if key == "" || strings.ContainsFunc(key, unicode.IsSpace) {
		return "", fmt.Errorf("%w: teach one word at a time", ErrInvalid)
	}
	if !strings.ContainsFunc(key, unicode.IsLetter) {
		return "", fmt.Errorf("%w: %q has no letters", ErrInvalid, word)
	}
	return key, nil
}

func (in *TermInput) validate() error {
	in.English = strings.TrimSpace(in.English)
	if in.KeepAsIs {
		in.English = ""
		return nil
	}
	if in.English == "" {
		return fmt.Errorf("%w: give the English or keep_as_is", ErrInvalid)
	}
	if len(in.English) > 80 {
		return fmt.Errorf("%w: english is too long", ErrInvalid)
	}
	return nil
}

type Term struct {
	Dutch         string
	English       string
	KeepAsIs      bool
	ProductsCount int
	UpdatedAt     time.Time
}

func termFrom(r db.GlossaryTerm) Term {
	t, _ := store.ParseTS(r.UpdatedAt)
	return Term{Dutch: r.Dutch, English: r.English.String, KeepAsIs: r.KeepAsIs != 0, UpdatedAt: t}
}

// Teach saves a term, re-translates, and returns the term, its previous
// value (nil if new) and how many products changed.
func (s *Service) Teach(ctx context.Context, word string, in TermInput) (Term, *Term, int, error) {
	key, err := NormaliseKey(word)
	if err != nil {
		return Term{}, nil, 0, err
	}
	if err := in.validate(); err != nil {
		return Term{}, nil, 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	before, err := s.snapshot(ctx)
	if err != nil {
		return Term{}, nil, 0, err
	}
	var prev *Term
	if old, err := s.q.GetGlossaryTerm(ctx, key); err == nil {
		t := termFrom(old)
		prev = &t
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Term{}, nil, 0, err
	}
	row, err := s.q.UpsertGlossaryTerm(ctx, db.UpsertGlossaryTermParams{
		Dutch: key, English: store.Str(in.English), KeepAsIs: boolInt(in.KeepAsIs), UpdatedAt: store.Now(),
	})
	if err != nil {
		return Term{}, nil, 0, err
	}
	n, err := s.reload(ctx, before)
	t := termFrom(row)
	t.ProductsCount, _ = s.productsUsing(ctx, key)
	return t, prev, n, err
}

// Forget deletes a term and re-translates.
func (s *Service) Forget(ctx context.Context, word string) (int, error) {
	key, err := NormaliseKey(word)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before, err := s.snapshot(ctx)
	if err != nil {
		return 0, err
	}
	n, err := s.q.DeleteGlossaryTerm(ctx, key)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrNotFound
	}
	return s.reload(ctx, before)
}

// reload installs the changed glossary, re-translates, and returns how many
// automatic products changed name *or status* (a keep-as-is word leaves
// the name as is but takes the product out of "needs English").
func (s *Service) reload(ctx context.Context, before map[int64]string) (int, error) {
	if err := s.Load(ctx); err != nil {
		return 0, err
	}
	if _, err := s.Retranslate(ctx); err != nil {
		return 0, err
	}
	after, err := s.snapshot(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for id, v := range after {
		if before[id] != v {
			n++
		}
	}
	return n, nil
}

// snapshot records name + status of every automatic product.
func (s *Service) snapshot(ctx context.Context) (map[int64]string, error) {
	rows, err := s.q.ProductsForTranslation(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]string, len(rows))
	for _, r := range rows {
		m[r.ID] = r.NameEn.String + "\x00" + translate.Translate(r.Name).Status()
	}
	return m, nil
}

// Terms lists the user glossary with how many products use each word.
func (s *Service) Terms(ctx context.Context) ([]Term, error) {
	rows, err := s.q.ListGlossaryTerms(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := s.wordCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Term, len(rows))
	for i, r := range rows {
		out[i] = termFrom(r)
		out[i].ProductsCount = counts[r.Dutch]
	}
	return out, nil
}

// wordCounts maps each folded word to the number of products whose Dutch
// name contains it.
func (s *Service) wordCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.q.ProductNames(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, r := range rows {
		seen := map[string]bool{}
		for _, w := range strings.Fields(r.Name) {
			k := translate.Key(w)
			if k != "" && !seen[k] {
				seen[k] = true
				counts[k]++
			}
		}
	}
	return counts, nil
}

func (s *Service) productsUsing(ctx context.Context, key string) (int, error) {
	c, err := s.wordCounts(ctx)
	return c[key], err
}

// ------------------------------------------------------------- preview

type PreviewExample struct {
	ProductID    int64
	Name         string
	NameENBefore *string
	NameENAfter  *string
	StatusAfter  string
}

// Preview shows what teaching a term would do, without saving it.
func (s *Service) Preview(ctx context.Context, word string, in TermInput, examples int) (int, []PreviewExample, error) {
	key, err := NormaliseKey(word)
	if err != nil {
		return 0, nil, err
	}
	if err := in.validate(); err != nil {
		return 0, nil, err
	}
	rows, err := s.q.ProductsForTranslation(ctx)
	if err != nil {
		return 0, nil, err
	}
	affected := 0
	var out []PreviewExample
	for _, r := range rows {
		after := translate.TranslateWith(r.Name, key, translate.Term{English: in.English, KeepAsIs: in.KeepAsIs})
		name, src := autoName(after)
		before := translate.Translate(r.Name)
		if name == r.NameEn && src == r.NameEnSource && after.Status() == before.Status() {
			continue
		}
		affected++
		if len(out) < examples {
			out = append(out, PreviewExample{ProductID: r.ID, Name: r.Name,
				NameENBefore: store.StrPtr(r.NameEn), NameENAfter: store.StrPtr(name), StatusAfter: after.Status()})
		}
	}
	return affected, out, nil
}

// ---------------------------------------------------------------- todo

type TodoWord struct {
	Word     string // display form, as first seen
	Key      string
	Products int
	Lines    int
	Examples []string // up to 3 product names
}

type TodoProduct struct {
	ID        int64
	Name      string
	NameEN    *string
	Status    Status
	ShopName  *string
	ShopChain *string
	lastAt    string
}

type Todo struct {
	WordsLeft, ProductsLeft int
	Words                   []TodoWord
	Products                []TodoProduct
}

// unmatchedWindow bounds how far back unmatched receipt lines count.
const unmatchedWindow = 180 * 24 * time.Hour

// Todo builds the "needs English" queue: unknown words ranked by how many
// products (then receipt lines) use them, and the products that need
// English, most recently bought first. Totals cover everything; limit only
// trims the lists.
func (s *Service) Todo(ctx context.Context, limit int, now time.Time) (Todo, error) {
	rows, err := s.q.ProductsForTranslation(ctx)
	if err != nil {
		return Todo{}, err
	}
	latest, err := s.q.LatestPurchases(ctx)
	if err != nil {
		return Todo{}, err
	}
	shops := map[int64]db.LatestPurchasesRow{}
	for _, l := range latest {
		shops[l.ProductID.Int64] = l
	}

	words := map[string]*TodoWord{}
	var products []TodoProduct
	for _, r := range rows {
		res := translate.Translate(r.Name)
		if res.Complete {
			continue
		}
		st := statusFor(res)
		tp := TodoProduct{ID: r.ID, Name: r.Name, NameEN: store.StrPtr(r.NameEn), Status: st}
		if l, ok := shops[r.ID]; ok {
			tp.ShopName, tp.ShopChain, tp.lastAt = &l.ShopName, store.StrPtr(l.ShopChain), l.At
		}
		products = append(products, tp)
		seen := map[string]bool{}
		for _, sp := range res.UnknownInput {
			if seen[sp.Key] {
				continue
			}
			seen[sp.Key] = true
			w := words[sp.Key]
			if w == nil {
				w = &TodoWord{Word: sp.Word, Key: sp.Key}
				words[sp.Key] = w
			}
			w.Products++
			if len(w.Examples) < 3 {
				w.Examples = append(w.Examples, r.Name)
			}
		}
	}

	texts, err := s.q.UnmatchedItemTexts(ctx, store.Str(store.TS(now.Add(-unmatchedWindow))))
	if err != nil {
		return Todo{}, err
	}
	for _, t := range texts {
		seen := map[string]bool{}
		for _, sp := range translate.Translate(t).UnknownInput {
			if w := words[sp.Key]; w != nil && !seen[sp.Key] {
				seen[sp.Key] = true
				w.Lines++
			}
		}
	}

	out := Todo{WordsLeft: len(words), ProductsLeft: len(products)}
	for _, w := range words {
		out.Words = append(out.Words, *w)
	}
	sort.Slice(out.Words, func(i, j int) bool {
		a, b := out.Words[i], out.Words[j]
		if a.Products != b.Products {
			return a.Products > b.Products
		}
		if a.Lines != b.Lines {
			return a.Lines > b.Lines
		}
		return a.Key < b.Key
	})
	sort.SliceStable(products, func(i, j int) bool {
		if products[i].lastAt != products[j].lastAt {
			return products[i].lastAt > products[j].lastAt // never bought ("") sorts last
		}
		return products[i].ID > products[j].ID
	})
	out.Products = products
	if limit > 0 {
		out.Words = out.Words[:min(limit, len(out.Words))]
		out.Products = out.Products[:min(limit, len(out.Products))]
	}
	return out, nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
