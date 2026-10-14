package composeguard

import (
	"regexp"
	"strings"
	"testing"
)

// The hosted kit must run the hosted binary with a read-only root, publish no admin port, give the
// off-site service no household data, and ship the sandbox switch.
func TestHostedKitRunsTheHostedServerHardened(t *testing.T) {
	s := read(t, "deploy/hosted/docker-compose.yml")
	api := regexp.MustCompile(`(?ms)^  api:\n(.*?)^  \S`).FindStringSubmatch(s)
	if api == nil {
		t.Fatal("api service not found in deploy/hosted/docker-compose.yml")
	}
	for _, must := range []string{
		`entrypoint: ["/usr/local/bin/shop-hosted"]`,
		`command: ["serve"]`,
		"read_only: true",
		"tmpfs:",
		"SHOP_MODE: hosted",
		"ADMIN_ADDR: 127.0.0.1:8081",
		"SHOP_CONTROL_DB: /data/control.db",
		"SHOP_APPLE_ACCEPT_SANDBOX: ${SHOP_APPLE_ACCEPT_SANDBOX:-true}",
		`"/usr/local/bin/shop-hosted", "healthcheck"`,
	} {
		if !strings.Contains(api[1], must) {
			t.Errorf("hosted kit api service lost %q", must)
		}
	}
	if regexp.MustCompile(`(?m)^\s+ports:`).MatchString(api[1]) {
		t.Error("the api service must publish no port (the admin API is loopback inside the container)")
	}
	if strings.Contains(api[1], "/usr/local/bin/shop\"") {
		t.Error("the api service must not run the single-household binary")
	}
	if strings.Contains(api[1], "SHOP_APPLE_MODE") || strings.Contains(s, "SHOP_APPLE_FAKE_ACK") {
		t.Error("the production compose file must never mention the fake Apple mode")
	}
	off := regexp.MustCompile(`(?ms)^  offsite:\n(.*?)^  \S`).FindStringSubmatch(s)
	if off == nil || strings.Contains(off[1], "shop-data") {
		t.Error("the offsite service must not mount the household data volume")
	}
	for _, f := range []string{"deploy/hosted/.env.example"} {
		e := read(t, f)
		for _, must := range []string{"SHOP_APPLE_ACCEPT_SANDBOX=true", "SHOP_APPLE_SANDBOX_MAX_DAYS=30", "SHOP_APPLE_TEAM_ID"} {
			if !strings.Contains(e, must) {
				t.Errorf("%s lost %q", f, must)
			}
		}
	}
}
