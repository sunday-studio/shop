package hostedapi_test

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	w "shop/internal/hosted/hostedworld"
)

func zipText(t *testing.T, body []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			return string(b)
		}
	}
	t.Fatalf("%s not in export", name)
	return ""
}

// An export holds its requester's own account data and the household with display names only. A
// member cannot read an export another member asked for, and nobody sees other members' email or code.
func TestExportShowsMembersOnlyTheirOwnAccountData(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	kofi := world.SignIn("sub-kofi", "Kofi")
	inv := world.Do("POST", "/v1/household/invites", ama.Token, nil)
	want(t, world.Do("POST", "/v1/household/invites/"+inv.JSON()["code"].(string)+"/accept", kofi.Token, nil), 200, "")

	ex := world.Do("POST", "/v1/account/export", ama.Token, nil)
	want(t, ex, 202, "")
	id := ex.JSON()["id"].(string)
	// the member neither sees nor downloads the owner's job and may start their own
	want(t, world.Do("GET", "/v1/account/export", kofi.Token, nil), 404, "not_found")
	want(t, world.Do("GET", "/v1/account/export/"+id+"/download", kofi.Token, nil), 404, "not_found")
	own := world.Do("POST", "/v1/account/export", kofi.Token, nil)
	want(t, own, 202, "")
	world.Hosted.RunExports(world.Ctx)

	get := func(u w.User, jobID string) []byte {
		d := world.Do("GET", "/v1/account/export/"+jobID+"/download", u.Token, nil)
		want(t, d, 200, "")
		return d.Body.Bytes()
	}
	kofiZip := get(kofi, own.JSON()["id"].(string))
	ownerZip := get(ama, id)
	want(t, world.Do("GET", "/v1/account/export/"+own.JSON()["id"].(string)+"/download", ama.Token, nil), 404, "not_found")

	for name, c := range map[string]struct {
		zip  []byte
		self w.User
		them w.User
	}{"member": {kofiZip, kofi, ama}, "owner": {ownerZip, ama, kofi}} {
		acc := zipText(t, c.zip, "account.json")
		hh := zipText(t, c.zip, "household.json")
		if !strings.Contains(acc, c.self.Account["support_code"].(string)) {
			t.Fatalf("%s: own support code missing: %s", name, acc)
		}
		for _, f := range []string{acc, hh} {
			if strings.Contains(f, c.them.Account["support_code"].(string)) || strings.Contains(f, c.them.Account["id"].(string)) {
				t.Fatalf("%s export shows the other member's code or id: %s", name, f)
			}
		}
		if !strings.Contains(hh, "Ama") || !strings.Contains(hh, "Kofi") || strings.Contains(hh, "email") {
			t.Fatalf("%s: household.json: %s", name, hh)
		}
	}
}
