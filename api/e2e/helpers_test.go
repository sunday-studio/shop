//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type M = map[string]any

var token string

func num(m M, k string) int64 {
	f, _ := m[k].(float64)
	return int64(f)
}
func flt(m M, k string) float64 { f, _ := m[k].(float64); return f }
func str(m M, k string) string  { s, _ := m[k].(string); return s }
func list(m M, k string) []M {
	var out []M
	a, _ := m[k].([]any)
	for _, x := range a {
		if o, ok := x.(M); ok {
			out = append(out, o)
		}
	}
	return out
}

// raw performs a request with the device token and returns status and body.
func raw(method, path string, body any, authed bool) (int, []byte, http.Header, error) {
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
		ct = "application/json"
	}
	req, err := http.NewRequest(method, baseURL+path, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if authed && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header, nil
}

// obj calls the API and decodes a JSON object, failing the step on any other status.
func obj(t *testing.T, method, path string, body any, want int) M {
	t.Helper()
	code, b, _, err := raw(method, path, body, true)
	check(t, err == nil, "%s %s: %v", method, path, err)
	check(t, code == want, "%s %s: status %d, want %d: %s", method, path, code, want, trunc(b))
	var m M
	if len(bytes.TrimSpace(b)) > 0 {
		check(t, json.Unmarshal(b, &m) == nil, "%s %s: not a JSON object: %s", method, path, trunc(b))
	}
	return m
}

func arr(t *testing.T, method, path string, body any, want int) []M {
	t.Helper()
	code, b, _, err := raw(method, path, body, true)
	check(t, err == nil, "%s %s: %v", method, path, err)
	check(t, code == want, "%s %s: status %d, want %d: %s", method, path, code, want, trunc(b))
	var out []M
	check(t, json.Unmarshal(b, &out) == nil, "%s %s: not a JSON array: %s", method, path, trunc(b))
	return out
}

func status(t *testing.T, method, path string, body any) (int, M) {
	t.Helper()
	code, b, _, err := raw(method, path, body, true)
	check(t, err == nil, "%s %s: %v", method, path, err)
	var m M
	_ = json.Unmarshal(b, &m)
	return code, m
}

func trunc(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func upload(t *testing.T, name string, data []byte, ctype string, fields map[string]string) (int, M) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename=%q`, name))
	h.Set("Content-Type", ctype)
	p, _ := w.CreatePart(h)
	p.Write(data)
	for k, v := range fields {
		w.WriteField(k, v)
	}
	w.Close()
	req, _ := http.NewRequest("POST", baseURL+"/v1/receipts", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpc.Do(req)
	check(t, err == nil, "upload: %v", err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m M
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

// waitReceipt polls until the receipt has left pending/processing.
func waitReceipt(t *testing.T, id int64, limit time.Duration) M {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		r := obj(t, "GET", fmt.Sprintf("/v1/receipts/%d", id), nil, 200)
		switch str(r, "status") {
		case "ok", "needs_review", "failed":
			return r
		}
		check(t, time.Now().Before(deadline), "receipt %d still %q after %s", id, str(r, "status"), limit)
		time.Sleep(2 * time.Second)
	}
}

// itemLines are the lines of a receipt that are products (not discounts, deposits, bags, totals).
func itemLines(r M) []M {
	var out []M
	for _, l := range list(r, "lines") {
		if str(l, "kind") == "item" {
			out = append(out, l)
		}
	}
	return out
}

func sumLines(r M) int64 {
	var s int64
	for _, l := range list(r, "lines") {
		s += num(l, "line_total_cents")
	}
	return s
}

// ---- fake Open Food Facts ------------------------------------------------------

type offServer struct {
	ln           net.Listener
	srv          *http.Server
	containerURL string
	mu           sync.Mutex
	mode         map[string]string // barcode -> found | missing | down
	hits         map[string]int
}

func startFakeOFF() *offServer {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		panic(err)
	}
	f := &offServer{ln: ln, mode: map[string]string{}, hits: map[string]int{}}
	f.containerURL = fmt.Sprintf("http://host.docker.internal:%d", ln.Addr().(*net.TCPAddr).Port)
	f.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimSuffix(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], ".json")
		f.mu.Lock()
		mode := f.mode[code]
		f.hits[code]++
		f.mu.Unlock()
		switch mode {
		case "found":
			fmt.Fprint(w, `{"status":1,"product":{"code":"`+code+`","product_name_nl":"E2E Halfvolle melk","product_name_en":"E2E Semi-skimmed milk","brands":"Arla","product_quantity":"1000","product_quantity_unit":"ml","categories_tags":["en:dairies","en:milks"],"nutriments":{"energy-kcal_100g":46,"fat_100g":1.5},"nutrition_data_per":"100g"}}`)
		case "down":
			http.Error(w, "boom", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	})}
	go f.srv.Serve(ln)
	return f
}

func (f *offServer) set(code, mode string) { f.mu.Lock(); f.mode[code] = mode; f.mu.Unlock() }
func (f *offServer) close()                { f.srv.Close() }

// ean13 completes a 12-digit prefix with its check digit.
func ean13(prefix string) string {
	sum := 0
	for i, c := range prefix {
		d := int(c - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return prefix + fmt.Sprint((10-sum%10)%10)
}

// ---- synthetic receipt photo -------------------------------------------------------

type recLine struct {
	name  string
	cents int
}

func eur(c int) string { return fmt.Sprintf("%d,%02d", c/100, c%100) }

// renderReceipt draws an Albert Heijn style slip as a JPEG with a TrueType font. It is a
// plain picture of text: the OCR service reads it for real (nothing about OCR is mocked).
func renderReceipt(lines []recLine, when time.Time) ([]byte, int) {
	f, err := opentype.Parse(gomonobold.TTF)
	if err != nil {
		panic(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 44, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	const W, H = 1200, 1500
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: face}
	y := 120
	center := func(s string) {
		d.Dot = fixed.P((W-d.MeasureString(s).Round())/2, y)
		d.DrawString(s)
		y += 62
	}
	left := func(s string, right string) {
		d.Dot = fixed.P(180, y)
		d.DrawString(s)
		if right != "" {
			d.Dot = fixed.P(1020-d.MeasureString(right).Round(), y)
			d.DrawString(right)
		}
		y += 62
	}
	center("Albert Heijn")
	center("Teststraat 12 Utrecht")
	center("030-1234567")
	y += 40
	left("AANTAL OMSCHRIJVING", "BEDRAG")
	total := 0
	for _, l := range lines {
		left(l.name, eur(l.cents))
		total += l.cents
	}
	y += 40
	left("TOTAAL", eur(total))
	y += 30
	left("BETAALD MET:", "")
	left("PINNEN", eur(total))
	y += 60
	left(when.Format("15:04")+" "+when.Format("02-01-2006"), "")
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		panic(err)
	}
	return buf.Bytes(), total
}

func ctxBG() context.Context { return context.Background() }

func decodeObj(t *testing.T, b []byte) M {
	t.Helper()
	var m M
	check(t, json.Unmarshal(b, &m) == nil, "not a JSON object: %s", trunc(b))
	return m
}
