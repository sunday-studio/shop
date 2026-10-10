package autoprod

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"shop/internal/match"
	"shop/internal/openfoodfacts"
)

// ean13 completes 12 digits with a valid EAN-13 check digit.
func ean13(p12 string) string {
	sum := 0
	for i, c := range p12 {
		d := int(c - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return p12 + fmt.Sprint((10-sum%10)%10)
}

type fakeOFF struct {
	srv  *httptest.Server
	mu   sync.Mutex
	mode map[string]string // code -> found | nocat | missing | slow | down
	hits map[string]int
}

func newFakeOFF(t *testing.T) *fakeOFF {
	f := &fakeOFF{mode: map[string]string{}, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimSuffix(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], ".json")
		f.mu.Lock()
		mode := f.mode[code]
		f.hits[code]++
		f.mu.Unlock()
		switch mode {
		case "found":
			fmt.Fprint(w, `{"status":1,"product":{"code":"`+code+`","product_name_nl":"Halfvolle melk","product_name_en":"Semi-skimmed milk","brands":"Arla","product_quantity":"1000","product_quantity_unit":"ml","categories_tags":["en:dairies","en:milks"],"nutriments":{"energy-kcal_100g":46,"fat_100g":1.5},"nutrition_data_per":"100g"}}`)
		case "nocat":
			fmt.Fprint(w, `{"status":1,"product":{"code":"`+code+`","product_name":"Zonnebloempitten","brands":"Xyz","categories_tags":[]}}`)
		case "merge":
			fmt.Fprint(w, `{"status":1,"product":{"code":"`+code+`","product_name":"AH Bio Melk","brands":"AH","categories_tags":["en:dairies"]}}`)
		case "slow":
			time.Sleep(600 * time.Millisecond)
			fmt.Fprint(w, `{"status":1,"product":{"code":"`+code+`","product_name":"Late"}}`)
		case "down":
			http.Error(w, "boom", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOFF) set(code, mode string) { f.mu.Lock(); f.mode[code] = mode; f.mu.Unlock() }

func offService(t *testing.T, conn *sql.DB, f *fakeOFF) *Service {
	s := New(conn, match.NewMatcher(conn), nil, openfoodfacts.New("test").WithBaseURL(f.srv.URL), nil, slog.New(slog.DiscardHandler), true)
	s.OFFTimeout = 250 * time.Millisecond
	s.now = fixedClock
	return s
}

func TestBarcodeCreateOutcomes(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	f := newFakeOFF(t)
	s := offService(t, conn, f)

	full, nocat, missing, slow, down := ean13("871000000001"), ean13("871000000002"), ean13("871000000003"), ean13("871000000004"), ean13("871000000005")
	f.set(full, "found")
	f.set(nocat, "nocat")
	f.set(slow, "slow")
	f.set(down, "down")

	o, err := s.CreateFromBarcode(ctx, full)
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "created" || !o.CreatedNow || o.OFFStatus != "found" || o.Product.Name != "Halfvolle melk" || o.Product.Brand.String != "Arla" ||
		o.Product.NeedsReview != 0 || o.Product.CreatedVia != "barcode" || o.Product.CategorySource.String != "off" ||
		o.Product.NameEn.String != "Semi-skimmed milk" || o.Product.NetQuantity.Float64 != 1000 || o.Product.NetUnit.String != "ml" {
		t.Fatalf("found-full = %+v", o)
	}
	var catName string
	_ = conn.QueryRow(`SELECT name FROM categories WHERE id = ?`, o.Product.CategoryID.Int64).Scan(&catName)
	if catName != "Dairy" {
		t.Errorf("category = %q; want Dairy", catName)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM nutrition WHERE product_id = ?`, o.Product.ID); n != 1 {
		t.Error("OFF nutrition was not stored")
	}

	// a repeat is idempotent: known, not created now, no second OFF call
	hits := f.hits[full]
	again, err := s.CreateFromBarcode(ctx, full)
	if err != nil || again.Status != "known" || again.CreatedNow || again.Product.ID != o.Product.ID || again.MergeSuggestion != nil {
		t.Fatalf("repeat = %+v, %v", again, err)
	}
	if f.hits[full] != hits {
		t.Error("a known barcode must not call Open Food Facts again")
	}

	o, _ = s.CreateFromBarcode(ctx, nocat)
	if o.Status != "created" || o.Product.NeedsReview != 1 || o.Product.ReviewReason.String != ReasonNoCategory {
		t.Errorf("found-no-category = status %s reason %q review %d", o.Status, o.Product.ReviewReason.String, o.Product.NeedsReview)
	}

	o, _ = s.CreateFromBarcode(ctx, missing)
	if o.Status != "created_minimal" || o.OFFStatus != "not_found" || o.Product.ReviewReason.String != ReasonBarcodeMinimal ||
		o.Product.Name != "Unknown product "+missing[len(missing)-4:] {
		t.Errorf("not-found = %+v", o)
	}

	for _, c := range []string{slow, down} { // timeout and 503
		o, err = s.CreateFromBarcode(ctx, c)
		if err != nil || o.Status != "created_minimal" || o.OFFStatus != "unavailable" || o.Product.ReviewReason.String != ReasonOFFUnavailable {
			t.Errorf("unavailable (%s) = %+v, %v", c, o, err)
		}
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM jobs WHERE kind = 'off_refresh'`); n != 2 {
		t.Errorf("%d off_refresh jobs; want 2", n)
	}

	if _, err := s.CreateFromBarcode(ctx, "12ab"); err == nil {
		t.Error("bad code accepted")
	}
}

func TestOFFRefreshFillsOnlyEmptyFields(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	f := newFakeOFF(t)
	s := offService(t, conn, f)
	code := ean13("871000000011")
	f.set(code, "down")
	o, _ := s.CreateFromBarcode(ctx, code)
	if o.OFFStatus != "unavailable" {
		t.Fatalf("setup: %+v", o)
	}
	// the server is still down: nothing changes
	p, st, err := s.Refresh(ctx, o.Product.ID)
	if err != nil || st != "unavailable" || p.ReviewReason.String != ReasonOFFUnavailable {
		t.Fatalf("refresh while down = %s %q %v", st, p.ReviewReason.String, err)
	}
	// the user typed a name meanwhile; OFF comes back
	if _, err := conn.Exec(`UPDATE products SET name = 'Mijn melk' WHERE id = ?`, o.Product.ID); err != nil {
		t.Fatal(err)
	}
	f.set(code, "found")
	p, st, err = s.Refresh(ctx, o.Product.ID)
	if err != nil || st != "found" {
		t.Fatalf("refresh = %s %v", st, err)
	}
	if p.Name != "Mijn melk" {
		t.Errorf("name = %q; a name the user typed must survive", p.Name)
	}
	if p.Brand.String != "Arla" || p.CategoryID.Int64 == 0 || p.NeedsReview != 0 {
		t.Errorf("empty fields not filled / queue not cleared: %+v", p)
	}
	// no barcode -> invalid
	if _, err := conn.Exec(`INSERT INTO products (name, created_at, updated_at) VALUES ('Plain', 't', 't')`); err != nil {
		t.Fatal(err)
	}
	var plain int64
	_ = conn.QueryRow(`SELECT id FROM products WHERE name='Plain'`).Scan(&plain)
	if _, _, err := s.Refresh(ctx, plain); err == nil {
		t.Error("refresh without barcode must fail")
	}
}

func TestBarcodeProductVsReceiptProduct(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	f := newFakeOFF(t)
	s := offService(t, conn, f)
	processAll(t, s, loadReceipts(t, conn), "receipt")
	recProduct := idByName(t, conn, "AH Bio Melk")
	linesBefore := count(t, conn, `SELECT COUNT(*) FROM receipt_lines WHERE product_id = ?`, recProduct)

	code := ean13("871000000021")
	f.set(code, "merge")
	o, err := s.CreateFromBarcode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if o.MergeSuggestion == nil || o.MergeSuggestion.ProductID != recProduct || o.Product.ReviewReason.String != ReasonBarcodeName ||
		o.Product.SuggestedMergeID.Int64 != recProduct {
		t.Fatalf("expected a suggestion for product %d: %+v / %+v", recProduct, o.MergeSuggestion, o.Product)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products WHERE id = ? AND deleted_at IS NULL`, recProduct); n != 1 {
		t.Fatal("nothing may be merged without a tap")
	}

	// accepting the suggestion keeps the BARCODE product and moves the receipt product into it
	r, err := s.Review(ctx, o.Product.ID, ReviewInput{Action: "merge"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Product.ID != o.Product.ID || r.Product.Barcode.String != code || r.Merge.MergedAway.ID != recProduct {
		t.Fatalf("survivor %d barcode %q merged_away %+v; want the barcode product %d", r.Product.ID, r.Product.Barcode.String, r.Merge.MergedAway, o.Product.ID)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM receipt_lines WHERE product_id = ?`, o.Product.ID); n != linesBefore {
		t.Errorf("%d lines on the barcode product; want the %d from the receipt product", n, linesBefore)
	}
	// undo restores the queue card of the barcode product with its review state
	u, err := s.Undo(ctx, r.Merge.MergeID)
	if err != nil || u.Product.ID != recProduct {
		t.Fatalf("undo = %+v, %v", u, err)
	}
}

func TestBackfillDryRunThenRealIsIdempotent(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	loadReceipts(t, conn)

	if _, err := newService(t, conn, false).StartBackfill(ctx, true); err != ErrDisabled {
		t.Errorf("backfill with the flag off err = %v; want disabled", err)
	}
	if _, err := s.StartBackfill(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartBackfill(ctx, false); err == nil {
		t.Error("a second backfill while one is running must be refused")
	}
	if err := s.RunBackfill(ctx, true); err != nil {
		t.Fatal(err)
	}
	dry, _ := s.GetBackfill(ctx)
	if dry.State != "done" || !dry.DryRun || dry.Created == 0 || dry.ReceiptsDone != dry.ReceiptsTotal {
		t.Fatalf("dry run = %+v", dry)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products`); n != 0 {
		t.Fatalf("a dry run created %d products", n)
	}

	if _, err := s.StartBackfill(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RunBackfill(ctx, false); err != nil {
		t.Fatal(err)
	}
	real, _ := s.GetBackfill(ctx)
	if real.Created != dry.Created || real.Linked != dry.Linked {
		t.Errorf("dry run said created %d linked %d; the real run did created %d linked %d", dry.Created, dry.Linked, real.Created, real.Linked)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products`); n != real.Created {
		t.Errorf("%d products, run says %d", n, real.Created)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products WHERE created_via != 'backfill'`); n != 0 {
		t.Error("backfill products must be created_via=backfill")
	}
	if _, err := s.StartBackfill(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RunBackfill(ctx, false); err != nil {
		t.Fatal(err)
	}
	second, _ := s.GetBackfill(ctx)
	if second.Created != 0 || second.Linked != 0 {
		t.Errorf("second backfill = %+v; want nothing new", second)
	}
	_ = json.Marshal
}
