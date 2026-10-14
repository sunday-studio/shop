package httpapi_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shop/internal/apitypes"
	"shop/internal/auth"
	"shop/internal/hosted/hostedworld"
)

func imageForm(t testing.TB, seed uint8) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "p.png")
	_, _ = fw.Write(pngBytes(seed))
	_ = mw.Close()
	return body.Bytes(), mw.FormDataContentType()
}

// doTok sends a request as the device that holds token.
func (e *env) doTok(token, method, path string, hdr ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) putImage(id int64, seed uint8) apitypes.Product {
	e.t.Helper()
	body, ct := imageForm(e.t, seed)
	req := httptest.NewRequest("PUT", fmt.Sprintf("/v1/products/%d/image", id), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		e.t.Fatalf("put image: %d %s", rec.Code, rec.Body)
	}
	return decodeAs[apitypes.Product](e.t, rec)
}

func TestProductImageIsTheSameForEveryDeviceAndCarriesAVersion(t *testing.T) {
	e := newEnv(t)
	devB, err := auth.Create(t.Context(), e.q, "second phone")
	if err != nil {
		t.Fatal(err)
	}
	milk := decodeAs[apitypes.Product](t, e.do("POST", "/v1/products", map[string]any{"name": "Melk"}))
	if milk.ImageUrl != nil || milk.ImageVersion != nil {
		t.Fatalf("image fields without a picture: %v %v", milk.ImageUrl, milk.ImageVersion)
	}
	// no picture: 404, and a 404 is never cached
	if rec := e.doTok(devB, "GET", fmt.Sprintf("/v1/products/%d/image", milk.Id)); rec.Code != 404 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("no picture: %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}

	// device A adds the picture
	put := e.putImage(milk.Id, 40)
	if put.ImageUrl == nil || put.ImageVersion == nil || !put.HasImage {
		t.Fatalf("PUT answer lacks the image fields: %+v", put)
	}
	v1 := *put.ImageVersion
	if want := fmt.Sprintf("/v1/products/%d/image?v=%s", milk.Id, v1); *put.ImageUrl != want {
		t.Errorf("image_url = %q, want %q", *put.ImageUrl, want)
	}

	// every payload that carries a product carries the same url and version
	same := func(where string, p apitypes.Product) {
		t.Helper()
		if p.ImageUrl == nil || *p.ImageUrl != *put.ImageUrl || p.ImageVersion == nil || *p.ImageVersion != v1 || !p.HasImage {
			t.Errorf("%s: image_url %v version %v has_image %v; want %s", where, p.ImageUrl, p.ImageVersion, p.HasImage, *put.ImageUrl)
		}
	}
	same("detail", decodeAs[apitypes.Product](t, e.do("GET", fmt.Sprintf("/v1/products/%d", milk.Id), nil)))
	list := decodeAs[[]apitypes.Product](t, e.do("GET", "/v1/products", nil))
	if len(list) != 1 {
		t.Fatalf("list = %d", len(list))
	}
	same("list", list[0])
	// another device lists the same
	req := decodeAs[[]apitypes.Product](t, rewrap(e.doTok(devB, "GET", "/v1/products")))
	same("list on device B", req[0])
	var sr struct {
		Products []apitypes.Product `json:"products"`
	}
	if rec := e.do("GET", "/v1/search?q=Melk", nil); rec.Code != 200 {
		t.Fatalf("search: %d %s", rec.Code, rec.Body)
	} else {
		sr = decodeAs[struct {
			Products []apitypes.Product `json:"products"`
		}](t, rec)
	}
	if len(sr.Products) == 0 {
		t.Fatal("search found no product")
	}
	same("search", sr.Products[0])

	// device B downloads it, and it is the very picture A sent
	a := e.doTok(e.token, "GET", *put.ImageUrl)
	b := e.doTok(devB, "GET", *put.ImageUrl)
	if a.Code != 200 || b.Code != 200 || b.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) || b.Body.Len() == 0 {
		t.Fatalf("device A %d, device B %d (%s, %d bytes)", a.Code, b.Code, b.Header().Get("Content-Type"), b.Body.Len())
	}
	if got := b.Header().Get("ETag"); got != `"`+v1+`"` {
		t.Errorf("ETag = %q", got)
	}
	if cc := b.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "private") {
		t.Errorf("versioned url: Cache-Control %q", cc)
	}
	// without the version a cache must revalidate, and a matching ETag costs nothing
	bare := e.doTok(devB, "GET", fmt.Sprintf("/v1/products/%d/image", milk.Id))
	if bare.Code != 200 || bare.Header().Get("Cache-Control") != "private, no-cache" {
		t.Errorf("bare url: %d, Cache-Control %q", bare.Code, bare.Header().Get("Cache-Control"))
	}
	if rec := e.doTok(devB, "GET", fmt.Sprintf("/v1/products/%d/image", milk.Id), "If-None-Match", `"`+v1+`"`); rec.Code != 304 {
		t.Errorf("If-None-Match: %d", rec.Code)
	}
	// the image needs a device token, like everything else
	if rec := e.doTok("", "GET", *put.ImageUrl); rec.Code != 401 {
		t.Errorf("no token: %d", rec.Code)
	}

	// A changes the picture: the version and the url change, device B refreshes, the old version is not served as a fresh immutable copy
	put2 := e.putImage(milk.Id, 200)
	v2 := *put2.ImageVersion
	if v2 == v1 || *put2.ImageUrl == *put.ImageUrl {
		t.Fatalf("a new picture kept version %s", v1)
	}
	if rec := e.doTok(devB, "GET", fmt.Sprintf("/v1/products/%d/image", milk.Id), "If-None-Match", `"`+v1+`"`); rec.Code != 200 || rec.Header().Get("ETag") != `"`+v2+`"` {
		t.Errorf("revalidate after a change: %d, ETag %q", rec.Code, rec.Header().Get("ETag"))
	}
	if rec := e.doTok(devB, "GET", *put.ImageUrl); rec.Code != 200 || strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("stale versioned url: %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	// the same picture again keeps the version (nothing for clients to refetch)
	if again := e.putImage(milk.Id, 200); *again.ImageVersion != v2 {
		t.Errorf("same picture, version %s -> %s", v2, *again.ImageVersion)
	}
}

// rewrap lets decodeAs read a recorder produced by doTok.
func rewrap(r *httptest.ResponseRecorder) *httptest.ResponseRecorder { return r }

// Two products can hold the same picture (the same Open Food Facts photo, a merge). Replacing it on one of
// them must not delete the file under the other.
func TestSharedProductImageSurvivesAReplacement(t *testing.T) {
	e := newEnv(t)
	a := decodeAs[apitypes.Product](t, e.do("POST", "/v1/products", map[string]any{"name": "Cola 1L"}))
	b := decodeAs[apitypes.Product](t, e.do("POST", "/v1/products", map[string]any{"name": "Cola 1,5L"}))
	pa, pb := e.putImage(a.Id, 90), e.putImage(b.Id, 90)
	if *pa.ImageVersion != *pb.ImageVersion {
		t.Fatalf("the same bytes should be the same picture: %s %s", *pa.ImageVersion, *pb.ImageVersion)
	}
	e.putImage(a.Id, 10) // A gets another picture
	if rec := e.do("GET", *pb.ImageUrl, nil); rec.Code != 200 || rec.Body.Len() == 0 {
		t.Fatalf("B lost its picture when A replaced the shared one: %d %s", rec.Code, rec.Body)
	}
	// and when nobody uses a file any more it goes
	e.putImage(b.Id, 10)
	if rec := e.do("GET", *pa.ImageUrl, nil); rec.Code != 200 { // A's old url now serves A's current picture, never a 404
		t.Errorf("A's old url: %d", rec.Code)
	}
}

// The review queue and the barcode answer carry the image fields too.
func TestImageFieldsInReviewQueueAndBarcodeAnswers(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	uploadAndConfirm(e, shop.Id, []string{"01-10-2026 10:00|", "NUNNUNONNTT|1,39", "TOTAAL|1,39"}, 61)
	q := decodeAs[apitypes.ReviewQueue](t, e.do("GET", "/v1/products/review-queue", nil))
	if len(q.Items) == 0 {
		t.Fatal("empty review queue")
	}
	id := q.Items[0].Product.Id
	if q.Items[0].Product.ImageUrl != nil {
		t.Error("image_url before any picture")
	}
	put := e.putImage(id, 33)
	q = decodeAs[apitypes.ReviewQueue](t, e.do("GET", "/v1/products/review-queue", nil))
	var found bool
	for _, it := range q.Items {
		if it.Product.Id == id {
			found = true
			if it.Product.ImageUrl == nil || *it.Product.ImageUrl != *put.ImageUrl || it.Product.ImageVersion == nil || *it.Product.ImageVersion != *put.ImageVersion {
				t.Errorf("review queue image fields: %v %v", it.Product.ImageUrl, it.Product.ImageVersion)
			}
		}
	}
	if !found {
		t.Fatal("product left the queue")
	}

	// a barcode scan of a known product answers with the picture
	bc := "8710400000024"
	cr := decodeAs[apitypes.Product](t, e.do("POST", "/v1/products", map[string]any{"name": "Yoghurt", "barcode": bc}))
	pp := e.putImage(cr.Id, 77)
	rec := e.do("POST", "/v1/barcodes/"+bc+"/product", nil)
	if rec.Code != 200 {
		t.Fatalf("barcode create on a known code: %d %s", rec.Code, rec.Body)
	}
	got := decodeAs[apitypes.BarcodeCreateResult](t, rec)
	if got.Product.ImageUrl == nil || *got.Product.ImageUrl != *pp.ImageUrl || got.Product.ImageVersion == nil {
		t.Errorf("barcode answer image fields: %v %v", got.Product.ImageUrl, got.Product.ImageVersion)
	}
}

// Hosted: two members of one household see each other's pictures, another household cannot read them.
func TestIsolationHostedMembersShareProductImages(t *testing.T) {
	hw := hostedworld.New(t, hostedworld.NoRates)
	a := hw.SignIn("img-a", "Ama")
	hw.Subscribe(a, "otx-img-a")
	b := hw.SignIn("img-b", "Bob")
	hw.Subscribe(b, "otx-img-b")
	m := hw.SignIn("img-m", "Mia")
	a2 := hw.SignInDevice("img-a", "Ama", "Ama's iPad") // second device of the owner

	inv := hw.Do("POST", "/v1/household/invites", a.Token, nil)
	if r := hw.Do("POST", "/v1/household/invites/"+inv.JSON()["code"].(string)+"/accept", m.Token, nil); r.Status() != 200 {
		t.Fatal(r.Body)
	}
	// B gets products too, so ids overlap and a leak would show a picture
	hw.Do("POST", "/v1/products", b.Token, map[string]any{"name": "B product"})

	p := hw.Do("POST", "/v1/products", a.Token, map[string]any{"name": "Melk"})
	if p.Status() != 201 {
		t.Fatal(p.Body)
	}
	id := int64(p.JSON()["id"].(float64))
	body, ct := imageForm(t, 55)
	put := hw.Do("PUT", fmt.Sprintf("/v1/products/%d/image", id), m.Token, body, "Content-Type", ct) // the member adds it
	if put.Status() != 200 {
		t.Fatalf("member PUT image: %d %s", put.Status(), put.Body)
	}
	url, _ := put.JSON()["image_url"].(string)
	ver, _ := put.JSON()["image_version"].(string)
	if url == "" || ver == "" {
		t.Fatalf("PUT answer: %s", put.Body)
	}
	var first []byte
	for name, tok := range map[string]string{"owner phone": a.Token, "owner iPad": a2.Token, "member": m.Token} {
		r := hw.Do("GET", url, tok, nil)
		if r.Status() != 200 || r.Header().Get("Content-Type") != "image/jpeg" || r.Header().Get("ETag") != `"`+ver+`"` {
			t.Errorf("%s: %d %s etag %q", name, r.Status(), r.Header().Get("Content-Type"), r.Header().Get("ETag"))
			continue
		}
		if first == nil {
			first = r.Body.Bytes()
		} else if !bytes.Equal(first, r.Body.Bytes()) {
			t.Errorf("%s got another picture", name)
		}
		// the list of that device carries the same url
		if l := hw.Do("GET", "/v1/products", tok, nil).Body.String(); !strings.Contains(l, `"image_version":"`+ver+`"`) {
			t.Errorf("%s: product list lacks the image version: %s", name, l)
		}
	}
	// another household: not the picture, not the product
	for _, path := range []string{url, fmt.Sprintf("/v1/products/%d/image", id), fmt.Sprintf("/v1/products/%d", id)} {
		if r := hw.Do("GET", path, b.Token, nil); r.Status() == http.StatusOK && r.Header().Get("Content-Type") == "image/jpeg" {
			t.Errorf("household B read A's picture at %s", path)
		}
	}
	if r := hw.Do("GET", fmt.Sprintf("/v1/products/%d/image", id), b.Token, nil); r.Status() != 404 {
		t.Errorf("household B: %d, want 404", r.Status())
	}
	// and without a token nobody does
	if r := hw.Do("GET", url, "", nil); r.Status() != 401 {
		t.Errorf("no token: %d", r.Status())
	}
}
