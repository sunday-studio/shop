package hostedapi

import "testing"

func TestLogPathHidesInviteCodes(t *testing.T) {
	for in, want := range map[string]string{
		"/v1/household/invites/K7PQ-4MXD-93AB":        "/v1/household/invites/{code}",
		"/v1/household/invites/K7PQ-4MXD-93AB/accept": "/v1/household/invites/{code}/accept",
		"/v1/household/invites/":                      "/v1/household/invites/",
		"/v1/household/invites":                       "/v1/household/invites",
		"/v1/receipts/12":                             "/v1/receipts/12",
	} {
		if got := logPath(in); got != want {
			t.Errorf("logPath(%q) = %q, want %q", in, got, want)
		}
	}
}
