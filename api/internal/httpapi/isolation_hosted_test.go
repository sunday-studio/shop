//go:build isolation

package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"shop/internal/hosted/hostedapi"
	"shop/internal/hosted/hostedworld"
	"shop/internal/openfoodfacts"
)

// bootHostedWorld is the isolation world for the hosted service: two accounts, each with its own
// household (own SQLite file and image folder under the hosted provider), real control database,
// device tokens resolved through the control database, every request through the plan gate, limits
// and idempotency layers. ISOLATION_WORLD=hosted selects it for the whole isolation suite.
func bootHostedWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	f := newFakeOCR()
	t.Cleanup(f.srv.Close)
	hw := hostedworld.NewWith(t, f.srv.URL, hostedworld.NoRates, func(c *hostedapi.Config) {
		c.Limits.ReceiptsPerMonth, c.Limits.ReceiptsPerDay, c.Limits.ReceiptsPerMonthSoft = 0, 0, 0 // the seed uploads many receipts
		c.Limits.MaxQueuedJobs = 0
	})
	foodFacts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/product/8710400000017.json" {
			_, _ = w.Write([]byte(`{"status":1,"product":{"product_name_nl":"Halfvolle melk","brands":"AH"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":0}`))
	}))
	t.Cleanup(foodFacts.Close)
	logoDev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nologo.nl" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNG:" + strings.TrimPrefix(r.URL.Path, "/")))
	}))
	t.Cleanup(logoDev.Close)
	hw.RT.FoodFacts = openfoodfacts.New("test").WithBaseURL(foodFacts.URL)
	hw.RT.Cfg.LogoDevToken = "pk_test"
	hw.RT.LogoBaseURL = logoDev.URL
	hw.RT.Backoff = func(int64) time.Duration { return 0 }
	w := &world{t: t, ocr: f, mode: "hosted: control database, device tokens, one household per account", hosted: true}
	pins := map[string]func(){}
	users := map[string]hostedworld.User{}
	mk := func(name, marker, sub string) *household {
		u := hw.SignInDevice(sub, marker, "iPhone "+marker)
		hw.Subscribe(u, "otx-"+sub)
		users[name] = u
		id := hw.HouseholdID(u)
		if err := hw.Prov.Provision(ctx, id); err != nil {
			t.Fatal(err)
		}
		tn, release, err := hw.Prov.Acquire(ctx, id) // pinned for the test, so it is never evicted
		if err != nil {
			t.Fatal(err)
		}
		pins[name] = release
		in, err := hw.RT.Instance(ctx, tn)
		if err != nil {
			t.Fatal(err)
		}
		return &household{name: name, marker: marker, token: u.Token, w: w, conn: tn.DB, handler: hw.Handler, imagesDir: tn.ImagesDir,
			drain: func() {
				for in.Queue.RunOne(ctx) {
				}
			}}
	}
	w.a = mk("A", "xalpha", "iso-a")
	w.b = mk("B", "xbeta", "iso-b")
	w.erase = func(h *household) error {
		if rel := pins[h.name]; rel != nil {
			rel()
			delete(pins, h.name)
		}
		id := hw.HouseholdID(users[h.name])
		// What the lifecycle job does at stage 2: erase the files, then mark the household deleted.
		if err := hw.Prov.Erase(ctx, id); err != nil {
			return err
		}
		return hw.Store.MarkHouseholdDeleted(ctx, id, hw.Clock.Now())
	}
	return w
}

// hostedSkip: operations whose behaviour differs by design in the hosted service. Devices live in the
// control database and are tested in hostedapi (the household database has no tokens of its own there).
func hostedSkip(op specOp) bool {
	return strings.HasPrefix(op.Tmpl, "/v1/auth/devices")
}

// TestIsolationHostedMember: a member of a household shares that household's data and nothing of the
// other household; leaving ends the access.
func TestIsolationHostedMember(t *testing.T) {
	hw := hostedworld.New(t, hostedworld.NoRates)
	a := hw.SignIn("iso-a", "Ama")
	hw.Subscribe(a, "otx-a")
	b := hw.SignIn("iso-b", "Bob")
	hw.Subscribe(b, "otx-b")
	m := hw.SignIn("iso-m", "Mia")

	shopA := hw.Do("POST", "/v1/shops", a.Token, map[string]any{"name": "ShopOfA"})
	shopB := hw.Do("POST", "/v1/shops", b.Token, map[string]any{"name": "ShopOfB"})
	if shopA.Status() != 201 || shopB.Status() != 201 {
		t.Fatal(shopA.Body, shopB.Body)
	}
	inv := hw.Do("POST", "/v1/household/invites", a.Token, nil)
	if r := hw.Do("POST", "/v1/household/invites/"+inv.JSON()["code"].(string)+"/accept", m.Token, nil); r.Status() != 200 {
		t.Fatal(r.Body)
	}
	// give A higher ids than B has, so a hit cannot be B's own row with the same number
	extra := hw.Do("POST", "/v1/shops", a.Token, map[string]any{"name": "ShopOfA 2"})
	extra = hw.Do("POST", "/v1/shops", a.Token, map[string]any{"name": "ShopOfA 3"})
	idA := int64(extra.JSON()["id"].(float64))
	list := func(tok string) string { return hw.Do("GET", "/v1/shops", tok, nil).Body.String() }
	if l := list(m.Token); !strings.Contains(l, "ShopOfA") || strings.Contains(l, "ShopOfB") {
		t.Errorf("member sees %s", l)
	}
	if r := hw.Do("POST", "/v1/shops", m.Token, map[string]any{"name": "AddedByMember"}); r.Status() != 201 {
		t.Fatal(r.Body)
	}
	if l := list(a.Token); !strings.Contains(l, "AddedByMember") {
		t.Errorf("owner does not see the member's shop: %s", l)
	}
	if l := list(b.Token); strings.Contains(l, "AddedByMember") || strings.Contains(l, "ShopOfA") {
		t.Errorf("B sees A's household: %s", l)
	}
	// B and the stranger-to-A cannot reach A's ids; the member cannot reach B's.
	for _, c := range []struct{ tok, path string }{{b.Token, "/v1/shops/" + itoa(idA)}} {
		if r := hw.Do("GET", c.path, c.tok, nil); r.Status() != http.StatusNotFound {
			t.Errorf("GET %s: %d", c.path, r.Status())
		}
	}
	// The member's account endpoints show the household's plan but not the owner's private data.
	acc := hw.Do("GET", "/v1/account", m.Token, nil).JSON()
	if acc["role"] != "member" || acc["plan"].(map[string]any)["is_owner"] != false {
		t.Errorf("%v", acc)
	}
	if strings.Contains(hw.Do("GET", "/v1/household", m.Token, nil).Body.String(), "pending_invites\"") {
		t.Error("member sees the invite list")
	}
	// Leaving ends the access at once.
	if r := hw.Do("POST", "/v1/household/leave", m.Token, nil); r.Status() != 204 {
		t.Fatal(r.Body)
	}
	if r := hw.Do("GET", "/v1/shops", m.Token, nil); r.Status() != 403 {
		t.Errorf("after leaving: %d %s", r.Status(), r.Body)
	}
	// Two separate database files, two separate image folders.
	ha, hb := hw.HouseholdID(a), hw.HouseholdID(b)
	if ha == hb || hw.Prov.Dir(ha) == hw.Prov.Dir(hb) {
		t.Errorf("households share a folder: %s %s", ha, hb)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
