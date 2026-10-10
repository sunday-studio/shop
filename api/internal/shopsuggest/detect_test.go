package shopsuggest

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"shop/internal/ocr"
	"shop/internal/receipt"
)

// Headers (first ~25 OCR boxes) of 13 photographed receipts: 7 Albert Heijn, 4 Dirk, 2 butcher slips.
func header(t *testing.T, n int) []string {
	t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("testdata/IMG_%d.json", n))
	if err != nil {
		t.Fatal(err)
	}
	var res ocr.Result
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range receipt.BuildRows(res.Lines) {
		out = append(out, r.Raw)
	}
	return out
}

func TestRealReceiptHeaders(t *testing.T) {
	ah := []int{1463, 1466, 1468, 1469, 1471, 1473, 1474}
	dirk := []int{1464, 1465, 1470, 1475}
	var first = map[string]*Detected{}
	check := func(ns []int, chain, branchPrefix string) {
		for _, n := range ns {
			d := Detect(header(t, n))
			if d == nil || d.Chain != chain || !d.Known {
				t.Errorf("IMG_%d: got %+v; want known chain %s", n, d, chain)
				continue
			}
			if first[chain] == nil {
				first[chain] = d
			} else if !SameBranch(first[chain], d) {
				t.Errorf("IMG_%d: branch %q (%s %s) is not the same branch as %q", n, d.Branch, d.Street, d.Number, first[chain].Branch)
			}
			if len(d.Branch) < len(branchPrefix) || d.Branch[:len(branchPrefix)] != branchPrefix {
				t.Errorf("IMG_%d: branch = %q; want prefix %q", n, d.Branch, branchPrefix)
			}
		}
	}
	check(ah, "Albert Heijn", "Hoofdstraat 12")
	check(dirk, "Dirk", "Marktw")
	if SameBranch(first["Albert Heijn"], first["Dirk"]) {
		t.Error("AH and Dirk must not be the same branch")
	}
}

func TestButcherSlipsGetNoSuggestion(t *testing.T) {
	for _, n := range []int{1467, 1472} {
		if d := Detect(header(t, n)); d != nil {
			t.Errorf("IMG_%d: got %+v; want no suggestion", n, d)
		}
	}
}

func TestUnknownShopNeedsNameAndPostcode(t *testing.T) {
	d := Detect([]string{"command", "Bakkerij De Korenaar", "Hoofdstraat 12", "1234 AB Utrecht", "Brood 2,50"})
	if d == nil || d.Known || d.Chain != "Bakkerij De Korenaar" || d.Branch != "Hoofdstraat 12, Utrecht" {
		t.Errorf("got %+v", d)
	}
	if d := Detect([]string{"KIPFILET 1,00", "Hoofdstraat 12", "BROOD 2,00"}); d != nil {
		t.Errorf("no name/postcode, got %+v", d)
	}
}

func TestKeyboardNoiseIgnored(t *testing.T) {
	d := Detect([]string{"command", "option", "ctrl", "shift", "Jumbo", "Veilingstraat 5 Gouda"})
	if d == nil || d.Chain != "Jumbo" || d.Branch != "Veilingstraat 5, Gouda" {
		t.Errorf("got %+v", d)
	}
}
