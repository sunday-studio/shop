//go:build isolation

package httpapi_test

import (
	"context"
	"testing"

	"shop/internal/auth"
)

// bootSingleTenantWorld is what the code can do today: one server, one database, and two tokens in it.
// "Household A" and "household B" are just two device tokens of the same data. Every isolation check
// therefore fails, and the failures list what has to change. It is replaced by two real households
// when the tenant seam exists.
func bootSingleTenantWorld(t *testing.T) *world {
	t.Helper()
	e := newEnv(t)
	tokB, err := auth.Create(context.Background(), e.q, "household-b")
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, ocr: e.ocr, mode: "single-tenant: one database, one tenant"}
	mk := func(name, marker, token string) *household {
		return &household{name: name, marker: marker, token: token, w: w, conn: e.conn, handler: e.h, drain: e.drain, imagesDir: e.dataDir + "/images"}
	}
	w.a = mk("A", "xalpha", e.token)
	w.b = mk("B", "xbeta", tokB)
	return w
}
