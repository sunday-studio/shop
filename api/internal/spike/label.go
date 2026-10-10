package spike

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"shop/internal/receipt"
)

//go:embed label.html
var labelHTML []byte

// LabelServer serves a local page for labelling receipts, prefilled from a
// cached OCR engine's parse so most receipts only need checking.
type LabelServer struct {
	Dir     Dir
	Engines []string // prefill from the first engine with a cached result
	Loc     *time.Location
	mu      sync.Mutex
}

type labelLine struct {
	Text      string  `json:"text"`
	Kind      string  `json:"kind"`
	Qty       float64 `json:"qty"`
	Unit      string  `json:"unit"`
	UnitPrice string  `json:"unit_price"`
	Total     string  `json:"total"`
}

type labelForm struct {
	Shop   string      `json:"shop"`
	Date   string      `json:"date"`
	Total  string      `json:"total"`
	Items  int         `json:"n_items"`
	Notes  string      `json:"notes"`
	Lines  []labelLine `json:"lines"` // nil = no line-level label
	Source string      `json:"source,omitempty"`
}

type labelPayload struct {
	ID        string    `json:"id"`
	Index     int       `json:"index"`
	Count     int       `json:"count"`
	Labelled  int       `json:"labelled"`
	WithLines int       `json:"with_lines"`
	Prev      string    `json:"prev"`
	Next      string    `json:"next"`
	NextTodo  string    `json:"next_todo"`
	Saved     bool      `json:"saved"`
	Form      labelForm `json:"form"`
	Shops     []string  `json:"shops"`
	Reasons   []string  `json:"reasons"`
	Rows      []string  `json:"rows"`
}

func (s *LabelServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(labelHTML)
	})
	mux.HandleFunc("GET /img", s.image)
	mux.HandleFunc("GET /api/receipt", s.get)
	mux.HandleFunc("POST /api/receipt", s.save)
	return mux
}

func (s *LabelServer) image(w http.ResponseWriter, r *http.Request) {
	paths, _, err := s.Dir.Images()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	p, ok := paths[r.URL.Query().Get("id")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, p)
}

func centsStr(c *int64) string {
	if c == nil {
		return ""
	}
	return formatCents(*c)
}

func (s *LabelServer) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ids, err := s.Dir.Images()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	truth, err := s.Dir.LoadTruth()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if len(ids) == 0 {
		http.Error(w, "no images in "+s.Dir.Receipts(), 404)
		return
	}
	id := r.URL.Query().Get("id")
	idx := slices.Index(ids, id)
	if idx < 0 {
		idx, id = 0, ids[0]
		for i, x := range ids { // start at the first unlabelled receipt
			if _, ok := truth[x]; !ok {
				idx, id = i, x
				break
			}
		}
	}

	p := labelPayload{ID: id, Index: idx, Count: len(ids), Labelled: len(truth)}
	if idx > 0 {
		p.Prev = ids[idx-1]
	}
	if idx < len(ids)-1 {
		p.Next = ids[idx+1]
	}
	for i := 1; i <= len(ids); i++ {
		x := ids[(idx+i)%len(ids)]
		if _, ok := truth[x]; !ok && x != id {
			p.NextTodo = x
			break
		}
	}
	shopSet := map[string]bool{}
	var lastShop string
	for _, t := range truth {
		shopSet[t.Shop] = true
	}
	for i := idx - 1; i >= 0; i-- {
		if t, ok := truth[ids[i]]; ok {
			lastShop = t.Shop
			break
		}
	}
	for sh := range shopSet {
		p.Shops = append(p.Shops, sh)
	}
	slices.Sort(p.Shops)
	for _, x := range ids {
		if _, has, _ := s.Dir.LoadLines(x); has {
			p.WithLines++
		}
	}

	// Parse from the first engine with cached OCR, for prefill and context.
	var parsed *receipt.Result
	for _, eng := range s.Engines {
		if c, ok, _ := s.Dir.LoadCached(eng, id); ok && c.Error == "" {
			res := receipt.Parse(c.Result, receipt.Options{Location: s.Loc, Now: time.Now()})
			parsed = &res
			p.Form.Source = eng
			break
		}
	}
	if parsed != nil {
		p.Reasons = parsed.Reasons
		for _, row := range parsed.Rows {
			p.Rows = append(p.Rows, row.Raw)
		}
	}

	if t, ok := truth[id]; ok {
		p.Saved = true
		p.Form = labelForm{Shop: t.Shop, Date: t.Date, Total: formatCents(t.TotalCents), Items: t.Items, Notes: t.Notes, Source: p.Form.Source}
		if ls, has, _ := s.Dir.LoadLines(id); has {
			p.Form.Lines = []labelLine{}
			for _, l := range ls {
				up := ""
				if l.UnitPriceCents != 0 {
					up = formatCents(l.UnitPriceCents)
				}
				p.Form.Lines = append(p.Form.Lines, labelLine{Text: l.Text, Kind: l.Kind, Qty: l.Qty, Unit: l.Unit,
					UnitPrice: up, Total: formatCents(l.TotalCents)})
			}
		}
	} else {
		// Prefill: folder name ("ah/001" → "ah") or the previous receipt's shop.
		if dir := path.Dir(id); dir != "." {
			p.Form.Shop = dir
		} else {
			p.Form.Shop = lastShop
		}
		if parsed != nil {
			if parsed.PurchasedAt != nil {
				p.Form.Date = parsed.PurchasedAt.In(s.Loc).Format(time.DateOnly)
			}
			p.Form.Total = centsStr(parsed.TotalCents)
			p.Form.Lines = []labelLine{}
			for _, l := range parsed.Lines {
				if l.Kind == receipt.KindItem {
					p.Form.Items++
				}
				p.Form.Lines = append(p.Form.Lines, labelLine{Text: l.RawText, Kind: string(l.Kind), Qty: l.Qty,
					Unit: l.Unit, UnitPrice: centsStr(l.UnitPriceCents), Total: formatCents(l.LineTotalCents)})
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

func (s *LabelServer) save(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.URL.Query().Get("id")
	paths, _, err := s.Dir.Images()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, ok := paths[id]; !ok {
		http.Error(w, "unknown receipt "+id, 404)
		return
	}
	var f labelForm
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	bad := func(msg string, a ...any) { http.Error(w, fmt.Sprintf(msg, a...), 400) }

	t := Truth{ID: id, Shop: strings.TrimSpace(f.Shop), Date: strings.TrimSpace(f.Date), Items: f.Items,
		Notes: strings.ReplaceAll(strings.TrimSpace(f.Notes), "\n", " ")}
	if t.Shop == "" {
		bad("shop is required")
		return
	}
	if _, err := time.Parse(time.DateOnly, t.Date); err != nil {
		bad("date must be YYYY-MM-DD")
		return
	}
	if t.TotalCents, err = ParseMoney(f.Total); err != nil {
		bad("total: %v", err)
		return
	}

	var lines []TruthLine
	if f.Lines != nil {
		lines = []TruthLine{}
		for i, l := range f.Lines {
			if strings.TrimSpace(l.Text) == "" && strings.TrimSpace(l.Total) == "" {
				continue
			}
			total, err := ParseMoney(l.Total)
			if err != nil {
				bad("line %d total: %v", i+1, err)
				return
			}
			tl := TruthLine{Text: strings.TrimSpace(l.Text), Kind: l.Kind, Qty: l.Qty, Unit: l.Unit, TotalCents: total}
			if tl.Qty == 0 {
				tl.Qty = 1
			}
			if tl.Unit == "" {
				tl.Unit = "pc"
			}
			if !slices.Contains([]string{"item", "discount", "deposit", "bag"}, tl.Kind) {
				bad("line %d: kind %q", i+1, tl.Kind)
				return
			}
			if strings.TrimSpace(l.UnitPrice) != "" {
				if tl.UnitPriceCents, err = ParseMoney(l.UnitPrice); err != nil {
					bad("line %d unit price: %v", i+1, err)
					return
				}
			}
			lines = append(lines, tl)
		}
		var sum int64
		items := 0
		for _, l := range lines {
			sum += l.TotalCents
			if l.Kind == "item" {
				items++
			}
		}
		if sum != t.TotalCents {
			bad("lines add up to %s but the total is %s; fix a line or the total", formatCents(sum), formatCents(t.TotalCents))
			return
		}
		t.Items = items
	}

	truth, err := s.Dir.LoadTruth()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	truth[id] = t
	if err := s.Dir.SaveTruth(truth); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.Dir.SaveLines(id, lines); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	log.Printf("saved %s: %s %s %s (%d items, lines=%v)", id, t.Shop, t.Date, formatCents(t.TotalCents), t.Items, lines != nil)
	w.WriteHeader(http.StatusNoContent)
}
