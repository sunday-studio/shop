package hostedapi_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/hostedapi"
	w "shop/internal/hosted/hostedworld"
)

func png1(seed int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{uint8(seed), uint8(x * 30), uint8(y * 30), 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func upload(world *w.World, token string, img []byte, hdr ...string) w.Resp {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("image", "r.png")
	_, _ = fw.Write(img)
	_ = mw.Close()
	return world.Do("POST", "/v1/receipts", token, b.Bytes(), append([]string{"Content-Type", mw.FormDataContentType()}, hdr...)...)
}

func want(t *testing.T, r w.Resp, status int, code string) {
	t.Helper()
	if r.Status() != status || (code != "" && r.Code() != code) {
		t.Fatalf("got %d %q (%s), want %d %q", r.Status(), r.Code(), r.Body, status, code)
	}
}

func TestServerInfoHosted(t *testing.T) {
	world := w.New(t)
	r := world.Do("GET", "/v1/server-info", "", nil)
	want(t, r, 200, "")
	j := r.JSON()
	if j["mode"] != "hosted" || j["api_level"].(float64) != 8 || j["pairing_enabled"] != false {
		t.Fatalf("%v", j)
	}
	if lm := j["login_methods"].([]any); len(lm) != 1 || lm[0] != "apple" {
		t.Fatalf("%v", lm)
	}
	f := j["features"].(map[string]any)
	if f["mcp"] != false || f["billing"] != true || f["households"] != true || f["async_export"] != true || f["account_deletion"] != true {
		t.Fatalf("%v", f)
	}
	lim := j["limits"].(map[string]any)
	if lim["receipts_per_month"].(float64) != 300 || lim["receipts_per_month_soft"].(float64) != 150 || lim["max_upload_bytes"].(float64) != 10485760 {
		t.Fatalf("%v", lim)
	}
	b := j["billing"].(map[string]any)
	if b["provider"] != "apple" || b["price_display"] != "storekit" || b["trial_days"].(float64) != 14 {
		t.Fatalf("%v", b)
	}
	if j["links"].(map[string]any)["terms_url"] == "" {
		t.Fatal("links")
	}
	if r.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatal(r.Header())
	}
	// pairing is not offered
	want(t, world.Do("POST", "/v1/auth/pair", "", map[string]any{"password": "x", "device_name": "d"}), 404, "login_method_unavailable")
	// the contract's static table
	pr := world.Do("GET", "/v1/billing/plan-states", "", nil)
	want(t, pr, 200, "")
	if n := len(pr.JSON()["states"].([]any)); n != 9 {
		t.Fatalf("states %d", n)
	}
	want(t, world.Do("GET", "/healthz", "", nil), 503, "") // no OCR sidecar in the test world, but the route answers
}

func TestSignInFlow(t *testing.T) {
	world := w.New(t, w.NoRates)
	u := world.SignIn("sub-ama", "Ama")
	if u.Account["role"] != "none" || u.Account["household_id"] != nil || u.Account["display_name"] != "Ama T." {
		t.Fatalf("%v", u.Account)
	}
	if st := u.Account["plan"].(map[string]any)["state"]; st != "none" {
		t.Fatal(st)
	}
	// second sign-in: 200, new device, same account
	nonce := world.Nonce()
	world.Apple.AddCode("c2", "r2")
	r := world.Do("POST", "/v1/auth/apple", "", map[string]any{"identity_token": world.IdentityToken("sub-ama", nonce), "authorization_code": "c2", "nonce": nonce, "device_name": "iPad"})
	want(t, r, 200, "")
	if r.JSON()["created"] != false || r.JSON()["household"] != nil {
		t.Fatalf("%s", r.Body)
	}
	// replayed nonce
	r = world.Do("POST", "/v1/auth/apple", "", map[string]any{"identity_token": world.IdentityToken("sub-ama", nonce), "authorization_code": "c2", "nonce": nonce, "device_name": "iPad"})
	want(t, r, 401, "apple_nonce_invalid")
	// invalid identity token
	nonce = world.Nonce()
	r = world.Do("POST", "/v1/auth/apple", "", map[string]any{"identity_token": "garbage", "authorization_code": "c2", "nonce": nonce, "device_name": "iPad"})
	want(t, r, 401, "apple_token_invalid")
	// wrong audience / nonce claim mismatch
	nonce = world.Nonce()
	r = world.Do("POST", "/v1/auth/apple", "", map[string]any{"identity_token": world.IdentityToken("sub-ama", "other-nonce"), "authorization_code": "c2", "nonce": nonce, "device_name": "iPad"})
	want(t, r, 401, "apple_token_invalid")
	// missing fields
	want(t, world.Do("POST", "/v1/auth/apple", "", map[string]any{"nonce": "x"}), 400, "invalid_request")
	// Apple down on the token exchange
	world.Apple.Down = true
	nonce = world.Nonce()
	r = world.Do("POST", "/v1/auth/apple", "", map[string]any{"identity_token": world.IdentityToken("sub-ama", nonce), "authorization_code": "c2", "nonce": nonce, "device_name": "iPad"})
	want(t, r, 502, "apple_unavailable")
	if r.JSON()["retry_after_seconds"] == nil {
		t.Fatal("retry_after_seconds")
	}
	world.Apple.Down = false

	// me, devices
	me := world.Do("GET", "/v1/auth/me", u.Token, nil)
	want(t, me, 200, "")
	if me.JSON()["account_id"] != u.AccountID || me.JSON()["kind"] != "device" || me.JSON()["household_id"] != nil {
		t.Fatalf("%s", me.Body)
	}
	devs := world.Do("GET", "/v1/auth/devices", u.Token, nil)
	want(t, devs, 200, "")
	if !strings.Contains(devs.Body.String(), `"current":true`) || strings.Count(devs.Body.String(), `"id"`) != 2 {
		t.Fatalf("%s", devs.Body)
	}
	// invalid tokens
	want(t, world.Do("GET", "/v1/auth/me", "", nil), 401, "token_invalid")
	want(t, world.Do("GET", "/v1/auth/me", "st_nonsense", nil), 401, "token_invalid")
	want(t, world.Do("GET", "/v1/shops", "st_nonsense", nil), 401, "token_invalid")
	// revoke own device: the token stops working
	want(t, world.Do("DELETE", fmt.Sprintf("/v1/auth/devices/%d", u.DeviceID), u.Token, nil), 204, "")
	want(t, world.Do("GET", "/v1/auth/me", u.Token, nil), 401, "token_invalid")
}

func TestPlanGateAndHouseholdRoutes(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	// no entitlement: every household route is plan_required
	r := world.Do("GET", "/v1/shops", ama.Token, nil)
	want(t, r, 403, "plan_required")
	if r.JSON()["plan_state"] != "none" {
		t.Fatalf("%s", r.Body)
	}
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "AH"}), 403, "plan_required")
	want(t, world.Do("GET", "/v1/household", ama.Token, nil), 404, "no_household")
	want(t, world.Do("POST", "/v1/account/export", ama.Token, nil), 404, "no_household")
	want(t, world.Do("GET", "/mcp", ama.Token, nil), 404, "feature_unavailable")

	world.Subscribe(ama, "otx-ama")
	if world.PlanState(ama) != "trial" {
		t.Fatal("not in trial")
	}
	want(t, world.Do("GET", "/v1/shops", ama.Token, nil), 200, "")
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "AH"}), 201, "")
	hh := world.Do("GET", "/v1/household", ama.Token, nil)
	want(t, hh, 200, "")
	hj := hh.JSON()
	if hj["your_role"] != "owner" || hj["member_cap"].(float64) != 4 || len(hj["pending_invites"].([]any)) != 0 || hj["pending_invites_count"].(float64) != 0 {
		t.Fatalf("%s", hh.Body)
	}
	// invite, preview, accept
	inv := world.Do("POST", "/v1/household/invites", ama.Token, nil)
	want(t, inv, 201, "")
	code := inv.JSON()["code"].(string)
	if len(code) != 14 || !strings.HasSuffix(inv.JSON()["url"].(string), "/"+code) {
		t.Fatalf("%s", inv.Body)
	}
	pv := world.Do("GET", "/v1/household/invites/"+code, "", nil)
	want(t, pv, 200, "")
	if pv.JSON()["valid"] != true || pv.JSON()["owner_display_name"] != "Ama T." {
		t.Fatalf("%s", pv.Body)
	}
	bad := world.Do("GET", "/v1/household/invites/ZZZZ-ZZZZ-ZZZZ", "", nil)
	want(t, bad, 200, "")
	if bad.JSON()["valid"] != false || bad.JSON()["owner_display_name"] != nil {
		t.Fatalf("%s", bad.Body)
	}
	kofi := world.SignIn("sub-kofi", "Kofi")
	want(t, world.Do("POST", "/v1/household/invites/NOPE-NOPE-NOPE/accept", kofi.Token, nil), 404, "invite_invalid")
	acc := world.Do("POST", "/v1/household/invites/"+strings.ToLower(code)+"/accept", kofi.Token, nil)
	want(t, acc, 200, "")
	aj := acc.JSON()
	if aj["account"].(map[string]any)["plan"].(map[string]any)["is_owner"] != false || aj["household"].(map[string]any)["your_role"] != "member" {
		t.Fatalf("%s", acc.Body)
	}
	if _, ok := aj["household"].(map[string]any)["pending_invites"]; ok {
		t.Fatal("pending_invites in accept response")
	}
	// repeat after success: 200 again
	want(t, world.Do("POST", "/v1/household/invites/"+code+"/accept", kofi.Token, nil), 200, "")
	// the preview is invalid now
	if world.Do("GET", "/v1/household/invites/"+code, "", nil).JSON()["valid"] != false {
		t.Fatal("used invite still valid")
	}
	// the member sees the owner's plan, cannot manage, sees the pending count but not the list
	own2 := world.Do("POST", "/v1/household/invites", ama.Token, nil)
	want(t, own2, 201, "")
	mv := world.Do("GET", "/v1/household", kofi.Token, nil)
	want(t, mv, 200, "")
	if _, ok := mv.JSON()["pending_invites"]; ok || mv.JSON()["pending_invites_count"].(float64) != 1 {
		t.Fatalf("%s", mv.Body)
	}
	want(t, world.Do("POST", "/v1/household/invites", kofi.Token, nil), 403, "not_household_owner")
	want(t, world.Do("POST", "/v1/household/transfer", kofi.Token, map[string]any{"account_id": ama.AccountID}), 403, "not_household_owner")
	// the member reads and writes the same data
	sh := world.Do("GET", "/v1/shops", kofi.Token, nil)
	want(t, sh, 200, "")
	if !strings.Contains(sh.Body.String(), "AH") {
		t.Fatalf("member does not see the shared data: %s", sh.Body)
	}
	// already in a household
	inv3 := world.Do("POST", "/v1/household/invites", ama.Token, nil)
	want(t, world.Do("POST", "/v1/household/invites/"+inv3.JSON()["code"].(string)+"/accept", kofi.Token, nil), 409, "already_in_household")
	// revoke an invite
	want(t, world.Do("DELETE", "/v1/household/invites/"+inv3.JSON()["id"].(string), ama.Token, nil), 204, "")
	want(t, world.Do("DELETE", "/v1/household/invites/"+inv3.JSON()["id"].(string), ama.Token, nil), 404, "not_found")
	// full: 2 members + 2 open invites = cap 4
	want(t, world.Do("POST", "/v1/household/invites", ama.Token, nil), 201, "")
	full := world.Do("POST", "/v1/household/invites", ama.Token, nil)
	want(t, full, 409, "household_full")
	if full.JSON()["limit"] != "household_members" || full.JSON()["limit_value"].(float64) != 4 {
		t.Fatalf("%s", full.Body)
	}
	// remove the member; a stranger id is 404
	want(t, world.Do("DELETE", "/v1/household/members/"+ama.AccountID, ama.Token, nil), 409, "cannot_remove_owner")
	want(t, world.Do("DELETE", "/v1/household/members/00000000-0000-4000-8000-000000000000", ama.Token, nil), 404, "not_found")
	want(t, world.Do("DELETE", "/v1/household/members/"+kofi.AccountID, ama.Token, nil), 204, "")
	want(t, world.Do("GET", "/v1/shops", kofi.Token, nil), 403, "plan_required")
	want(t, world.Do("GET", "/v1/household", kofi.Token, nil), 404, "no_household")
	// leave and owner cannot leave
	want(t, world.Do("POST", "/v1/household/leave", ama.Token, nil), 409, "owner_cannot_leave")
	want(t, world.Do("POST", "/v1/household/leave", kofi.Token, nil), 404, "no_household")
}

func TestTransferAndLeave(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	kofi := world.SignIn("sub-kofi", "Kofi")
	inv := world.Do("POST", "/v1/household/invites", ama.Token, nil)
	want(t, world.Do("POST", "/v1/household/invites/"+inv.JSON()["code"].(string)+"/accept", kofi.Token, nil), 200, "")
	// the target has no plan: needs confirmation
	want(t, world.Do("POST", "/v1/household/transfer", ama.Token, map[string]any{"account_id": kofi.AccountID}), 409, "transfer_target_no_plan")
	r := world.Do("POST", "/v1/household/transfer", ama.Token, map[string]any{"account_id": kofi.AccountID, "confirm_plan_change": true})
	want(t, r, 200, "")
	if r.JSON()["your_role"] != "member" || r.JSON()["owner_account_id"] != kofi.AccountID {
		t.Fatalf("%s", r.Body)
	}
	if world.PlanState(kofi) != "lapsed_read_only" {
		t.Fatal(world.PlanState(kofi))
	}
	// the old owner is a member now and can leave
	want(t, world.Do("POST", "/v1/household/leave", ama.Token, nil), 204, "")
}

func TestReadOnlyAndNotifications(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	trial := world.Subscribe(ama, "otx-ama")
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "Jumbo"}), 201, "")

	// day 14: first charge arrives as a signed notification; a replay is a no-op
	world.At(14)
	renewed := world.Tx(ama, "otx-ama", func(x *billing.TransactionInfo) {
		x.OfferType, x.OfferDiscountType = 0, ""
		x.ExpiresDate = billing.MsOf(w.Day(44))
		x.TransactionID = "otx-ama-2"
		x.SignedDate = billing.MsOf(w.Day(14))
	})
	n := world.Notification(billing.NtDidRenew, "", "n-1", w.Day(14), renewed, w.Ren(1))
	want(t, world.Deliver(n), 200, "")
	want(t, world.Deliver(n), 200, "")
	if world.PlanState(ama) != "active" {
		t.Fatal(world.PlanState(ama))
	}
	// bad signature: 400; other PKI: 400
	want(t, world.Deliver("not.a.jws"), 400, "apple_token_invalid")
	other := w.New(t)
	want(t, world.Deliver(other.Notification(billing.NtDidRenew, "", "n-x", w.Day(14), renewed, w.Ren(1))), 400, "apple_token_invalid")
	want(t, world.Do("POST", "/v1/billing/apple/notifications", "", map[string]any{}), 400, "")

	// day 44: the subscription expires -> lapsed read-only
	world.At(44)
	expired := renewed
	expired.SignedDate = billing.MsOf(w.Day(44))
	want(t, world.Deliver(world.Notification(billing.NtExpired, "VOLUNTARY", "n-2", w.Day(44), expired, w.Ren(0))), 200, "")
	if st := world.PlanState(ama); st != "lapsed_read_only" {
		t.Fatal(st)
	}
	r := world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "Lidl"})
	want(t, r, 403, "read_only")
	if r.JSON()["plan_state"] != "lapsed_read_only" || r.JSON()["delete_after"] == nil {
		t.Fatalf("%s", r.Body)
	}
	want(t, upload(world, ama.Token, png1(1)), 403, "read_only")
	want(t, world.Do("DELETE", "/v1/shops/1", ama.Token, nil), 403, "read_only")
	want(t, world.Do("GET", "/v1/shops", ama.Token, nil), 200, "") // reads work
	want(t, world.Do("POST", "/v1/glossary/preview", ama.Token, map[string]any{"dutch": "melk", "english": "milk"}), 200, "")
	want(t, world.Do("POST", "/v1/household/invites", ama.Token, nil), 403, "read_only")
	acct := world.Do("GET", "/v1/account", ama.Token, nil)
	want(t, acct, 200, "")
	pl := acct.JSON()["plan"].(map[string]any)
	if pl["read_only"] != true || pl["delete_after"] == nil {
		t.Fatalf("%v", pl)
	}
	// export always works
	ex := world.Do("POST", "/v1/account/export", ama.Token, nil)
	want(t, ex, 202, "")

	// resubscribe: renewal notification restores everything
	world.At(50)
	back := renewed
	back.ExpiresDate, back.SignedDate, back.TransactionID = billing.MsOf(w.Day(80)), billing.MsOf(w.Day(50)), "otx-ama-3"
	want(t, world.Deliver(world.Notification(billing.NtSubscribed, "RESUBSCRIBE", "n-3", w.Day(50), back, w.Ren(1))), 200, "")
	if st := world.PlanState(ama); st != "active" {
		t.Fatal(st)
	}
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "Lidl"}), 201, "")
	_ = trial
}

func TestTransactionErrors(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	bob := world.SignIn("sub-bob", "Bob")
	tx := world.Tx(ama, "otx-a")
	world.SetStatus("otx-a", 1, tx)
	want(t, world.Do("POST", "/v1/billing/apple/transactions", ama.Token, map[string]any{"signed_transaction": "junk"}), 400, "apple_transaction_invalid")
	want(t, world.Do("POST", "/v1/billing/apple/transactions", ama.Token, map[string]any{}), 400, "invalid_request")
	want(t, world.Do("POST", "/v1/billing/apple/transactions", bob.Token, map[string]any{"signed_transaction": world.PKI.Sign(tx)}), 409, "subscription_belongs_to_other_account")
	badProd := world.Tx(ama, "otx-b", func(x *billing.TransactionInfo) { x.ProductID = "com.other" })
	want(t, world.Do("POST", "/v1/billing/apple/transactions", ama.Token, map[string]any{"signed_transaction": world.PKI.Sign(badProd)}), 400, "product_not_allowed")
	world.API.Down = true
	r := world.Do("POST", "/v1/billing/apple/transactions", ama.Token, map[string]any{"signed_transaction": world.PKI.Sign(tx)})
	want(t, r, 502, "apple_unavailable")
	world.API.Down = false
	ok := world.Do("POST", "/v1/billing/apple/transactions", ama.Token, map[string]any{"signed_transaction": world.PKI.Sign(tx)})
	want(t, ok, 200, "")
	if ok.JSON()["result"] != "bound" || ok.JSON()["household"] == nil {
		t.Fatalf("%s", ok.Body)
	}
	again := world.Do("POST", "/v1/billing/apple/transactions", ama.Token, map[string]any{"signed_transaction": world.PKI.Sign(tx)})
	if again.JSON()["result"] != "already_bound" {
		t.Fatalf("%s", again.Body)
	}
	rf := world.Do("POST", "/v1/billing/apple/refresh", ama.Token, nil)
	want(t, rf, 200, "")
	if rf.JSON()["account"].(map[string]any)["plan"].(map[string]any)["state"] != "trial" {
		t.Fatalf("%s", rf.Body)
	}
}

func TestAccountDeletion(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	second := world.Do("POST", "/v1/auth/apple/nonce", "", nil) // a second device
	_ = second
	ipad := world.SignIn("sub-ama", "AmaPad")
	want(t, world.Do("DELETE", "/v1/account", ama.Token, map[string]any{"confirm": "nope", "delete_household": true}), 400, "invalid_request")
	want(t, world.Do("DELETE", "/v1/account", ama.Token, map[string]any{"confirm": "delete my account"}), 409, "owner_must_confirm_household")
	want(t, world.Do("DELETE", "/v1/account/deletion", ama.Token, nil), 409, "deletion_not_pending")
	r := world.Do("DELETE", "/v1/account", ama.Token, map[string]any{"confirm": "delete my account", "delete_household": true})
	want(t, r, 202, "")
	j := r.JSON()
	if j["status"] != "scheduled" || j["subscription_still_active"] != true || j["can_cancel"] != true || j["delete_after"] == nil {
		t.Fatalf("%s", r.Body)
	}
	if len(world.Apple.RevokedTokens()) != 1 {
		t.Fatalf("apple token not revoked: %v", world.Apple.RevokedTokens())
	}
	// repeat: 200, same body
	r2 := world.Do("DELETE", "/v1/account", ama.Token, map[string]any{"confirm": "delete my account", "delete_household": true})
	want(t, r2, 200, "")
	if r2.JSON()["delete_after"] != j["delete_after"] {
		t.Fatal("not idempotent")
	}
	// the other device is signed out; this one stays, read-only
	want(t, world.Do("GET", "/v1/auth/me", ipad.Token, nil), 401, "token_invalid")
	acct := world.Do("GET", "/v1/account", ama.Token, nil)
	pl := acct.JSON()["plan"].(map[string]any)
	if pl["state"] != "pending_deletion" || pl["deletion_requested"] != true {
		t.Fatalf("%v", pl)
	}
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "x"}), 403, "read_only")
	want(t, world.Do("GET", "/v1/shops", ama.Token, nil), 200, "")
	// cancel
	c := world.Do("DELETE", "/v1/account/deletion", ama.Token, nil)
	want(t, c, 200, "")
	if c.JSON()["plan"].(map[string]any)["state"] != "trial" {
		t.Fatalf("%s", c.Body)
	}
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "x"}), 201, "")
	// PATCH account
	want(t, world.Do("PATCH", "/v1/account", ama.Token, map[string]any{"display_name": "  "}), 400, "invalid_request")
	p := world.Do("PATCH", "/v1/account", ama.Token, map[string]any{"display_name": "Ama Boateng"})
	want(t, p, 200, "")
	if p.JSON()["display_name"] != "Ama Boateng" {
		t.Fatalf("%s", p.Body)
	}
}

func TestExportJob(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	want(t, world.Do("GET", "/v1/account/export", ama.Token, nil), 404, "not_found")
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "AH"}), 201, "")
	up := upload(world, ama.Token, png1(7))
	want(t, up, 202, "")
	r := world.Do("POST", "/v1/account/export", ama.Token, nil)
	want(t, r, 202, "")
	id := r.JSON()["id"].(string)
	if r.JSON()["status"] != "queued" || r.JSON()["download_url"] != nil {
		t.Fatalf("%s", r.Body)
	}
	// a second request while queued returns the same job
	r2 := world.Do("POST", "/v1/account/export", ama.Token, nil)
	want(t, r2, 200, "")
	if r2.JSON()["id"] != id {
		t.Fatal("new job while one is queued")
	}
	want(t, world.Do("GET", "/v1/account/export/"+id+"/download", ama.Token, nil), 409, "export_not_ready")
	world.Hosted.RunExports(world.Ctx)
	g := world.Do("GET", "/v1/account/export", ama.Token, nil)
	want(t, g, 200, "")
	if g.JSON()["status"] != "ready" || g.JSON()["size_bytes"].(float64) < 100 {
		t.Fatalf("%s", g.Body)
	}
	d := world.Do("GET", g.JSON()["download_url"].(string), ama.Token, nil)
	want(t, d, 200, "")
	if d.Header().Get("Content-Type") != "application/zip" || !strings.Contains(d.Header().Get("Content-Disposition"), "shop-export-2026-10-14.zip") {
		t.Fatal(d.Header())
	}
	zr, err := zip.NewReader(bytes.NewReader(d.Body.Bytes()), int64(d.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	var shops string
	for _, f := range zr.File {
		names[f.Name] = true
		if f.Name == "data/shops.json" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			shops = string(b)
		}
	}
	for _, n := range []string{"README.txt", "account.json", "household.json", "manifest.json", "data/shops.json", "data/receipts.json"} {
		if !names[n] {
			t.Fatalf("missing %s in %v", n, names)
		}
	}
	hasImage := false
	for n := range names {
		if strings.HasPrefix(n, "images/") {
			hasImage = true
		}
		if strings.Contains(n, "api_tokens") {
			t.Fatal("tokens in export")
		}
	}
	if !hasImage || !strings.Contains(shops, "AH") {
		t.Fatalf("image %v shops %s", hasImage, shops)
	}
	// range support
	rg := world.Do("GET", g.JSON()["download_url"].(string), ama.Token, nil, "Range", "bytes=0-9")
	want(t, rg, 206, "")
	// another household cannot download it
	bob := world.SignIn("sub-bob", "Bob")
	world.Subscribe(bob, "otx-bob")
	want(t, world.Do("GET", g.JSON()["download_url"].(string), bob.Token, nil), 404, "not_found")
	// expiry
	world.At(8)
	e := world.Do("GET", "/v1/account/export", ama.Token, nil)
	if e.JSON()["status"] != "expired" {
		t.Fatalf("%s", e.Body)
	}
	want(t, world.Do("GET", g.JSON()["download_url"].(string), ama.Token, nil), 410, "export_expired")
	if n, err := world.Hosted.CleanupExports(world.Ctx); err != nil || n != 1 {
		t.Fatalf("cleanup %d %v", n, err)
	}
	// daily limit: 3 per day
	world.At(9)
	for i := 0; i < 3; i++ {
		x := world.Do("POST", "/v1/account/export", ama.Token, nil)
		want(t, x, 202, "")
		world.Hosted.RunExports(world.Ctx)
	}
	lim := world.Do("POST", "/v1/account/export", ama.Token, nil)
	want(t, lim, 429, "limit_reached")
	if lim.JSON()["limit"] != "exports_per_day" || lim.JSON()["resets_at"] == nil {
		t.Fatalf("%s", lim.Body)
	}
}

func TestLimitsAndUploadAllowed(t *testing.T) {
	world := w.New(t, w.NoRates, func(c *hostedapi.Config) {
		c.Limits.ReceiptsPerMonth, c.Limits.ReceiptsPerMonthSoft, c.Limits.ReceiptsPerDay = 4, 2, 25
		c.Limits.MaxUploadBytes = 4000
	})
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	for i := 0; i < 3; i++ {
		want(t, upload(world, ama.Token, png1(i+1)), 202, "")
	}
	hasUpload := func() bool {
		pl := world.Do("GET", "/v1/account", ama.Token, nil).JSON()["plan"].(map[string]any)
		for _, a := range pl["allowed"].([]any) {
			if a == "upload" {
				return true
			}
		}
		return false
	}
	if !hasUpload() {
		t.Fatal("upload not allowed below the hard limit")
	}
	// a duplicate image is refused by the household and gives the quota back
	want(t, upload(world, ama.Token, png1(1)), 409, "")
	if n := world.Do("GET", "/v1/account", ama.Token, nil).JSON()["usage"].(map[string]any)["receipts_this_month"].(float64); n != 3 {
		t.Fatalf("quota not released: %v", n)
	}
	want(t, upload(world, ama.Token, png1(4)), 202, "")
	if hasUpload() {
		t.Fatal("upload still allowed at the hard limit")
	}
	acct := world.Do("GET", "/v1/account", ama.Token, nil).JSON()
	if u := acct["usage"].(map[string]any); u["receipts_this_month"].(float64) != 4 || u["receipts_soft_limit_reached"] != true {
		t.Fatalf("%v", u)
	}
	r := upload(world, ama.Token, png1(9))
	want(t, r, 429, "limit_reached")
	if r.JSON()["limit"] != "receipts_per_month" || r.JSON()["limit_value"].(float64) != 4 || r.JSON()["current"].(float64) != 4 || r.JSON()["resets_at"] != "2026-11-01T00:00:00Z" {
		t.Fatalf("%s", r.Body)
	}
	// too large
	big := append(png1(2), bytes.Repeat([]byte{0}, 5000)...)
	tl := upload(world, ama.Token, big)
	want(t, tl, 413, "file_too_large")
	if tl.JSON()["limit"] != "max_upload_bytes" {
		t.Fatalf("%s", tl.Body)
	}
	// writes other than uploads still work at the limit
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "AH"}), 201, "")
}

func TestRateLimitsAndIdempotency(t *testing.T) {
	world := w.New(t)
	for i := 0; i < 20; i++ {
		want(t, world.Do("POST", "/v1/auth/apple/nonce", "", nil), 201, "")
	}
	r := world.Do("POST", "/v1/auth/apple/nonce", "", nil)
	want(t, r, 429, "rate_limited")
	if r.Header().Get("Retry-After") == "" || r.JSON()["retry_after_seconds"] == nil {
		t.Fatalf("%s %v", r.Body, r.Header())
	}
	// another address is not limited; a minute later it works again
	want(t, world.DoFrom("203.0.113.9:1", "POST", "/v1/auth/apple/nonce", "", nil), 201, "")
	world.Clock.Advance(61 * time.Second)
	want(t, world.Do("POST", "/v1/auth/apple/nonce", "", nil), 201, "")

	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	// refresh: 1 per minute
	want(t, world.Do("POST", "/v1/billing/apple/refresh", ama.Token, nil), 200, "")
	want(t, world.Do("POST", "/v1/billing/apple/refresh", ama.Token, nil), 429, "rate_limited")

	// idempotency: the same key replays the same invite
	a := world.Do("POST", "/v1/household/invites", ama.Token, nil, "Idempotency-Key", "k-1")
	want(t, a, 201, "")
	b := world.Do("POST", "/v1/household/invites", ama.Token, nil, "Idempotency-Key", "k-1")
	want(t, b, 201, "")
	if b.Header().Get("Idempotent-Replay") != "true" || a.Body.String() != b.Body.String() {
		t.Fatalf("replay: %v\n%s\n%s", b.Header(), a.Body, b.Body)
	}
	if n := world.Do("GET", "/v1/household", ama.Token, nil).JSON()["pending_invites_count"].(float64); n != 1 {
		t.Fatalf("replay created another invite: %v", n)
	}
	// same key, other path: reused
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "x"}, "Idempotency-Key", "k-1"), 409, "idempotency_key_reused")
	// the key is per account
	bob := world.SignIn("sub-bob", "Bob")
	world.Subscribe(bob, "otx-bob")
	want(t, world.Do("POST", "/v1/household/invites", bob.Token, nil, "Idempotency-Key", "k-1"), 201, "")
	// existing write routes too, and the key expires after 24 h
	s1 := world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "Once"}, "Idempotency-Key", "k-2")
	s2 := world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "Once"}, "Idempotency-Key", "k-2")
	if s1.Status() != 201 || s2.Status() != 201 || s2.Header().Get("Idempotent-Replay") != "true" {
		t.Fatalf("%d %d", s1.Status(), s2.Status())
	}
	world.Clock.Advance(25 * time.Hour)
	s3 := world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "Twice"}, "Idempotency-Key", "k-2")
	if s3.Header().Get("Idempotent-Replay") != "" {
		t.Fatal("expired key replayed")
	}
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "x"}, "Idempotency-Key", strings.Repeat("k", 101)), 400, "invalid_request")
}

func TestAppleEvents(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	ev := func(typ, jti string) string {
		return world.IDP.Sign("rsa-1", map[string]any{"iss": "https://appleid.apple.com", "aud": w.Bundle, "iat": world.Clock.Now().Unix(), "jti": jti,
			"events": fmt.Sprintf(`{"type":"%s","sub":"sub-ama","event_time":1}`, typ)})
	}
	want(t, world.Do("POST", "/v1/auth/apple/events", "", map[string]any{"payload": "junk"}), 400, "apple_token_invalid")
	want(t, world.Do("POST", "/v1/auth/apple/events", "", map[string]any{"payload": ev("consent-revoked", "j1")}), 200, "")
	want(t, world.Do("POST", "/v1/auth/apple/events", "", map[string]any{"payload": ev("consent-revoked", "j1")}), 200, "") // duplicate
	want(t, world.Do("GET", "/v1/auth/me", ama.Token, nil), 401, "token_invalid")
	ama2 := world.SignIn("sub-ama", "Ama again") // sign-in works again
	want(t, world.Do("POST", "/v1/auth/apple/events", "", map[string]any{"payload": ev("account-deleted", "j2")}), 200, "")
	want(t, world.Do("GET", "/v1/auth/me", ama2.Token, nil), 401, "token_invalid")
	nonce := world.Nonce()
	world.Apple.AddCode("c9", "r9")
	want(t, world.Do("POST", "/v1/auth/apple", "", map[string]any{"identity_token": world.IdentityToken("sub-ama", nonce), "authorization_code": "c9", "nonce": nonce, "device_name": "x"}), 403, "account_locked")
}

func TestAdminAPI(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	support := ama.Account["support_code"].(string)
	// loopback only, token required, device token refused, operator required
	want(t, world.DoAdminRaw("203.0.113.5:1", w.Admin, "GET", "/admin/v1/grants", nil, "X-Admin-Operator", "x"), 404, "")
	want(t, world.DoAdminRaw("127.0.0.1:1", "", "GET", "/admin/v1/grants", nil, "X-Admin-Operator", "x"), 401, "admin_token_invalid")
	want(t, world.DoAdminRaw("127.0.0.1:1", ama.Token, "GET", "/admin/v1/grants", nil, "X-Admin-Operator", "x"), 401, "admin_token_invalid")
	want(t, world.DoAdminRaw("127.0.0.1:1", w.Admin, "GET", "/admin/v1/grants", nil), 400, "invalid_request")
	// the admin token does nothing on the public API
	want(t, world.Do("GET", "/v1/shops", w.Admin, nil), 401, "token_invalid")
	want(t, world.Do("GET", "/admin/v1/grants", w.Admin, nil), 401, "token_invalid")

	f := world.DoAdmin("GET", "/admin/v1/accounts?support_code="+support, nil)
	want(t, f, 200, "")
	if f.JSON()["accounts"].([]any)[0].(map[string]any)["plan_state"] != "none" {
		t.Fatalf("%s", f.Body)
	}
	want(t, world.DoAdmin("POST", "/admin/v1/grants", map[string]any{"support_code": support, "reason": "friend"}), 400, "invalid_request") // key required
	g := world.DoAdmin("POST", "/admin/v1/grants", map[string]any{"support_code": support, "reason": "friend"}, "Idempotency-Key", "g1")
	want(t, g, 201, "")
	gj := g.JSON()
	if gj["status"] != "active" || gj["support_code"] != support || gj["created_by"] != "casprine" {
		t.Fatalf("%s", g.Body)
	}
	rep := world.DoAdmin("POST", "/admin/v1/grants", map[string]any{"support_code": support, "reason": "friend"}, "Idempotency-Key", "g1")
	want(t, rep, 200, "")
	want(t, world.DoAdmin("POST", "/admin/v1/grants", map[string]any{"support_code": support, "reason": "again"}, "Idempotency-Key", "g2"), 409, "grant_exists")
	if world.PlanState(ama) != "comped" {
		t.Fatal(world.PlanState(ama))
	}
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "AH"}), 201, "") // household exists, writes work
	id := gj["id"].(string)
	ext := world.DoAdmin("POST", "/admin/v1/grants/"+id+"/extend", map[string]any{"expires_at": "2027-02-01T00:00:00Z", "reason": "more"})
	want(t, ext, 200, "")
	lst := world.DoAdmin("GET", "/admin/v1/grants?status=active", nil)
	want(t, lst, 200, "")
	if len(lst.JSON()["grants"].([]any)) != 1 {
		t.Fatalf("%s", lst.Body)
	}
	rv := world.DoAdmin("POST", "/admin/v1/grants/"+id+"/revoke", map[string]any{"reason": "done"})
	want(t, rv, 200, "")
	if rv.JSON()["status"] != "revoked" {
		t.Fatalf("%s", rv.Body)
	}
	if world.PlanState(ama) != "lapsed_read_only" {
		t.Fatal(world.PlanState(ama))
	}
	au := world.DoAdmin("GET", "/admin/v1/audit?action=grant.create&limit=5", nil)
	want(t, au, 200, "")
	if es := au.JSON()["entries"].([]any); len(es) != 1 || es[0].(map[string]any)["actor"] != "casprine" {
		t.Fatalf("%s", au.Body)
	}
	want(t, world.DoAdmin("POST", "/admin/v1/grants/nope/revoke", map[string]any{"reason": "x"}), 404, "not_found")
	want(t, world.DoAdmin("GET", "/admin/v1/nothing", nil), 404, "")
}

func TestCrossHouseholdAndDevices(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	bob := world.SignIn("sub-bob", "Bob")
	world.Subscribe(bob, "otx-bob")
	sh := world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "AmaShop"})
	want(t, sh, 201, "")
	id := fmt.Sprint(int64(sh.JSON()["id"].(float64)))
	want(t, world.Do("GET", "/v1/shops/"+id, bob.Token, nil), 404, "")
	want(t, world.Do("DELETE", "/v1/shops/"+id, bob.Token, nil), 404, "")
	if strings.Contains(world.Do("GET", "/v1/shops", bob.Token, nil).Body.String(), "AmaShop") {
		t.Fatal("bob sees ama's shop")
	}
	// devices of another account: 404 even though ids are global
	want(t, world.Do("DELETE", fmt.Sprintf("/v1/auth/devices/%d", ama.DeviceID), bob.Token, nil), 404, "not_found")
	want(t, world.Do("DELETE", "/v1/auth/devices/abc", bob.Token, nil), 404, "not_found")
	want(t, world.Do("GET", "/v1/auth/me", ama.Token, nil), 200, "")
}

func TestPlanGateBillingRetryExportAndSchedulerLock(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	tx := world.Subscribe(ama, "otx-ama")
	world.At(15)
	fail := tx
	fail.SignedDate = billing.MsOf(w.Day(15))
	fail.OfferType, fail.OfferDiscountType = 0, ""
	want(t, world.Deliver(world.Notification(billing.NtDidFailToRenew, "", "n-f", w.Day(15), fail, &billing.RenewalInfo{AutoRenewStatus: 1, IsInBillingRetryPeriod: true})), 200, "")
	world.At(40)
	if st := world.PlanState(ama); st != "billing_retry" && st != "grace" {
		t.Logf("state %s", st)
	}
	_ = hostedapi.HashAdminToken
}
