package hostedapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"shop/internal/hosted/control"
	w "shop/internal/hosted/hostedworld"
)

// The answer kept for an Idempotency-Key must not hold the invite code in plaintext, and a replay
// must still return the same code.
func TestIdempotencyStoreKeepsNoInviteCodeInPlaintext(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")

	first := world.Do("POST", "/v1/household/invites", ama.Token, nil, "Idempotency-Key", "inv-1")
	want(t, first, 201, "")
	code, _ := first.JSON()["code"].(string)
	if code == "" {
		t.Fatalf("no code: %s", first.Body)
	}
	again := world.Do("POST", "/v1/household/invites", ama.Token, nil, "Idempotency-Key", "inv-1")
	if again.Header().Get("Idempotent-Replay") != "true" || again.JSON()["code"] != code {
		t.Fatalf("replay did not return the same code: %s", again.Body)
	}

	sum := sha256.Sum256(nil)
	res, rec, err := world.Store.BeginIdempotent(context.Background(), ama.Account["id"].(string), "inv-1", "POST", "/v1/household/invites", hex.EncodeToString(sum[:]), time.Hour)
	if err != nil || res != control.IdemReplay {
		t.Fatalf("stored record: %s %v", res, err)
	}
	if len(rec.Response) == 0 || bytes.Contains(rec.Response, []byte(code)) || strings.Contains(string(rec.Response), "code") {
		t.Fatalf("the stored answer is readable: %q", rec.Response)
	}
}
