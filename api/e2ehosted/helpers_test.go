//go:build e2ehosted

package e2ehosted

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
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type M = map[string]any

func num(m M, k string) int64  { f, _ := m[k].(float64); return int64(f) }
func str(m M, k string) string { s, _ := m[k].(string); return s }
func sub(m M, k string) M      { o, _ := m[k].(M); return o }
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

var httpc = &http.Client{Timeout: 60 * time.Second}

// user is one account on one device, the way the app holds it.
type user struct {
	name  string
	sub   string // Apple "sub"
	token string // device bearer token
	acc   M      // account object from the last sign-in
}

// resp is one HTTP answer.
type resp struct {
	code int
	body []byte
	hdr  http.Header
	m    M
	a    []M // when the body is a JSON array
}

func (r resp) String() string  { return fmt.Sprintf("%d %s", r.code, trunc(r.body)) }
func (r resp) problem() string { return str(r.m, "code") }

func trunc(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func doReq(req *http.Request) (resp, error) {
	rs, err := httpc.Do(req)
	if err != nil {
		return resp{}, err
	}
	defer rs.Body.Close()
	b, _ := io.ReadAll(rs.Body)
	r := resp{code: rs.StatusCode, body: b, hdr: rs.Header}
	if t := bytes.TrimSpace(b); len(t) > 0 && t[0] == '[' {
		_ = json.Unmarshal(b, &r.a)
	} else {
		_ = json.Unmarshal(b, &r.m)
	}
	return r, nil
}

func request(t *testing.T, base, method, path, token string, body any, hdr ...string) resp {
	t.Helper()
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
	req, err := http.NewRequest(method, base+path, rd)
	check(t, err == nil, "%s %s: %v", method, path, err)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	r, err := doReq(req)
	check(t, err == nil, "%s %s: %v", method, path, err)
	return r
}

// api calls the public API with u's token (nil: unauthenticated).
func api(t *testing.T, u *user, method, path string, body any, hdr ...string) resp {
	t.Helper()
	tok := ""
	if u != nil {
		tok = u.token
	}
	return request(t, baseURL, method, path, tok, body, hdr...)
}

// adm calls the admin API (through the sidecar) as operator "e2e".
func adm(t *testing.T, method, path string, body any, hdr ...string) resp {
	t.Helper()
	return request(t, adminURL, method, path, adminTok, body, append([]string{"X-Admin-Operator", "e2e"}, hdr...)...)
}

// want fails the step unless r has status code (and, when given, problem code).
func want(t *testing.T, r resp, code int, problem ...string) resp {
	t.Helper()
	ok := r.code == code
	if len(problem) > 0 {
		ok = ok && r.problem() == problem[0]
	}
	check(t, ok, "got %s, want %d %v", r, code, problem)
	return r
}

// upload posts a receipt photo the way the app does.
func upload(t *testing.T, u *user, name string, data []byte) resp {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename=%q`, name))
	h.Set("Content-Type", "image/jpeg")
	p, _ := w.CreatePart(h)
	p.Write(data)
	w.Close()
	req, _ := http.NewRequest("POST", baseURL+"/v1/receipts", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+u.token)
	r, err := doReq(req)
	check(t, err == nil, "upload: %v", err)
	return r
}

func eventually(t *testing.T, limit time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !fn() {
		check(t, time.Now().Before(deadline), "timed out after %s waiting for %s", limit, what)
		time.Sleep(500 * time.Millisecond)
	}
}

// waitReceipt polls until the receipt has left pending/processing.
func waitReceipt(t *testing.T, u *user, id int64, limit time.Duration) M {
	t.Helper()
	var last M
	eventually(t, limit, fmt.Sprintf("receipt %d to be read", id), func() bool {
		r := api(t, u, "GET", fmt.Sprintf("/v1/receipts/%d", id), nil)
		last = r.m
		return r.code == 200 && (str(r.m, "status") == "ok" || str(r.m, "status") == "needs_review" || str(r.m, "status") == "failed")
	})
	return last
}

// ---- tools sidecar -------------------------------------------------------------

// toolsPy runs a python snippet in the sidecar (read-only view of /data) and returns stdout.
func toolsPy(t *testing.T, code string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := compose(ctx, "", "exec", "-T", "tools", "python3", "-c", code)
	check(t, err == nil, "tools exec: %v: %s", err, out)
	return strings.TrimSpace(out)
}

// householdFiles lists the files under /data/households/<id>/ (relative paths); nil when the folder is gone.
func householdFiles(t *testing.T, hid string) []string {
	t.Helper()
	out := toolsPy(t, fmt.Sprintf(`
import os,json
r='/data/households/%s'
f=[]
if os.path.isdir(r):
    for d,_,fs in os.walk(r):
        for n in fs: f.append(os.path.relpath(os.path.join(d,n),r))
    print(json.dumps(sorted(f)) if f else '[]')
else:
    print('null')`, hid))
	var files []string
	if out == "null" {
		return nil
	}
	check(t, json.Unmarshal([]byte(out), &files) == nil, "bad listing: %s", out)
	return files
}

func householdDirExists(t *testing.T, hid string) bool {
	t.Helper()
	return toolsPy(t, fmt.Sprintf("import os;print(os.path.isdir('/data/households/%s'))", hid)) == "True"
}

// ---- clock ---------------------------------------------------------------------

// advance moves the server's clock forward (dev route, fake Apple only) and returns its new time.
func advance(t *testing.T, d time.Duration) time.Time {
	t.Helper()
	r := adm(t, "POST", "/admin/v1/dev/advance-clock", M{"seconds": d.Seconds()})
	want(t, r, 200)
	ts, err := time.Parse(time.RFC3339, str(r.m, "now"))
	check(t, err == nil, "advance-clock answer: %s", r)
	return ts
}

func serverNow(t *testing.T) time.Time {
	t.Helper()
	r := adm(t, "POST", "/admin/v1/dev/advance-clock", M{})
	want(t, r, 200)
	ts, _ := time.Parse(time.RFC3339, str(r.m, "now"))
	return ts
}

// ---- synthetic receipt photo -------------------------------------------------------

type recLine struct {
	name  string
	cents int
}

func eur(c int) string { return fmt.Sprintf("%d,%02d", c/100, c%100) }

// renderReceipt draws an Albert Heijn style slip as a JPEG with a TrueType font. It is a plain
// picture of text: the OCR service reads it for real (nothing about OCR is mocked). Same renderer
// as api/e2e.
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

var recipes = [][]recLine{
	{{"AH HALFVOL MELK", 129}, {"AH VOLKORENBROOD", 215}, {"BANANEN", 189}, {"GOUDA JONG BEL", 379}, {"AH GRIEKSE YOGHURT", 119}},
	{{"AH KIPFILET", 549}, {"AH ROOMBOTER", 289}, {"TOMATEN TROS", 229}, {"EIEREN 10 STUKS", 259}},
	{{"AH PASTA PENNE", 99}, {"AH PASTASAUS", 179}, {"PARMEZAANSE KAAS", 349}, {"APPELSAP", 199}},
	{{"AH KOFFIE", 599}, {"MAGERE MELK", 109}, {"ROGGEBROOD", 189}, {"KOMKOMMER", 79}},
	{{"AH BASILICUM", 129}, {"MOZZARELLA", 99}, {"OLIJFOLIE", 599}, {"CITROENEN", 149}},
	{{"AH HAVERMOUT", 139}, {"BLAUWE BESSEN", 299}, {"SINAASAPPELS", 249}, {"KAAS GEITEN", 319}},
	{{"AH RIJST", 189}, {"KOOLRAAB", 119}, {"AH SOEP", 159}, {"BOTERHAMWORST", 219}},
}

// synth renders recipe i as a receipt photo dated `when`.
func synth(i int, when time.Time) ([]byte, int, []recLine) {
	l := recipes[i%len(recipes)]
	img, total := renderReceipt(l, when.Add(time.Duration(i)*time.Minute))
	return img, total, l
}

// findPhotos returns private receipt photos (git-ignored) when present: same places as api/e2e.
func findPhotos(root string) []string {
	dirs := []string{filepath.Join(root, "spike/data"), filepath.Join(root, "spike/receipts")}
	if d := os.Getenv("E2E_PHOTOS"); d != "" {
		dirs = []string{d}
	}
	var out []string
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			n := strings.ToLower(e.Name())
			if !e.IsDir() && (strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg")) {
				out = append(out, filepath.Join(d, e.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}

func newGet(url string) (*http.Request, error) { return http.NewRequest("GET", url, nil) }

func mustReq(method, url, body string, hdr ...string) *http.Request {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return req
}

func ctxMin(n int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(n)*time.Minute)
}

func receiptIDsSorted(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// rows are the elements of a list answer: a bare JSON array or a page {"items": [...]}.
func rows(r resp) []M {
	if r.a != nil {
		return r.a
	}
	return list(r.m, "items")
}
