// Package spike is the OCR/parser evaluation harness (docs/PLAN.md §13):
// run several OCR engines over a folder of real receipts, cache their
// output, score the parser against hand-labelled truth, and write a report.
package spike

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"shop/internal/ocr"
	"shop/internal/receipt"
)

// Layout of the spike directory.
type Dir struct{ Root string }

func (d Dir) Receipts() string           { return filepath.Join(d.Root, "receipts") }
func (d Dir) TruthCSV() string           { return filepath.Join(d.Root, "truth.csv") }
func (d Dir) LinesDir() string           { return filepath.Join(d.Root, "lines") }
func (d Dir) Cache(engine string) string { return filepath.Join(d.Root, "ocr", engine) }
func (d Dir) EnginesFile() string        { return filepath.Join(d.Root, "engines.json") }
func (d Dir) ResultsCSV() string         { return filepath.Join(d.Root, "results.csv") }

var imageExts = []string{".jpg", ".jpeg", ".png"}

// Images lists receipt images by id: the path under receipts/ without the
// extension, e.g. "ah/001".
func (d Dir) Images() (map[string]string, []string, error) {
	paths := map[string]string{}
	err := filepath.WalkDir(d.Receipts(), func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() || !slices.Contains(imageExts, strings.ToLower(filepath.Ext(p))) {
			return nil
		}
		rel, _ := filepath.Rel(d.Receipts(), p)
		id := filepath.ToSlash(strings.TrimSuffix(rel, filepath.Ext(rel)))
		paths[id] = p
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return paths, nil, fmt.Errorf("no receipts folder at %s", d.Receipts())
	}
	ids := make([]string, 0, len(paths))
	for id := range paths {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return paths, ids, err
}

// ---------------------------------------------------------------- truth

// Truth is the hand label for one receipt (truth.csv).
type Truth struct {
	ID         string
	Shop       string
	Date       string // YYYY-MM-DD
	TotalCents int64
	Items      int // number of item lines (not discounts/deposits/bags)
	Notes      string
}

var truthHeader = []string{"id", "shop", "date", "total", "n_items", "notes"}

func formatCents(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s%d,%02d", sign, c/100, c%100)
}

// ParseMoney accepts "4,51", "4.51" or "-0,50".
func ParseMoney(s string) (int64, error) {
	if c, ok := receipt.ParseAmount(strings.TrimSpace(s)); ok {
		return c, nil
	}
	return 0, fmt.Errorf("invalid amount %q (use e.g. 4,51)", s)
}

func (d Dir) LoadTruth() (map[string]Truth, error) {
	f, err := os.Open(d.TruthCSV())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Truth{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	out := map[string]Truth{}
	for i, r := range recs {
		if i == 0 && len(r) > 0 && r[0] == "id" {
			continue
		}
		if len(r) < 5 {
			return nil, fmt.Errorf("truth.csv line %d: want %d columns", i+1, len(truthHeader))
		}
		total, err := ParseMoney(r[3])
		if err != nil {
			return nil, fmt.Errorf("truth.csv line %d: %w", i+1, err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(r[4]))
		if err != nil {
			return nil, fmt.Errorf("truth.csv line %d: n_items %q", i+1, r[4])
		}
		if _, err := time.Parse(time.DateOnly, strings.TrimSpace(r[2])); err != nil {
			return nil, fmt.Errorf("truth.csv line %d: date %q (want YYYY-MM-DD)", i+1, r[2])
		}
		t := Truth{ID: r[0], Shop: strings.TrimSpace(r[1]), Date: strings.TrimSpace(r[2]), TotalCents: total, Items: n}
		if len(r) > 5 {
			t.Notes = r[5]
		}
		out[t.ID] = t
	}
	return out, nil
}

func (d Dir) SaveTruth(all map[string]Truth) error {
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	tmp := d.TruthCSV() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	_ = w.Write(truthHeader)
	for _, id := range ids {
		t := all[id]
		_ = w.Write([]string{t.ID, t.Shop, t.Date, formatCents(t.TotalCents), strconv.Itoa(t.Items), t.Notes})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, d.TruthCSV())
}

// TruthLine is one hand-labelled receipt line (lines/<id>.json).
type TruthLine struct {
	Text           string  `json:"text"`
	Kind           string  `json:"kind"` // item | discount | deposit | bag
	Qty            float64 `json:"qty"`
	Unit           string  `json:"unit"` // pc | kg
	UnitPriceCents int64   `json:"unit_price_cents,omitempty"`
	TotalCents     int64   `json:"total_cents"`
}

func (d Dir) linesPath(id string) string {
	return filepath.Join(d.LinesDir(), filepath.FromSlash(id)+".json")
}

func (d Dir) LoadLines(id string) ([]TruthLine, bool, error) {
	b, err := os.ReadFile(d.linesPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var ls []TruthLine
	if err := json.Unmarshal(b, &ls); err != nil {
		return nil, false, fmt.Errorf("%s: %w", d.linesPath(id), err)
	}
	return ls, true, nil
}

func (d Dir) SaveLines(id string, ls []TruthLine) error {
	p := d.linesPath(id)
	if ls == nil {
		err := os.Remove(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(ls, "", "  ")
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// ---------------------------------------------------------------- cache

// Cached is one engine's output for one image plus how long it took.
type Cached struct {
	Result ocr.Result `json:"result"`
	WallMS int64      `json:"wall_ms"` // request round-trip, incl. upload
	At     time.Time  `json:"at"`
	Error  string     `json:"error,omitempty"`
}

func (d Dir) cachePath(engine, id string) string {
	return filepath.Join(d.Cache(engine), filepath.FromSlash(id)+".json")
}

func (d Dir) LoadCached(engine, id string) (Cached, bool, error) {
	b, err := os.ReadFile(d.cachePath(engine, id))
	if errors.Is(err, os.ErrNotExist) {
		return Cached{}, false, nil
	}
	if err != nil {
		return Cached{}, false, err
	}
	var c Cached
	return c, true, json.Unmarshal(b, &c)
}

func (d Dir) SaveCached(engine, id string, c Cached) error {
	p := d.cachePath(engine, id)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(c)
	return os.WriteFile(p, b, 0o644)
}

// EngineMeta holds per-engine run facts (peak memory).
type EngineMeta struct {
	PeakMemMB float64   `json:"peak_mem_mb,omitempty"`
	Version   string    `json:"version,omitempty"`
	Updated   time.Time `json:"updated"`
}

func (d Dir) LoadMeta(engine string) EngineMeta {
	var m EngineMeta
	b, err := os.ReadFile(filepath.Join(d.Cache(engine), "_meta.json"))
	if err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (d Dir) SaveMeta(engine string, m EngineMeta) error {
	if err := os.MkdirAll(d.Cache(engine), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(filepath.Join(d.Cache(engine), "_meta.json"), b, 0o644)
}
