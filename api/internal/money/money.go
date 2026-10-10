// Package money formats integer cents for people.
package money

import (
	"fmt"
	"strconv"
	"strings"
)

// EUR formats cents the Dutch way: 129 → "€1,29", -50 → "-€0,50",
// 123456 → "€1.234,56".
func EUR(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	whole := strconv.FormatInt(cents/100, 10)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return fmt.Sprintf("%s€%s,%02d", sign, b.String(), cents%100)
}
