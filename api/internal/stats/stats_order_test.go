package stats

import "testing"

func TestEqualSpendBucketsOrderByNameIgnoringCase(t *testing.T) {
	g := newGrouper()
	g.add("3", "zebra", 500, 1)
	g.add("1", "Banana", 500, 2)
	g.add("2", "apple", 500, 3)
	g.add("9", "Big", 900, 4)
	var got []string
	for _, b := range g.buckets(true) {
		got = append(got, b.Label)
	}
	want := []string{"Big", "apple", "Banana", "zebra"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v; want %v", got, want)
		}
	}
}
