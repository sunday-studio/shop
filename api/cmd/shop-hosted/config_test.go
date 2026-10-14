package main

import (
	"net/http"
	"testing"
	"time"
)

func TestProductionRefusesAppleEndpointOverrides(t *testing.T) {
	t.Setenv("SHOP_DATA_DIR", t.TempDir())
	t.Setenv("SHOP_APPLE_ENVIRONMENT", "production")
	if _, err := loadConfig(); err != nil {
		t.Fatalf("plain production config: %v", err)
	}
	for _, name := range []string{"SHOP_APPLE_JWKS_URL", "SHOP_APPLE_SIWA_URL", "SHOP_APPLE_STOREKIT_URL", "SHOP_APPLE_STOREKIT_SANDBOX_URL"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "https://evil.example.com")
			if _, err := loadConfig(); err == nil {
				t.Fatalf("%s was accepted with SHOP_APPLE_ENVIRONMENT=production", name)
			}
			// a non-production environment may point at a fake
			t.Setenv("SHOP_APPLE_ENVIRONMENT", "sandbox")
			if _, err := loadConfig(); err != nil {
				t.Fatalf("sandbox with %s: %v", name, err)
			}
		})
	}
}

func TestHTTPServerHasTimeouts(t *testing.T) {
	s := newHTTPServer(":0", http.NotFoundHandler())
	if s.ReadHeaderTimeout <= 0 || s.ReadTimeout <= 0 || s.IdleTimeout <= 0 || s.MaxHeaderBytes <= 0 {
		t.Fatalf("missing timeouts: %+v", s)
	}
	if s.ReadTimeout > 10*time.Minute || s.WriteTimeout != 0 {
		t.Fatalf("read %v write %v", s.ReadTimeout, s.WriteTimeout)
	}
}
