package httpapi

import (
	"net/http"

	"shop/internal/auth"
	"shop/internal/version"
)

// ServerInfo is the body of GET /v1/server-info (see openapi.yaml and notes/hosted-api-contract.md).
// Unset limits are written as null, as the contract shows. A hosted deployment adds Billing and Links.
type ServerInfo struct {
	Server         string          `json:"server"`
	Version        string          `json:"version"`
	APILevel       int             `json:"api_level"`
	Mode           string          `json:"mode"`
	LoginMethods   []string        `json:"login_methods"`
	PairingEnabled bool            `json:"pairing_enabled"`
	MinAppBuild    int             `json:"min_app_build"`
	Features       map[string]bool `json:"features"`
	Limits         map[string]any  `json:"limits"`
	Billing        any             `json:"billing,omitempty"`
	Links          any             `json:"links,omitempty"`
}

var limitKeys = []string{"household_members", "max_upload_bytes", "receipts_per_month", "receipts_per_month_soft",
	"receipts_per_day", "storage_bytes", "max_queued_jobs", "exports_per_day", "open_invites"}

// NoLimits is a limits object where every limit is null.
func NoLimits() map[string]any {
	m := make(map[string]any, len(limitKeys))
	for _, k := range limitKeys {
		m[k] = nil
	}
	return m
}

// SingleServerInfo is the answer of a self-hosted server. pairing says whether a pairing password is set.
func SingleServerInfo(pairing bool) ServerInfo {
	methods := []string{}
	if pairing {
		methods = append(methods, "pairing_password")
	}
	limits := NoLimits()
	limits["max_upload_bytes"] = int64(maxUpload)
	return ServerInfo{
		Server: "shop", Version: version.Version, APILevel: version.APILevel, Mode: "single",
		LoginMethods: methods, PairingEnabled: pairing, MinAppBuild: version.MinAppBuild,
		Features: map[string]bool{"households": false, "billing": false, "account_deletion": false, "async_export": false, "mcp": true},
		Limits:   limits,
	}
}

func writeServerInfo(w http.ResponseWriter, info ServerInfo) {
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) serverInfo(w http.ResponseWriter, r *http.Request) {
	pairing, err := auth.HasPassword(r.Context(), s.q)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeServerInfo(w, SingleServerInfo(pairing))
}
