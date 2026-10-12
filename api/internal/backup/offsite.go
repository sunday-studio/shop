package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// OffsiteFile is the status file the optional off-site copy (deploy/offsite/offsite.sh)
// writes into the data directory. The API reads it for /healthz. It says nothing about
// where the copy goes beyond a label chosen by the owner: any rclone destination works.
const OffsiteFile = "offsite_status.json"

// OffsiteStaleAfter is how old the last successful off-site copy may be. Backups are
// daily and the copy follows each one, plus slack for a missed night.
const OffsiteStaleAfter = 36 * time.Hour

// OffsiteStatus is the result of the latest off-site copy attempt.
type OffsiteStatus struct {
	OK          bool      `json:"ok"`
	At          time.Time `json:"at"`                   // latest attempt
	LastOKAt    time.Time `json:"last_ok_at,omitempty"` // latest success (zero when none yet)
	Destination string    `json:"destination,omitempty"`
	Snapshot    string    `json:"snapshot,omitempty"`
	Error       string    `json:"error,omitempty"`
	DurationMS  int64     `json:"duration_ms"`
}

// ReadOffsiteStatus loads the off-site status file from the data directory. A missing
// file (os.ErrNotExist) means no off-site copy has ever run: that is normal.
func ReadOffsiteStatus(dataDir string) (OffsiteStatus, error) {
	var st OffsiteStatus
	b, err := os.ReadFile(filepath.Join(dataDir, OffsiteFile))
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("%s: %w", OffsiteFile, err)
	}
	return st, nil
}

// Evaluate judges the status at time now. It returns whether the off-site copy is
// healthy and a short description for /healthz.
func (s OffsiteStatus) Evaluate(now time.Time) (bool, string) {
	name := s.Destination
	if name == "" {
		name = "off-site"
	}
	since := func(t time.Time) time.Duration { return now.Sub(t).Round(time.Minute) }
	switch {
	case !s.OK:
		last := "never succeeded"
		if !s.LastOKAt.IsZero() {
			last = fmt.Sprintf("last success %s ago", since(s.LastOKAt))
		}
		return false, fmt.Sprintf("%s copy failed %s ago (%s): %s", name, since(s.At), last, s.Error)
	case s.LastOKAt.IsZero():
		// Written by an older script or by hand: fall back to the attempt time.
		s.LastOKAt = s.At
	}
	if age := since(s.LastOKAt); age > OffsiteStaleAfter {
		return false, fmt.Sprintf("%s copy stale: last success %s ago", name, age)
	}
	return true, fmt.Sprintf("%s copy %s ago", name, since(s.LastOKAt))
}
