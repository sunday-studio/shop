package httpapi

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"shop/internal/auth"
	"shop/internal/store"
	"shop/internal/store/db"
	"shop/internal/version"
)

// pairLimiter slows down password guessing: an address is locked out after
// 5 failures in 15 minutes, and pairing is paused for everyone after 30
// failures in an hour (in case addresses rotate).
type pairLimiter struct {
	mu     sync.Mutex
	perIP  map[string][]time.Time
	global []time.Time
	now    func() time.Time
}

const (
	ipMaxFails     = 5
	ipWindow       = 15 * time.Minute
	globalMaxFails = 30
	globalWindow   = time.Hour
)

func newPairLimiter() *pairLimiter {
	return &pairLimiter{perIP: map[string][]time.Time{}, now: time.Now}
}

func recent(ts []time.Time, since time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if t.After(since) {
			out = append(out, t)
		}
	}
	return out
}

// allowed reports whether ip may try, and if not, how long to wait.
func (l *pairLimiter) allowed(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.global = recent(l.global, now.Add(-globalWindow))
	if len(l.global) >= globalMaxFails {
		return false, l.global[0].Add(globalWindow).Sub(now)
	}
	fails := recent(l.perIP[ip], now.Add(-ipWindow))
	l.perIP[ip] = fails
	if len(fails) >= ipMaxFails {
		return false, fails[0].Add(ipWindow).Sub(now)
	}
	return true, 0
}

func (l *pairLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.perIP[ip] = append(l.perIP[ip], now)
	l.global = append(l.global, now)
}

func (l *pairLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.perIP, ip)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type statusResponse struct {
	Server         string `json:"server"`
	Version        string `json:"version"`
	PairingEnabled bool   `json:"pairing_enabled"`
}

// authStatus lets the app confirm a URL points at a Shop server (and that
// pairing is set up) before asking for the password.
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	ok, err := auth.HasPassword(r.Context(), s.q)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Server: "shop", Version: version.Version, PairingEnabled: ok})
}

type pairRequest struct {
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
}

type deviceJSON struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Current    bool       `json:"current"`
}

type pairResponse struct {
	Token  string     `json:"token"`
	Device deviceJSON `json:"device"`
}

func toDevice(t db.ApiToken, currentID int64) deviceJSON {
	created, _ := store.ParseTS(t.CreatedAt)
	return deviceJSON{ID: t.ID, Name: t.Name, CreatedAt: created, LastUsedAt: store.TimePtr(t.LastUsedAt), Current: t.ID == currentID}
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if ok, wait := s.pairing.allowed(ip); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeProblem(w, http.StatusTooManyRequests, "too many attempts", "try again in "+wait.Round(time.Minute).String())
		return
	}
	var in pairRequest
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	tok, row, err := auth.Pair(r.Context(), s.q, in.Password, in.DeviceName)
	switch {
	case errors.Is(err, auth.ErrNoPassword):
		writeProblem(w, http.StatusConflict, "pairing not set up", err.Error())
		return
	case errors.Is(err, auth.ErrWrongPassword):
		s.pairing.fail(ip)
		s.Log.Warn("pairing failed", "ip", ip, "device", in.DeviceName)
		writeProblem(w, http.StatusUnauthorized, "wrong password", "")
		return
	case err != nil:
		s.writeErr(w, r, err)
		return
	}
	s.pairing.success(ip)
	s.Log.Info("device paired", "ip", ip, "device", row.Name)
	writeJSON(w, http.StatusCreated, pairResponse{Token: tok, Device: toDevice(row, row.ID)})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	t, _ := currentToken(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"name": t.Name, "kind": t.Kind})
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	cur, _ := currentToken(r.Context())
	rows, err := s.q.ListDeviceTokens(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]deviceJSON, len(rows))
	for i, t := range rows {
		out[i] = toDevice(t, cur.ID)
	}
	writeJSON(w, http.StatusOK, out)
}

// revokeDevice removes a paired device (its token stops working at once).
// Tokens created with `shop token create` are managed on the server only.
func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	t, err := s.q.GetToken(r.Context(), id)
	if err != nil || t.Kind != "device" {
		writeProblem(w, http.StatusNotFound, "not found", "")
		return
	}
	if _, err := s.q.DeleteTokenByID(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
