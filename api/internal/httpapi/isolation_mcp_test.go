//go:build isolation

package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpProbe says how one MCP tool is called by household B with household A's ids.
type mcpProbe struct {
	Args func(v, x *household) map[string]any
	// Foreign: the arguments name an id of A; B must get a tool error.
	Foreign bool
	// Own: the answer must contain B's own data.
	Own bool
	// Differ: the answer for B must differ from the answer for A (amounts only, no markers).
	Differ bool
	// Echo: the call sends A's key and gets it echoed; only A's taught meaning counts as a leak.
	Echo bool
}

var mcpProbes = map[string]mcpProbe{
	"spend_summary":     {Args: func(v, x *household) map[string]any { return M{"from": "2026-01-01", "to": "2026-12-31"} }, Differ: true},
	"list_receipts":     {Args: func(v, x *household) map[string]any { return M{"limit": 100} }, Own: true},
	"get_receipt":       {Args: func(v, x *household) map[string]any { return M{"id": v.ids.Receipt} }, Foreign: true},
	"search_products":   {Args: func(v, x *household) map[string]any { return M{"query": x.marker} }, Own: true},
	"price_history":     {Args: func(v, x *household) map[string]any { return M{"product": fmt.Sprint(v.ids.Product)} }, Foreign: true},
	"compare_prices":    {Args: func(v, x *household) map[string]any { return M{"product": fmt.Sprint(v.ids.Product)} }, Foreign: true},
	"price_changes":     {Args: func(v, x *household) map[string]any { return M{"since": "2026-01-01", "min_pct": 0.01} }},
	"list_shops":        {Args: func(v, x *household) map[string]any { return M{} }, Own: true},
	"frequent_items":    {Args: func(v, x *household) map[string]any { return M{"lookback_days": 3650, "min_count": 1} }},
	"shopping_list_get": {Args: func(v, x *household) map[string]any { return M{"include_checked": true} }, Own: true},
	"shopping_list_add": {Args: func(v, x *household) map[string]any {
		return M{"text": "x " + x.marker, "product": fmt.Sprint(v.ids.Product)}
	}, Foreign: true},
	"shopping_list_check":  {Args: func(v, x *household) map[string]any { return M{"id": v.ids.Item} }, Foreign: true},
	"shopping_list_remove": {Args: func(v, x *household) map[string]any { return M{"id": v.ids.Item} }, Foreign: true},
	"shopping_group_create": {Args: func(v, x *household) map[string]any {
		return M{"name": "mcp group " + x.marker, "items": []any{M{"text": "x " + x.marker}}}
	}},
	"shopping_group_list": {Args: func(v, x *household) map[string]any { return M{"include_items": true} }, Own: true},
	"shopping_group_add_items": {Args: func(v, x *household) map[string]any {
		return M{"group_id": v.ids.Group, "items": []any{M{"text": "x " + x.marker}}}
	}, Foreign: true},
	"shopping_group_remove": {Args: func(v, x *household) map[string]any { return M{"group_id": v.ids.Group, "items": "delete"} }, Foreign: true},
	"translation_todo":      {Args: func(v, x *household) map[string]any { return M{"limit": 100} }},
	"teach_word":            {Args: func(v, x *household) map[string]any { return M{"word": v.ids.Word, "english": "taught by " + x.marker} }, Echo: true},
	"review_new_products":   {Args: func(v, x *household) map[string]any { return M{"limit": 100} }},
	"resolve_review":        {Args: func(v, x *household) map[string]any { return M{"product_id": v.ids.Review, "action": "accept"} }, Foreign: true},
}

func callTool(cs *mcp.ClientSession, name string, args map[string]any) (string, bool, error) {
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error(), true, nil
	}
	b, _ := json.Marshal(res.StructuredContent)
	text := string(b)
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += " " + tc.Text
		}
	}
	return text, res.IsError, nil
}

// TestIsolationMCP calls every MCP tool as household B with household A's ids.
func TestIsolationMCP(t *testing.T) {
	w := bootWorld(t)
	if w.hosted {
		hostedMCPOff(t, w)
		return
	}
	rep := newReport()
	before := w.a.tableDigests()
	sb, err := w.b.mcpSession()
	if err != nil {
		t.Fatalf("B cannot connect to /mcp: %v", err)
	}
	defer sb.Close()
	sa, err := w.a.mcpSession()
	if err != nil {
		t.Fatalf("A cannot connect to /mcp: %v", err)
	}
	defer sa.Close()

	tools, err := sb.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = true
		p, ok := mcpProbes[tl.Name]
		if !ok {
			rep.add("mcp %s: no probe in the isolation matrix (add it to mcpProbes in isolation_mcp_test.go)", tl.Name)
			continue
		}
		text, isErr, _ := callTool(sb, tl.Name, p.Args(w.a, w.b))
		low := strings.ToLower(text)
		switch {
		case p.Echo:
			if strings.Contains(low, "english "+w.a.marker) {
				rep.add("mcp %s: B received A's data (%s)", tl.Name, truncate(text, 80))
			}
		case strings.Contains(low, w.a.marker):
			rep.add("mcp %s: B received A's data (%s)", tl.Name, truncate(text, 80))
		}
		if p.Foreign && !isErr {
			rep.add("mcp %s: B used an id of A and the call succeeded", tl.Name)
		}
		if p.Own && !strings.Contains(low, w.b.marker) {
			rep.add("mcp %s: B's own data is missing from the answer", tl.Name)
		}
		if p.Differ {
			ownA, _, _ := callTool(sa, tl.Name, p.Args(w.a, w.a))
			if text == ownA {
				rep.add("mcp %s: B's answer is identical to A's (amounts of one household are shown to the other)", tl.Name)
			}
		}
	}
	var stale []string
	for name := range mcpProbes {
		if !have[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("mcpProbes has tools that do not exist: %v", stale)
	}
	after := w.a.tableDigests()
	for tb, d := range before {
		if after[tb] != d {
			rep.add("mcp: table %s of household A was changed by B's tool calls (%s -> %s)", tb, d, after[tb])
		}
	}
	rep.fail(t, "mcp, world: "+w.mode)
}

// TestIsolationMCPProbesWorkForTheOwner: every MCP probe must succeed for the household that owns the ids
// (each on a fresh world, because some tools change data), otherwise a refusal for B proves nothing.
func TestIsolationMCPProbesWorkForTheOwner(t *testing.T) {
	if os.Getenv("ISOLATION_WORLD") == "hosted" {
		t.Skip("MCP is off in the hosted service; TestIsolationMCP checks that")
	}
	names := make([]string, 0, len(mcpProbes))
	for name := range mcpProbes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w := bootWorld(t)
		sa, err := w.a.mcpSession()
		if err != nil {
			t.Fatal(err)
		}
		text, isErr, _ := callTool(sa, name, mcpProbes[name].Args(w.a, w.a))
		sa.Close()
		if isErr {
			t.Errorf("mcp %s: the probe fails for the owner (fix mcpProbes): %s", name, truncate(text, 160))
		}
	}
}

// hostedMCPOff: the hosted service does not offer MCP at launch, for either household.
func hostedMCPOff(t *testing.T, w *world) {
	for _, h := range []*household{w.a, w.b} {
		rec := h.do("POST", "/mcp", map[string]any{})
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), "feature_unavailable") {
			t.Errorf("hosted: POST /mcp for %s answered %d %s, want 404 feature_unavailable", h.name, rec.Code, truncate(rec.Body.String(), 80))
		}
	}
}
