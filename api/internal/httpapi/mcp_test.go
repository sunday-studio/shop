package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

func connectMCP(t *testing.T, e *env, token string) (*mcp.ClientSession, error) {
	t.Helper()
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	return client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: token, base: http.DefaultTransport}},
		MaxRetries: -1,
	}, nil)
}

// call invokes a tool and decodes its structured output into T.
func call[T any](t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (T, *mcp.CallToolResult) {
	t.Helper()
	var out T
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		return out, res
	}
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s: decode %s: %v", name, b, err)
	}
	return out, res
}

func errText(res *mcp.CallToolResult) string {
	if res == nil || !res.IsError || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return "?"
}

func TestMCPRequiresToken(t *testing.T) {
	e := newEnv(t)
	if _, err := connectMCP(t, e, "pt_wrong"); err == nil {
		t.Fatal("connect with a bad token should fail")
	}
}

func TestMCPTools(t *testing.T) {
	f := newStatsFixture(t)
	cs, err := connectMCP(t, f.e, f.e.token)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 13 {
		t.Errorf("tools = %d", len(tools.Tools))
	}

	type amount struct {
		Cents int64  `json:"cents"`
		EUR   string `json:"eur"`
	}

	// Spend, overall and per shop by fuzzy name.
	spend, _ := call[struct {
		Total    amount `json:"total"`
		Receipts int    `json:"receipts"`
		Buckets  []struct {
			Key   string `json:"key"`
			Cents int64  `json:"cents"`
		} `json:"buckets"`
	}](t, cs, "spend_summary", map[string]any{"from": "2026-09-01", "to": "2026-10-31"})
	if spend.Total.Cents != 725 || spend.Total.EUR != "€7,25" || spend.Receipts != 4 || len(spend.Buckets) != 2 {
		t.Errorf("spend: %+v", spend)
	}
	lidl, _ := call[struct {
		Shop  string `json:"shop"`
		Total amount `json:"total"`
	}](t, cs, "spend_summary", map[string]any{"from": "2026-09-01", "to": "2026-10-31", "shop": "lidl"})
	if lidl.Shop != "Lidl" || lidl.Total.Cents != 119 {
		t.Errorf("spend lidl: %+v", lidl)
	}

	// "Where is milk cheapest?"
	cmp, _ := call[struct {
		Product struct{ Name string } `json:"product"`
		Shops   []struct {
			Shop     string `json:"shop"`
			Latest   amount `json:"latest_unit_price"`
			BaseUnit string `json:"base_unit"`
			Cheapest bool   `json:"cheapest"`
		} `json:"shops"`
	}](t, cs, "compare_prices", map[string]any{"product": "halfvolle melk"})
	if cmp.Product.Name != "Halfvolle melk" || len(cmp.Shops) != 2 || cmp.Shops[0].Shop != "Lidl" ||
		!cmp.Shops[0].Cheapest || cmp.Shops[0].Latest.EUR != "€0,79" || cmp.Shops[0].BaseUnit != "l" {
		t.Errorf("compare: %+v", cmp)
	}

	hist, _ := call[struct {
		Points []struct{ Date, Shop string } `json:"points"`
	}](t, cs, "price_history", map[string]any{"product": "melk", "shop": "AH"})
	if len(hist.Points) != 3 || hist.Points[0].Date != "2026-09-01" {
		t.Errorf("history: %+v", hist)
	}

	changes, _ := call[struct {
		Changes []struct {
			Product string  `json:"product"`
			Pct     float64 `json:"pct"`
		} `json:"changes"`
	}](t, cs, "price_changes", map[string]any{"since": "2026-09-01"})
	if len(changes.Changes) != 1 || changes.Changes[0].Pct != 8.1 {
		t.Errorf("changes: %+v", changes)
	}

	search, _ := call[struct {
		Products []struct{ Name string } `json:"products"`
	}](t, cs, "search_products", map[string]any{"query": "banaan"})
	if len(search.Products) == 0 || search.Products[0].Name != "Bananen" {
		t.Errorf("search: %+v", search)
	}

	recs, _ := call[struct {
		Receipts []struct {
			ID   int64  `json:"id"`
			Shop string `json:"shop"`
		} `json:"receipts"`
	}](t, cs, "list_receipts", map[string]any{"shop": "AH"})
	if len(recs.Receipts) != 3 || recs.Receipts[0].Shop != "AH" {
		t.Fatalf("receipts: %+v", recs)
	}
	rec, _ := call[struct {
		Lines []struct {
			Text, Kind, Product string
		} `json:"lines"`
	}](t, cs, "get_receipt", map[string]any{"id": f.r1.Id})
	if len(rec.Lines) != 2 || rec.Lines[0].Product != "Halfvolle melk" {
		t.Errorf("receipt: %+v", rec)
	}

	// Shopping list round trip.
	type item struct {
		ID       int64  `json:"id"`
		Text     string `json:"text"`
		Cheapest string `json:"cheapest_at"`
		Checked  bool   `json:"checked"`
		Product  *struct{ Name string }
	}
	add, _ := call[struct {
		Item   item `json:"item"`
		Merged bool `json:"merged"`
	}](t, cs, "shopping_list_add", map[string]any{"text": "halfvolle melk"})
	if add.Item.Product == nil || add.Item.Cheapest != "Lidl (€0,79/l)" || add.Merged {
		t.Errorf("add milk: %+v", add)
	}
	again, _ := call[struct {
		Merged bool `json:"merged"`
	}](t, cs, "shopping_list_add", map[string]any{"text": "eggs", "product": "melk"})
	if !again.Merged {
		t.Errorf("re-adding milk should merge: %+v", again)
	}
	eggs, _ := call[struct {
		Item item `json:"item"`
	}](t, cs, "shopping_list_add", map[string]any{"text": "eieren", "qty": 12})
	list, _ := call[struct {
		Items []item `json:"items"`
	}](t, cs, "shopping_list_get", nil)
	if len(list.Items) != 2 {
		t.Errorf("list: %+v", list)
	}
	checked, _ := call[struct {
		Item item `json:"item"`
	}](t, cs, "shopping_list_check", map[string]any{"id": eggs.Item.ID})
	if !checked.Item.Checked {
		t.Errorf("check: %+v", checked)
	}
	if ok, _ := call[struct{ OK bool }](t, cs, "shopping_list_remove", map[string]any{"id": eggs.Item.ID}); !ok.OK {
		t.Error("remove failed")
	}

	// Tool errors come back as IsError results, not protocol errors.
	if _, res := call[struct{}](t, cs, "compare_prices", map[string]any{"product": "xyzzy plugh"}); !strings.Contains(errText(res), "no product matches") {
		t.Errorf("unknown product error: %q", errText(res))
	}
	if _, res := call[struct{}](t, cs, "spend_summary", map[string]any{"from": "september"}); !strings.Contains(errText(res), "invalid date") {
		t.Errorf("bad date error: %q", errText(res))
	}

	// Resources.
	r, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "pantry://shops"})
	if err != nil || len(r.Contents) != 1 || !strings.Contains(r.Contents[0].Text, `"Lidl"`) {
		t.Errorf("shops resource: %v %+v", err, r)
	}
}
