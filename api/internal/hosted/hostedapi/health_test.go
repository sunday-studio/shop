package hostedapi_test

import (
	"testing"

	w "shop/internal/hosted/hostedworld"
)

func TestPublicHealthzShowsOnlyTheStatus(t *testing.T) {
	world := w.New(t, w.NoRates)
	pub := world.DoFrom("203.0.113.9:4000", "GET", "/healthz", "", nil)
	if j := pub.JSON(); j["status"] == nil || len(j) != 1 {
		t.Fatalf("public /healthz must hold only the status: %s", pub.Body)
	}
	// through the proxy even a loopback peer is a stranger
	via := world.DoFrom("127.0.0.1:4000", "GET", "/healthz", "", nil, "X-Forwarded-For", "203.0.113.9")
	if len(via.JSON()) != 1 {
		t.Fatalf("proxied request saw details: %s", via.Body)
	}
	local := world.DoFrom("127.0.0.1:4000", "GET", "/healthz", "", nil)
	if local.JSON()["checks"] == nil {
		t.Fatalf("a direct loopback caller should see the details: %s", local.Body)
	}
	adm := world.DoAdmin("GET", "/admin/v1/health", nil)
	if adm.JSON()["checks"] == nil {
		t.Fatalf("admin health has no details: %s", adm.Body)
	}
}
