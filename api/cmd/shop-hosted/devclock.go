package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"

	"shop/internal/hosted/hclock"
)

// devClockAllowed is true only for the fake Apple of local stacks and tests. The configuration
// already refuses SHOP_APPLE_MODE=fake with the production environment; this checks both again so
// the movable clock can never be built for a production server.
func devClockAllowed(c hostedConfig) bool {
	return c.Apple.Mode == "fake" && c.Apple.Environment != "production" && c.API.Environment != "production"
}

// serveClock is the clock `serve` runs on: the system clock, or (fake Apple only) the system clock
// plus a forward offset that POST /admin/v1/dev/advance-clock moves.
func serveClock(c hostedConfig) (hclock.Clock, error) {
	if !devClockAllowed(c) {
		return hclock.System{}, nil
	}
	return hclock.NewOffset(filepath.Join(c.App.DataDir, "dev-clock-offset"))
}

// devWithClock adds the "advance-clock" development route (admin listener, loopback and admin
// token as for every admin route) in front of the fake Apple's routes. Body: {"seconds": N} or
// {"hours": N} or {"days": N} (forward only). The answer carries the server's new time.
func devWithClock(next func(http.ResponseWriter, *http.Request, string), off *hclock.Offset) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, what string) {
		if what != "advance-clock" {
			next(w, r, what)
			return
		}
		var in struct {
			Seconds float64 `json:"seconds"`
			Hours   float64 `json:"hours"`
			Days    float64 `json:"days"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in)
		d := time.Duration((in.Seconds + in.Hours*3600 + in.Days*86400) * float64(time.Second))
		w.Header().Set("Content-Type", "application/json")
		if d > 0 {
			if err := off.Advance(d); err != nil {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"title": "Invalid request", "status": 400, "code": "invalid_request", "detail": err.Error()})
				return
			}
		} else if in.Seconds != 0 || in.Hours != 0 || in.Days != 0 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"title": "Invalid request", "status": 400, "code": "invalid_request", "detail": "the clock only moves forward"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"now": off.Now().Format(time.RFC3339), "offset_seconds": int64(off.Offset() / time.Second)})
	}
}
