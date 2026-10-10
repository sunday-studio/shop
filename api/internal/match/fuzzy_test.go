package match

import "testing"

func TestScoreRanksReceiptTextToProduct(t *testing.T) {
	// Each receipt text should score its true product above the decoys.
	products := []string{"halfvol melk", "vol melk", "karnemelk", "halfvol yoghurt", "kipfilet", "kipdijfilet"}
	cases := map[string]string{
		"ah halfvol melk 1,5l": "halfvol melk",
		"melk halfvol":         "halfvol melk",
		"kipfilet 500g":        "kipfilet",
		"karnemlk":             "karnemelk",
		"halfvol yogh":         "halfvol yoghurt",
	}
	for text, want := range cases {
		best, bestScore := "", -1.0
		for _, p := range products {
			if s := Score(Normalise(text), p); s > bestScore {
				best, bestScore = p, s
			}
		}
		if best != want {
			t.Errorf("%q → %q (%.3f), want %q", text, best, bestScore, want)
		}
	}
}

func TestScoreBounds(t *testing.T) {
	if Score("melk", "melk") != 1 {
		t.Error("identical should be 1")
	}
	if s := Score("melk", "zzzz"); s > 0.3 {
		t.Errorf("unrelated = %.3f", s)
	}
	if Score("", "melk") != 0 {
		t.Error("empty should be 0")
	}
}

func TestTrigrams(t *testing.T) {
	got := Trigrams("melk ah")
	if len(got) != 2 || got[0] != "mel" || got[1] != "elk" {
		t.Errorf("trigrams = %v", got)
	}
}
