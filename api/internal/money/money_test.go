package money

import "testing"

func TestEUR(t *testing.T) {
	for cents, want := range map[int64]string{
		0: "€0,00", 5: "€0,05", 129: "€1,29", -50: "-€0,50", 123456: "€1.234,56", 100000000: "€1.000.000,00",
	} {
		if got := EUR(cents); got != want {
			t.Errorf("EUR(%d) = %q, want %q", cents, got, want)
		}
	}
}
