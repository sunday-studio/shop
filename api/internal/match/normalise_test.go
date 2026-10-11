package match

import (
	"sync"
	"testing"
)

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

// Fold is called from many goroutines at once (requests and background jobs); run with -race.
func TestFoldIsSafeForConcurrentUse(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				if got := Fold("Crème Fraîche ÉCLAIR"); got != "creme fraiche eclair" {
					t.Errorf("Fold = %q", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}
