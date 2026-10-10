package receipt

import (
	"regexp"
	"strconv"
	"time"
)

var (
	dateRe = regexp.MustCompile(`\b(\d{1,2})[-/.](\d{1,2})[-/.](\d{4}|\d{2})\b`)
	timeRe = regexp.MustCompile(`\b([01]?\d|2[0-3])[:.]([0-5]\d)(?:[:.]([0-5]\d))?\b`)
)

// FindDate returns the first plausible dd-mm-yyyy date (with an optional
// HH:MM on the same row) in local time. Dates in the future or more than
// two years before now are rejected as misreads.
func FindDate(rows []string, loc *time.Location, now time.Time) (time.Time, bool) {
	for _, row := range rows {
		for _, m := range dateRe.FindAllStringSubmatchIndex(row, -1) {
			d, _ := strconv.Atoi(row[m[2]:m[3]])
			mo, _ := strconv.Atoi(row[m[4]:m[5]])
			y, _ := strconv.Atoi(row[m[6]:m[7]])
			if y < 100 {
				y += 2000
			}
			if d < 1 || d > 31 || mo < 1 || mo > 12 {
				continue
			}
			hh, mm, ss := 0, 0, 0
			if tm := timeRe.FindStringSubmatch(row[m[1]:]); tm != nil {
				hh, _ = strconv.Atoi(tm[1])
				mm, _ = strconv.Atoi(tm[2])
				if tm[3] != "" {
					ss, _ = strconv.Atoi(tm[3])
				}
			}
			t := time.Date(y, time.Month(mo), d, hh, mm, ss, 0, loc)
			if t.Day() != d { // e.g. 31-02 normalised into March
				continue
			}
			if t.After(now.Add(24*time.Hour)) || t.Before(now.AddDate(-2, 0, 0)) {
				continue
			}
			return t, true
		}
	}
	return time.Time{}, false
}
