package autoprod

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCleanLine(t *testing.T) {
	for _, c := range []struct {
		raw  string
		name string
		skip bool
		odd  bool
	}{
		{"2 COCA-COLA 2,99", "Coca-Cola", false, false},
		{"HHTHN AH ZALMFILET", "AH Zalmfilet", false, false},
		{"HMHMNTT STARB COFFEE 4,99", "Starb Coffee", false, false},
		{"TTNN FIT PIGGY", "Fit Piggy", false, false},
		{"-- AH SPRUITJES 2,59", "AH Spruitjes", false, false},
		{"3,170 kg x 5", "", true, false},
		{"D LET KILO", "", true, false},
		{"NUNNUNONNTT BIO ZON OLIE", "Nunnunonntt Bio Zon Olie", false, true},
		{"AH BIO MELK", "AH Bio Melk", false, false},
	} {
		got := CleanLine(c.raw)
		if got.Skip != c.skip || got.Name != c.name || got.Odd != c.odd {
			t.Errorf("CleanLine(%q) = %+v; want name %q skip %v odd %v", c.raw, got, c.name, c.skip, c.odd)
		}
	}
	if CleanLine("2 COCA COLA").Norm != CleanLine("COCA-COLA").Norm {
		t.Error("qty and hyphen must not change the normalised text")
	}
}

// The reference evaluation from spike/autopop/cat_eval.py: 50 correct, 1 wrong, 16 no hit.
func TestCategoryRulesReproduceTheSpikeNumbers(t *testing.T) {
	b, err := os.ReadFile("testdata/category_truth.json")
	if err != nil {
		t.Fatal(err)
	}
	var truth map[string]string
	if err := json.Unmarshal(b, &truth); err != nil {
		t.Fatal(err)
	}
	ok, wrong, none := 0, 0, 0
	for name, want := range truth {
		got, hit := seedCategoryName(name)
		switch {
		case !hit:
			none++
		case got == want:
			ok++
		default:
			wrong++
			t.Logf("wrong: %q = %q, truth %q", name, got, want)
		}
	}
	if len(truth) != 67 || ok != 50 || wrong != 1 || none != 16 {
		t.Errorf("%d products: correct %d wrong %d no hit %d; want 67: 50 / 1 / 16", len(truth), ok, wrong, none)
	}
}
