package match

import "testing"

func TestNormalise(t *testing.T) {
	cases := map[string]string{
		"AH HALFV MELK 1,5L":      "ah halfvol melk 1,5l",
		"Crème Fraîche":           "creme fraiche",
		"  KIPFILET   1,79 B ":    "kipfilet b",
		"M&M's":                   "m en m s",
		"Volle Yoghurt 1 LTR":     "vol yoghurt 1 l",
		"BONUS: 2e HALVE PRIJS!!": "bonus 2e halve prijs",
	}
	for in, want := range cases {
		if got := Normalise(in); got != want {
			t.Errorf("Normalise(%q) = %q; want %q", in, got, want)
		}
	}
}
