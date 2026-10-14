package appleclient_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"shop/internal/hosted/appleclient"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// parseJWT splits a compact JWT, verifies an ES256 signature with pub and returns header and claims.
func parseJWT(t *testing.T, tok string, pub *ecdsa.PublicKey) (hdr, claims map[string]any) {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", tok)
	}
	for i, dst := range []*map[string]any{&hdr, &claims} {
		b, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil || json.Unmarshal(b, dst) != nil {
			t.Fatalf("bad JWT part %d", i)
		}
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("bad signature length")
	}
	d := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, d[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("JWT signature does not verify")
	}
	return hdr, claims
}

// rec gives Options with no real sleeping; Sleeps records the pauses, Logs the log output.
type rec struct {
	mu     sync.Mutex
	Sleeps []time.Duration
	Logs   bytes.Buffer
}

func (r *rec) options() appleclient.Options {
	return appleclient.Options{
		MaxAttempts: 3,
		Sleep: func(_ context.Context, d time.Duration) error {
			r.mu.Lock()
			r.Sleeps = append(r.Sleeps, d)
			r.mu.Unlock()
			return nil
		},
		Log: slog.New(slog.NewTextHandler(&r.Logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}
