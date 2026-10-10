// Package openfoodfacts looks up products on Open Food Facts by barcode.
package openfoodfacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("not on open food facts")

var eanRe = regexp.MustCompile(`^\d{8}$|^\d{12,14}$`)

// ValidBarcode reports whether s looks like an EAN-8/UPC-A/EAN-13/GTIN-14.
func ValidBarcode(s string) bool { return eanRe.MatchString(s) }

// Product is the subset of OFF data Pantry uses.
type Product struct {
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	Brand       string     `json:"brand,omitempty"`
	NetQuantity *float64   `json:"net_quantity,omitempty"`
	NetUnit     string     `json:"net_unit,omitempty"` // g | ml
	ImageURL    string     `json:"image_url,omitempty"`
	Nutrition   *Nutrition `json:"nutrition,omitempty"`
}

type Nutrition struct {
	Per          string   `json:"per"` // 100g | 100ml
	EnergyKcal   *float64 `json:"energy_kcal,omitempty"`
	Fat          *float64 `json:"fat,omitempty"`
	SaturatedFat *float64 `json:"saturated_fat,omitempty"`
	Carbs        *float64 `json:"carbs,omitempty"`
	Sugars       *float64 `json:"sugars,omitempty"`
	Fibre        *float64 `json:"fibre,omitempty"`
	Protein      *float64 `json:"protein,omitempty"`
	Salt         *float64 `json:"salt,omitempty"`
}

type Client struct {
	base      string
	userAgent string
	http      *http.Client

	mu       sync.Mutex
	negative map[string]time.Time // barcode → when OFF said "not found"
}

func New(userAgent string) *Client {
	return &Client{
		base:      "https://world.openfoodfacts.org",
		userAgent: userAgent,
		http:      &http.Client{Timeout: 10 * time.Second},
		negative:  map[string]time.Time{},
	}
}

// WithBaseURL points the client at another server (tests).
func (c *Client) WithBaseURL(u string) *Client { c.base = strings.TrimRight(u, "/"); return c }

const negativeTTL = 24 * time.Hour

const fields = "code,product_name,product_name_nl,product_name_en,brands,product_quantity,product_quantity_unit,quantity,nutriments,nutrition_data_per,image_front_url"

// Lookup fetches a product by barcode.
func (c *Client) Lookup(ctx context.Context, barcode string) (Product, error) {
	if !ValidBarcode(barcode) {
		return Product{}, fmt.Errorf("invalid barcode %q", barcode)
	}
	c.mu.Lock()
	if t, ok := c.negative[barcode]; ok && time.Since(t) < negativeTTL {
		c.mu.Unlock()
		return Product{}, ErrNotFound
	}
	c.mu.Unlock()

	u := fmt.Sprintf("%s/api/v2/product/%s.json?fields=%s", c.base, url.PathEscape(barcode), fields)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Product{}, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return Product{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		c.markMissing(barcode)
		return Product{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return Product{}, fmt.Errorf("open food facts: %s", resp.Status)
	}

	var raw struct {
		Status  int        `json:"status"`
		Product rawProduct `json:"product"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&raw); err != nil {
		return Product{}, fmt.Errorf("open food facts: %w", err)
	}
	if raw.Status != 1 {
		c.markMissing(barcode)
		return Product{}, ErrNotFound
	}
	p := raw.Product.toProduct(barcode)
	if p.Name == "" {
		c.markMissing(barcode)
		return Product{}, ErrNotFound
	}
	return p, nil
}

func (c *Client) markMissing(barcode string) {
	c.mu.Lock()
	c.negative[barcode] = time.Now()
	c.mu.Unlock()
}

// AllowedImageURL reports whether u is an OFF image URL the server may fetch
// (guards against using image downloads to reach arbitrary hosts).
func AllowedImageURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" {
		return false
	}
	h := p.Hostname()
	return h == "openfoodfacts.org" || strings.HasSuffix(h, ".openfoodfacts.org")
}

// FetchImage downloads an OFF image (max 10 MB).
func (c *Client) FetchImage(ctx context.Context, u string) ([]byte, error) {
	if !AllowedImageURL(u) {
		return nil, fmt.Errorf("image url not allowed: %s", u)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10<<20))
}

type rawProduct struct {
	Code         string               `json:"code"`
	Name         string               `json:"product_name"`
	NameNL       string               `json:"product_name_nl"`
	NameEN       string               `json:"product_name_en"`
	Brands       string               `json:"brands"`
	Qty          flexFloat            `json:"product_quantity"`
	QtyUnit      string               `json:"product_quantity_unit"`
	Quantity     string               `json:"quantity"`
	Nutriments   map[string]flexFloat `json:"nutriments"`
	NutritionPer string               `json:"nutrition_data_per"`
	Image        string               `json:"image_front_url"`
}

func (r rawProduct) toProduct(barcode string) Product {
	p := Product{Code: barcode, ImageURL: r.Image}
	for _, n := range []string{r.NameNL, r.Name, r.NameEN} {
		if n = strings.TrimSpace(n); n != "" {
			p.Name = n
			break
		}
	}
	if b, _, _ := strings.Cut(r.Brands, ","); b != "" {
		p.Brand = strings.TrimSpace(b)
	}

	unit := strings.ToLower(r.QtyUnit)
	if r.Qty.Valid && r.Qty.V > 0 {
		q := r.Qty.V
		switch unit {
		case "g", "":
			unit = "g"
			if strings.Contains(strings.ToLower(r.Quantity), "ml") || strings.Contains(strings.ToLower(r.Quantity), " l") {
				unit = "ml"
			}
		case "ml":
		case "kg":
			q, unit = q*1000, "g"
		case "l":
			q, unit = q*1000, "ml"
		case "cl":
			q, unit = q*10, "ml"
		default:
			unit = ""
		}
		if unit != "" {
			p.NetQuantity, p.NetUnit = &q, unit
		}
	}

	get := func(k string) *float64 {
		if v, ok := r.Nutriments[k+"_100g"]; ok && v.Valid {
			f := v.V
			return &f
		}
		return nil
	}
	n := Nutrition{
		Per:        "100g",
		EnergyKcal: get("energy-kcal"), Fat: get("fat"), SaturatedFat: get("saturated-fat"),
		Carbs: get("carbohydrates"), Sugars: get("sugars"), Fibre: get("fiber"),
		Protein: get("proteins"), Salt: get("salt"),
	}
	if p.NetUnit == "ml" {
		n.Per = "100ml"
	}
	if n.EnergyKcal != nil || n.Fat != nil || n.Carbs != nil || n.Protein != nil {
		p.Nutrition = &n
	}
	return p
}

// flexFloat accepts numbers and numeric strings (OFF uses both).
type flexFloat struct {
	V     float64
	Valid bool
}

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil {
		return nil // ignore junk values rather than failing the whole product
	}
	f.V, f.Valid = v, true
	return nil
}
