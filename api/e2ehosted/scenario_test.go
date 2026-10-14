//go:build e2ehosted

package e2ehosted

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	monthly = "com.sundaystudi.shop.hosted.monthly"
	delPhr  = "delete my account"
)

// ---- shared state of the story ---------------------------------------------------

var story struct {
	A, B, C, D, E *user
	aOTX          string
	aHouse        string           // A's household id
	aRecs         []int64          // A's receipt ids
	aHash         map[int64]string // sha256 of the image bytes A is served per receipt
	aFiles        []string         // files in A's household folder before the deletion
	bHouse        string
	bFiles        []string
	bRecs         []int64
	grantID       string
	invites       map[string]string // name -> code
	photos        []string
}

func needs(t *testing.T, ok bool, what string) {
	t.Helper()
	check(t, ok, "needs an earlier step: %s", what)
}

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// ---- helpers shaped like the app's calls -------------------------------------------

// signIn is the Sign in with Apple flow of the app: nonce, identity token (minted by the fake
// Apple's dev route), POST /v1/auth/apple.
func signIn(t *testing.T, name string) (*user, resp) {
	t.Helper()
	n := api(t, nil, "POST", "/v1/auth/apple/nonce", nil)
	want(t, n, 201)
	nonce := str(n.m, "nonce")
	check(t, len(nonce) >= 40, "nonce shape: %q", nonce)
	it := adm(t, "POST", "/admin/v1/dev/identity-token", M{"sub": "e2e-" + name, "nonce": nonce})
	want(t, it, 200)
	r := api(t, nil, "POST", "/v1/auth/apple", M{"identity_token": it.m["identity_token"], "authorization_code": it.m["authorization_code"],
		"nonce": nonce, "device_name": name + "'s iPhone", "name": M{"given_name": name, "family_name": "E2E"}})
	check(t, r.code == 201 || r.code == 200, "sign in %s: %s", name, r)
	u := &user{name: name, sub: "e2e-" + name, token: str(r.m, "token"), acc: sub(r.m, "account")}
	check(t, strings.HasPrefix(u.token, "st_"), "token shape %q", u.token)
	return u, r
}

func account(t *testing.T, u *user) M {
	t.Helper()
	r := api(t, u, "GET", "/v1/account", nil)
	want(t, r, 200)
	return r.m
}

func planState(t *testing.T, u *user) string {
	t.Helper()
	return str(sub(account(t, u), "plan"), "state")
}

func requireState(t *testing.T, u *user, st string) M {
	t.Helper()
	a := account(t, u)
	check(t, str(sub(a, "plan"), "state") == st, "%s: plan state %q, want %q (plan %v)", u.name, str(sub(a, "plan"), "state"), st, a["plan"])
	return a
}

// trial: the app posts a signed transaction (appAccountToken link) from StoreKit.
func startPurchase(t *testing.T, u *user, otx string, trial bool) resp {
	t.Helper()
	tx := adm(t, "POST", "/admin/v1/dev/transaction", M{"app_account_token": u.acc["app_account_token"], "original_transaction_id": otx, "trial": trial})
	want(t, tx, 200)
	return api(t, u, "POST", "/v1/billing/apple/transactions", M{"signed_transaction": tx.m["signed_transaction"]})
}

// notify mints a signed App Store notification and delivers it like Apple would (no token).
func notify(t *testing.T, u *user, otx, typ, subtype, uuid string, extra M) resp {
	t.Helper()
	body := M{"type": typ, "subtype": subtype, "uuid": uuid, "app_account_token": u.acc["app_account_token"], "original_transaction_id": otx, "trial": false}
	for k, v := range extra {
		body[k] = v
	}
	n := adm(t, "POST", "/admin/v1/dev/notification", body)
	want(t, n, 200)
	return api(t, nil, "POST", "/v1/billing/apple/notifications", M{"signedPayload": n.m["signedPayload"]})
}

func householdOf(t *testing.T, u *user) M {
	t.Helper()
	r := api(t, u, "GET", "/v1/household", nil)
	want(t, r, 200)
	return r.m
}

func receiptIDs(rs []M) []int64 {
	var out []int64
	for _, r := range rs {
		out = append(out, num(r, "id"))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func imageOf(t *testing.T, u *user, id int64) (int, []byte) {
	t.Helper()
	r := api(t, u, "GET", fmt.Sprintf("/v1/receipts/%d/image", id), nil)
	return r.code, r.body
}

func lineTexts(r M) []string {
	var out []string
	for _, l := range list(r, "lines") {
		out = append(out, fmt.Sprintf("%s=%d", str(l, "raw_text"), num(l, "line_total_cents")))
	}
	return out
}

func itemLines(r M) []M {
	var out []M
	for _, l := range list(r, "lines") {
		if str(l, "kind") == "item" {
			out = append(out, l)
		}
	}
	return out
}

func waitHealthy(t *testing.T) {
	t.Helper()
	eventually(t, 2*time.Minute, "the hosted server to answer again", func() bool {
		req, _ := newGet(baseURL + "/v1/server-info")
		r, err := doReq(req)
		return err == nil && r.code == 200
	})
}

// ---- the story -----------------------------------------------------------------------

func TestHostedProduct(t *testing.T) {
	s := &story
	s.aHash = map[int64]string{}
	s.invites = map[string]string{}
	t0 := time.Now()
	_ = t0

	step(t, "1 server-info: hosted, apple login; pairing refused", func(t *testing.T) {
		r := api(t, nil, "GET", "/v1/server-info", nil)
		want(t, r, 200)
		check(t, str(r.m, "mode") == "hosted", "mode %q", str(r.m, "mode"))
		lm, _ := r.m["login_methods"].([]any)
		check(t, len(lm) == 1 && lm[0] == "apple", "login_methods %v", r.m["login_methods"])
		check(t, r.m["pairing_enabled"] == false, "pairing_enabled %v", r.m["pairing_enabled"])
		feat := sub(r.m, "features")
		check(t, feat["households"] == true && feat["billing"] == true && feat["account_deletion"] == true && feat["async_export"] == true, "features %v", feat)
		lim := sub(r.m, "limits")
		check(t, num(lim, "receipts_per_day") == 3 && num(lim, "receipts_per_month") == 5 && num(lim, "household_members") == 4, "limits %v", lim)
		want(t, api(t, nil, "POST", "/v1/auth/pair", M{"password": "anything", "device_name": "x"}), 404, "login_method_unavailable")
		note(t, "api_level %d, version %s, tiny limits visible", num(r.m, "api_level"), str(r.m, "version"))
	})

	step(t, "2 sign in with Apple: A and B, own households, state none", func(t *testing.T) {
		// a bad nonce / forged token is refused before any account exists
		n := api(t, nil, "POST", "/v1/auth/apple/nonce", nil)
		want(t, n, 201)
		want(t, api(t, nil, "POST", "/v1/auth/apple", M{"identity_token": "mock.invalid", "authorization_code": "x", "nonce": str(n.m, "nonce"), "device_name": "x"}), 401, "apple_token_invalid")
		var ra, rb resp
		s.A, ra = signIn(t, "Ama")
		s.B, rb = signIn(t, "Bea")
		check(t, ra.code == 201 && rb.code == 201, "created: %d %d", ra.code, rb.code)
		check(t, s.A.sub != s.B.sub && str(s.A.acc, "id") != str(s.B.acc, "id"), "two distinct accounts")
		for _, u := range []*user{s.A, s.B} {
			a := requireState(t, u, "none")
			check(t, str(a, "role") == "none" && a["household_id"] == nil, "%s: role %v household %v", u.name, a["role"], a["household_id"])
			want(t, api(t, u, "GET", "/v1/household", nil), 404)
			me := api(t, u, "GET", "/v1/auth/me", nil)
			want(t, me, 200)
		}
		// the same Apple identity signs in again as the same account (200, not a second account)
		again, rr := signIn(t, "Ama")
		check(t, rr.code == 200 && str(again.acc, "id") == str(s.A.acc, "id"), "second sign-in: %d, account %s vs %s", rr.code, str(again.acc, "id"), str(s.A.acc, "id"))
		// a request with no token and with a junk token is refused
		want(t, api(t, nil, "GET", "/v1/account", nil), 401, "token_invalid")
		want(t, api(t, &user{token: "st_bogus"}, "GET", "/v1/account", nil), 401, "token_invalid")
	})

	step(t, "3 A: trial via signed transaction, receipts read by the real OCR", func(t *testing.T) {
		needs(t, s.A != nil, "sign-in")
		s.aOTX = "otx-e2e-A-1"
		r := startPurchase(t, s.A, s.aOTX, true)
		want(t, r, 200)
		check(t, r.m["result"] == "bound", "result %v", r.m["result"])
		a := requireState(t, s.A, "trial")
		check(t, str(a, "role") == "owner" && sub(a, "plan")["is_owner"] == true, "owner: %v", a)
		hh := householdOf(t, s.A)
		s.aHouse = str(hh, "id")
		check(t, s.aHouse != "" && str(hh, "your_role") == "owner", "household %v", hh)
		// the same transaction again is harmless
		want(t, startPurchase(t, s.A, s.aOTX, true), 200)
		// B (no plan) cannot buy A's subscription
		other := adm(t, "POST", "/admin/v1/dev/transaction", M{"app_account_token": s.A.acc["app_account_token"], "original_transaction_id": s.aOTX, "trial": true})
		want(t, other, 200)
		want(t, api(t, s.B, "POST", "/v1/billing/apple/transactions", M{"signed_transaction": other.m["signed_transaction"]}), 409, "subscription_belongs_to_other_account")
		requireState(t, s.B, "none")

		// a shop and a list item first, so there is more than receipts to isolate
		want(t, api(t, s.A, "POST", "/v1/shops", M{"name": "Ama's Albert Heijn"}), 201)
		want(t, api(t, s.A, "POST", "/v1/list", M{"text": "melk van Ama", "client_id": "e2e-ama-1"}), 201)

		// receipts: one synthetic (strict), then private photos when present, else synthetic
		now := serverNow(t)
		img0, total0, lines0 := synth(0, now)
		up := upload(t, s.A, "e2e-ama-0.jpg", img0)
		want(t, up, 202)
		s.aRecs = append(s.aRecs, num(up.m, "id"))
		want(t, upload(t, s.A, "e2e-ama-0.jpg", img0), 409) // same photo twice
		bad := upload(t, s.A, "notes.txt", []byte("hello"))
		check(t, bad.code == 415, "text file upload: %s", bad)

		s.photos = findPhotos(repoRoot)
		kinds := []string{"synthetic"}
		for i := 1; i <= 2; i++ {
			var data []byte
			name := fmt.Sprintf("e2e-ama-%d.jpg", i)
			if i-1 < len(s.photos) {
				var err error
				data, err = os.ReadFile(s.photos[i-1])
				check(t, err == nil, "read photo: %v", err)
				name = filepath.Base(s.photos[i-1])
				kinds = append(kinds, "private photo")
			} else {
				data, _, _ = synth(i, now)
				kinds = append(kinds, "synthetic")
			}
			up := upload(t, s.A, name, data)
			want(t, up, 202)
			s.aRecs = append(s.aRecs, num(up.m, "id"))
		}
		// the fourth receipt of the day is over the (tiny) daily limit
		img3, _, _ := synth(3, now)
		lr := upload(t, s.A, "e2e-ama-3.jpg", img3)
		check(t, lr.code == 429 && lr.problem() == "limit_reached" && str(lr.m, "limit") == "receipts_per_day", "4th upload: %s", lr)

		var reads []string
		for i, id := range s.aRecs {
			rec := waitReceipt(t, s.A, id, 12*time.Minute)
			check(t, str(rec, "status") != "failed", "receipt %d (%s) failed: %v", id, kinds[i], rec["review_reasons"])
			check(t, num(rec, "total_cents") > 0 && len(list(rec, "lines")) > 0, "receipt %d (%s) not read: total %d lines %v", id, kinds[i], num(rec, "total_cents"), lineTexts(rec))
			if i == 0 {
				items := itemLines(rec)
				check(t, str(rec, "status") == "ok", "synthetic receipt status %q, reasons %v, lines %v", str(rec, "status"), rec["review_reasons"], lineTexts(rec))
				check(t, len(items) == len(lines0), "item lines = %d, want %d: %v", len(items), len(lines0), lineTexts(rec))
				check(t, num(rec, "total_cents") == int64(total0), "total %d, want %d", num(rec, "total_cents"), total0)
				for j, w := range lines0 {
					check(t, num(items[j], "line_total_cents") == int64(w.cents) && strings.Contains(strings.ToUpper(str(items[j], "raw_text")), w.name), "line %d = %q %d, want %q %d", j, str(items[j], "raw_text"), num(items[j], "line_total_cents"), w.name, w.cents)
				}
			}
			reads = append(reads, fmt.Sprintf("%s:%s/%dc", kinds[i], str(rec, "status"), num(rec, "total_cents")))
			code, img := imageOf(t, s.A, id)
			check(t, code == 200 && len(img) > 1000, "image of %d: %d, %d bytes", id, code, len(img))
			s.aHash[id] = sha(img)
		}
		rs := api(t, s.A, "GET", "/v1/receipts", nil)
		want(t, rs, 200)
		check(t, len(rows(rs)) == 3, "A receipts: %d", len(rows(rs)))
		u := sub(account(t, s.A), "usage")
		check(t, num(u, "receipts_today") == 3 && num(u, "receipts_this_month") == 3, "usage %v", u)
		s.aFiles = householdFiles(t, s.aHouse)
		check(t, len(s.aFiles) >= 1+len(s.aRecs), "A's household folder has %d files: %v", len(s.aFiles), s.aFiles)
		note(t, "real OCR: %s", strings.Join(reads, " "))
	})

	step(t, "3b B (no plan) sees none of A's data and cannot fetch A's ids", func(t *testing.T) {
		needs(t, len(s.aRecs) == 3, "A's receipts")
		for _, p := range []string{"/v1/receipts", "/v1/shops", "/v1/products", "/v1/list", "/v1/receipts/counts"} {
			r := api(t, s.B, "GET", p, nil)
			check(t, r.code == 403 && r.problem() == "plan_required", "B GET %s: %s", p, r)
			check(t, !strings.Contains(string(r.body), "Ama"), "B GET %s leaks: %s", p, r)
		}
		for _, id := range s.aRecs {
			for _, p := range []string{fmt.Sprintf("/v1/receipts/%d", id), fmt.Sprintf("/v1/receipts/%d/image", id)} {
				r := api(t, s.B, "GET", p, nil)
				check(t, r.code == 403 || r.code == 404, "B GET %s: %s", p, r)
				check(t, !bytes.Contains(r.body, []byte("ALBERT")), "B GET %s leaks: %s", p, r)
			}
		}
		want(t, api(t, s.B, "POST", "/v1/shops", M{"name": "x"}), 403, "plan_required")
		want(t, upload(t, s.B, "b.jpg", []byte("x")), 403, "plan_required")
		want(t, api(t, s.B, "GET", "/v1/account/export", nil), 404)
	})

	step(t, "4 invites: C joins A's household, cap of 4, C leaves", func(t *testing.T) {
		needs(t, s.aHouse != "", "A's household")
		type inv struct{ id, code string }
		mk := func() inv {
			r := api(t, s.A, "POST", "/v1/household/invites", nil, "Idempotency-Key", fmt.Sprintf("inv-%d", time.Now().UnixNano()))
			want(t, r, 201)
			check(t, strings.Count(str(r.m, "code"), "-") == 2 && str(r.m, "url") != "", "invite shape %v", r.m)
			return inv{str(r.m, "id"), str(r.m, "code")}
		}
		// a member cannot invite
		s.C, _ = signIn(t, "Cleo")
		s.D, _ = signIn(t, "Dan")
		s.E, _ = signIn(t, "Eve")
		f, _ := signIn(t, "Finn")
		ic, id, ie := mk(), mk(), mk()
		// 1 member + 3 open invites = 4: a fourth invite is refused
		r := api(t, s.A, "POST", "/v1/household/invites", nil)
		check(t, r.code == 409 && r.problem() == "household_full" && num(r.m, "limit_value") == 4, "4th invite: %s", r)
		// an invite can be revoked and a revoked code is dead
		revoked := ie
		want(t, api(t, s.A, "DELETE", "/v1/household/invites/"+revoked.id, nil), 204)
		want(t, api(t, f, "POST", "/v1/household/invites/"+revoked.code+"/accept", nil), 404, "invite_invalid")
		ie = mk()
		want(t, api(t, f, "POST", "/v1/household/invites/H4Q8-ZN2M-7TWB/accept", nil), 404, "invite_invalid")

		// C accepts (lower case, without dashes works too) and sees A's data
		acc := api(t, s.C, "POST", "/v1/household/invites/"+strings.ToLower(strings.ReplaceAll(ic.code, "-", ""))+"/accept", nil)
		want(t, acc, 200)
		check(t, str(sub(sub(acc.m, "account"), "plan"), "state") == "trial" && sub(sub(acc.m, "account"), "plan")["is_owner"] == false, "C's plan after joining: %v", sub(acc.m, "account")["plan"])
		// a repeat of the same accept is a lost-response replay (200); another open code is refused
		want(t, api(t, s.C, "POST", "/v1/household/invites/"+ic.code+"/accept", nil), 200)
		want(t, api(t, s.C, "POST", "/v1/household/invites/"+id.code+"/accept", nil), 409, "already_in_household")
		want(t, api(t, f, "POST", "/v1/household/invites/"+ic.code+"/accept", nil), 404, "invite_invalid") // used
		recs := api(t, s.C, "GET", "/v1/receipts", nil)
		want(t, recs, 200)
		check(t, fmt.Sprint(receiptIDs(rows(recs))) == fmt.Sprint(receiptIDsSorted(s.aRecs)), "C sees receipts %v, A has %v", receiptIDs(rows(recs)), s.aRecs)
		for _, id := range s.aRecs {
			code, img := imageOf(t, s.C, id)
			check(t, code == 200 && sha(img) == s.aHash[id], "C sees A's image %d: %d", id, code)
		}
		sh := api(t, s.C, "GET", "/v1/shops", nil)
		want(t, sh, 200)
		check(t, strings.Contains(string(sh.body), "Ama's Albert Heijn"), "C sees A's shop: %s", sh)
		ls := api(t, s.C, "GET", "/v1/list", nil)
		want(t, ls, 200)
		check(t, strings.Contains(string(ls.body), "melk van Ama"), "C sees A's list: %s", ls)
		// members cannot invite, and cannot touch the household
		want(t, api(t, s.C, "POST", "/v1/household/invites", nil), 403, "not_household_owner")
		// D and E join: A + C + D + E = 4
		want(t, api(t, s.D, "POST", "/v1/household/invites/"+id.code+"/accept", nil), 200)
		want(t, api(t, s.E, "POST", "/v1/household/invites/"+ie.code+"/accept", nil), 200)
		hh := householdOf(t, s.A)
		check(t, len(list(hh, "members")) == 4 && num(hh, "member_cap") == 4, "members %d cap %d", len(list(hh, "members")), num(hh, "member_cap"))
		// a fifth is refused
		r = api(t, s.A, "POST", "/v1/household/invites", nil)
		check(t, r.code == 409 && r.problem() == "household_full" && str(r.m, "limit") == "household_members" && num(r.m, "limit_value") == 4, "fifth: %s", r)
		// C leaves; C keeps the account, loses the data, a seat is free again
		want(t, api(t, s.C, "POST", "/v1/household/leave", nil), 204)
		a := account(t, s.C)
		check(t, str(a, "role") == "none" && str(sub(a, "plan"), "state") == "none", "C after leaving: %v", a)
		cr := api(t, s.C, "GET", "/v1/receipts", nil)
		check(t, cr.code == 403 || cr.code == 404, "C after leaving reads receipts: %s", cr)
		hh = householdOf(t, s.A)
		check(t, len(list(hh, "members")) == 3, "members after leave: %d", len(list(hh, "members")))
		r = api(t, s.A, "POST", "/v1/household/invites", nil)
		want(t, r, 201)
		want(t, api(t, s.A, "DELETE", "/v1/household/invites/"+str(r.m, "id"), nil), 204)
		want(t, api(t, s.A, "POST", "/v1/household/leave", nil), 409, "owner_cannot_leave")
		note(t, "C joined, saw 3 receipts/images/shop/list; cap 4 held; fifth refused household_full; C left")
	})

	step(t, "5 renewal (delivered twice, processed once), failed renewal: grace then billing retry", func(t *testing.T) {
		needs(t, s.aOTX != "", "A's trial")
		advance(t, 14*24*time.Hour+time.Hour)
		r1 := notify(t, s.A, s.aOTX, "DID_RENEW", "", "n-e2e-renew-1", M{"trial": false, "days": 30})
		want(t, r1, 200)
		a1 := requireState(t, s.A, "active")
		p1 := fmt.Sprint(a1["plan"])
		r2 := notify(t, s.A, s.aOTX, "DID_RENEW", "", "n-e2e-renew-1", M{"trial": false, "days": 30})
		want(t, r2, 200)
		a2 := requireState(t, s.A, "active")
		check(t, p1 == fmt.Sprint(a2["plan"]), "plan changed by the replay:\n%s\n%s", p1, a2["plan"])
		// D (a member) sees the household plan
		d := requireState(t, s.D, "active")
		check(t, sub(d, "plan")["is_owner"] == false, "member plan: %v", d["plan"])

		// next cycle: the payment fails when the paid period ends; Apple's grace period starts
		advance(t, 29*24*time.Hour)
		want(t, notify(t, s.A, s.aOTX, "DID_FAIL_TO_RENEW", "GRACE_PERIOD", "n-e2e-fail-1", M{"days": 1}), 200)
		advance(t, 25*time.Hour) // the paid period (the last day) is over
		g := requireState(t, s.A, "grace")
		check(t, str(sub(g, "plan"), "grace_until") != "", "grace_until missing: %v", g["plan"])
		want(t, api(t, s.A, "POST", "/v1/shops", M{"name": "Written in grace"}), 201) // writes still work in grace
		// grace over, Apple still retrying
		advance(t, 16*24*time.Hour)
		want(t, notify(t, s.A, s.aOTX, "DID_FAIL_TO_RENEW", "", "n-e2e-fail-2", M{"days": 1}), 200)
		advance(t, 25*time.Hour)
		br := requireState(t, s.A, "billing_retry")
		check(t, sub(br, "plan")["read_only"] == true, "billing_retry read_only: %v", br["plan"])
		w := api(t, s.A, "POST", "/v1/shops", M{"name": "no"})
		check(t, w.code == 403 && w.problem() == "read_only" && str(w.m, "plan_state") == "billing_retry", "write in billing_retry: %s", w)
		want(t, api(t, s.A, "GET", "/v1/shops", nil), 200)

		// "processed once", proven the hard way on C's own subscription: an old renewal delivered again
		// after a later expiry must not bring the subscription back to life
		otxC := "otx-e2e-C-1"
		want(t, startPurchase(t, s.C, otxC, false), 200)
		requireState(t, s.C, "active")
		first := notify(t, s.C, otxC, "DID_RENEW", "", "n-e2e-c-renew", M{"days": 30})
		want(t, first, 200)
		advance(t, time.Hour)
		want(t, notify(t, s.C, otxC, "EXPIRED", "VOLUNTARY", "n-e2e-c-exp", M{"auto_renew": false}), 200)
		requireState(t, s.C, "lapsed_read_only")
		want(t, notify(t, s.C, otxC, "DID_RENEW", "", "n-e2e-c-renew", M{"days": 30}), 200)
		requireState(t, s.C, "lapsed_read_only")
		note(t, "trial -> active (replay changed nothing; a replayed renewal after expiry did not revive C) -> grace -> billing_retry; writes refused with read_only")
	})

	step(t, "10 restart the container: state survives", func(t *testing.T) {
		needs(t, planState(t, s.A) == "billing_retry", "billing_retry")
		before := map[string]string{}
		snap := func(into map[string]string) {
			a := account(t, s.A)
			into["plan"] = fmt.Sprint(a["plan"])
			into["id"] = str(a, "id")
			into["support"] = str(a, "support_code")
			into["household"] = fmt.Sprint(householdOf(t, s.A)["members"])
			rs := api(t, s.A, "GET", "/v1/receipts", nil)
			want(t, rs, 200)
			into["receipts"] = fmt.Sprint(receiptIDs(rows(rs)))
			for _, id := range s.aRecs {
				code, img := imageOf(t, s.A, id)
				check(t, code == 200, "image %d: %d", id, code)
				into[fmt.Sprintf("img%d", id)] = sha(img)
			}
			into["D"] = fmt.Sprint(account(t, s.D)["plan"])
			into["B"] = planState(t, s.B)
		}
		snap(before)
		nowBefore := serverNow(t)
		tokBefore := s.A.token
		ctx, cancel := ctxMin(5)
		defer cancel()
		out, err := compose(ctx, "", "restart", "api")
		check(t, err == nil, "restart api: %v %s", err, out)
		out, err = compose(ctx, "", "restart", "tools")
		check(t, err == nil, "restart tools: %v %s", err, out)
		waitHealthy(t)
		eventually(t, time.Minute, "the admin sidecar", func() bool {
			r, err := doReq(mustReq("POST", adminURL+"/admin/v1/dev/advance-clock", `{}`, "Authorization", "Bearer "+adminTok, "X-Admin-Operator", "e2e"))
			return err == nil && r.code == 200
		})
		after := map[string]string{}
		check(t, s.A.token == tokBefore, "token unchanged")
		snap(after)
		for k, v := range before {
			check(t, after[k] == v, "%s changed over the restart:\n before %s\n after  %s", k, v, after[k])
		}
		nowAfter := serverNow(t)
		check(t, !nowAfter.Before(nowBefore), "clock went back over the restart: %s -> %s", nowBefore, nowAfter)
		check(t, nowAfter.Sub(nowBefore) < time.Hour, "clock jumped over the restart: %s -> %s", nowBefore, nowAfter)
		// the device token, the fake Apple and the nonce flow all work again after the restart
		fresh, rr := signIn(t, "Ama")
		check(t, rr.code == 200 && str(fresh.acc, "id") == before["id"], "A signs in again after the restart: %d", rr.code)
		note(t, "same plan, members, receipts, image bytes and clock after restart; A signs in again")
	})

	step(t, "6 expiry: lapsed_read_only, writes 403 read_only, reads and export work", func(t *testing.T) {
		needs(t, s.aOTX != "", "A")
		advance(t, 24*time.Hour)
		want(t, notify(t, s.A, s.aOTX, "EXPIRED", "VOLUNTARY", "n-e2e-exp-1", M{"auto_renew": false}), 200)
		a := requireState(t, s.A, "lapsed_read_only")
		pl := sub(a, "plan")
		check(t, str(pl, "delete_after") != "" && str(pl, "read_only_since") != "", "lapse clock: %v", pl)
		// members see the same state
		requireState(t, s.D, "lapsed_read_only")
		now := serverNow(t)
		img, _, _ := synth(4, now)
		up := upload(t, s.A, "late.jpg", img)
		check(t, up.code == 403 && up.problem() == "read_only", "upload while lapsed: %s", up)
		for _, w := range []struct {
			m, p string
			b    any
		}{
			{"POST", "/v1/shops", M{"name": "no"}},
			{"POST", "/v1/list", M{"text": "no"}},
			{"POST", fmt.Sprintf("/v1/receipts/%d/reparse", s.aRecs[0]), nil},
			{"POST", "/v1/household/invites", nil},
		} {
			r := api(t, s.A, w.m, w.p, w.b)
			check(t, r.code == 403 && (r.problem() == "read_only" || r.problem() == "plan_required"), "%s %s while lapsed: %s", w.m, w.p, r)
		}
		check(t, api(t, s.A, "POST", "/v1/shops", M{"name": "no"}).problem() == "read_only", "shops write must be read_only")
		for _, p := range []string{"/v1/shops", "/v1/receipts", "/v1/list", "/v1/products"} {
			want(t, api(t, s.A, "GET", p, nil), 200)
		}
		for _, id := range s.aRecs {
			code, img := imageOf(t, s.A, id)
			check(t, code == 200 && sha(img) == s.aHash[id], "image %d readable while lapsed", id)
		}

		// export: async job, then a real zip
		r := api(t, s.A, "POST", "/v1/account/export", nil)
		check(t, r.code == 202 || r.code == 200, "export: %s", r)
		var job M
		eventually(t, 3*time.Minute, "the export job", func() bool {
			j := api(t, s.A, "GET", "/v1/account/export", nil)
			job = j.m
			return j.code == 200 && (str(j.m, "status") == "ready" || str(j.m, "status") == "failed")
		})
		check(t, str(job, "status") == "ready", "export job: %v", job)
		dl := api(t, s.A, "GET", str(job, "download_url"), nil)
		want(t, dl, 200)
		check(t, strings.HasPrefix(dl.hdr.Get("Content-Type"), "application/zip"), "content type %q", dl.hdr.Get("Content-Type"))
		// the download needs A's token
		check(t, api(t, nil, "GET", str(job, "download_url"), nil).code == 401, "download without token")
		check(t, api(t, s.B, "GET", str(job, "download_url"), nil).code >= 400, "B downloads A's export")
		zr, err := zip.NewReader(bytes.NewReader(dl.body), int64(len(dl.body)))
		check(t, err == nil, "zip: %v", err)
		var names []string
		imgs, hashes := 0, map[string]bool{}
		hasReceipts := false
		for _, f := range zr.File {
			names = append(names, f.Name)
			ln := strings.ToLower(f.Name)
			rc, err := f.Open()
			check(t, err == nil, "open %s: %v", f.Name, err)
			var buf bytes.Buffer
			buf.ReadFrom(rc)
			rc.Close()
			if strings.HasSuffix(ln, ".jpg") || strings.HasSuffix(ln, ".jpeg") || strings.HasSuffix(ln, ".png") {
				imgs++
				hashes[sha(buf.Bytes())] = true
			}
			if strings.Contains(ln, "receipt") && !strings.Contains(ln, "image") {
				hasReceipts = true
			}
			if strings.HasSuffix(ln, ".json") || strings.HasSuffix(ln, ".csv") {
				if strings.Contains(buf.String(), "ALBERT") || strings.Contains(strings.ToLower(buf.String()), "albert") {
					hasReceipts = true
				}
			}
		}
		matched := 0
		for _, id := range s.aRecs {
			if hashes[s.aHash[id]] {
				matched++
			}
		}
		check(t, imgs >= len(s.aRecs) && hasReceipts, "export zip has %d images, receipts file=%v: %v", imgs, hasReceipts, names)
		check(t, matched == len(s.aRecs), "only %d of A's %d image bytes are in the export (%d images): %v", matched, len(s.aRecs), imgs, names)
		// exports per day (tiny limit 2): the second works once the first is ready, the third is limit_reached
		r = api(t, s.A, "POST", "/v1/account/export", nil)
		check(t, r.code == 202 || r.code == 200 || r.code == 429, "second export: %s", r)
		eventually(t, 3*time.Minute, "the second export", func() bool {
			j := api(t, s.A, "GET", "/v1/account/export", nil)
			return j.code == 200 && str(j.m, "status") != "queued" && str(j.m, "status") != "running"
		})
		r = api(t, s.A, "POST", "/v1/account/export", nil)
		check(t, r.code == 429 && r.problem() == "limit_reached" && str(r.m, "limit") == "exports_per_day", "third export: %s", r)
		note(t, "export zip: %d files, A's %d images byte-identical; exports_per_day limit hit", len(zr.File), len(s.aRecs))
	})

	step(t, "7 complimentary grant: B comped, then revoked lapses", func(t *testing.T) {
		needs(t, s.B != nil && len(s.aRecs) == 3, "B and A's data")
		bid := str(s.B.acc, "id")
		g := adm(t, "POST", "/admin/v1/grants", M{"account_id": bid, "key": "hosted", "days": 30, "reason": "e2e: friend"}, "Idempotency-Key", "e2e-grant-b-1")
		want(t, g, 201)
		s.grantID = str(g.m, "id")
		again := adm(t, "POST", "/admin/v1/grants", M{"account_id": bid, "key": "hosted", "days": 30, "reason": "e2e: friend"}, "Idempotency-Key", "e2e-grant-b-1")
		check(t, again.code == 200 && str(again.m, "id") == s.grantID, "idempotent grant: %s", again)
		b := requireState(t, s.B, "comped")
		pl := sub(b, "plan")
		check(t, str(pl, "entitlement_source") == "grant" && str(pl, "ends_at") != "" && pl["read_only"] == false, "comped plan: %v", pl)
		s.bHouse = str(householdOf(t, s.B), "id")
		check(t, s.bHouse != "" && s.bHouse != s.aHouse, "B household %q vs A %q", s.bHouse, s.aHouse)

		// B's own household is empty: none of A's receipts, shops, list, products, images
		for _, p := range []string{"/v1/receipts", "/v1/shops", "/v1/list", "/v1/products"} {
			r := api(t, s.B, "GET", p, nil)
			want(t, r, 200)
			check(t, len(rows(r)) == 0 && !strings.Contains(string(r.body), "Ama"), "B GET %s: %s", p, r)
		}
		for _, id := range s.aRecs {
			for _, p := range []string{fmt.Sprintf("/v1/receipts/%d", id), fmt.Sprintf("/v1/receipts/%d/image", id), fmt.Sprintf("/v1/receipts/%d/shop-suggestion", id)} {
				r := api(t, s.B, "GET", p, nil)
				check(t, r.code == 404, "B GET %s: %s", p, r)
			}
		}
		// B writes into B's household only
		want(t, api(t, s.B, "POST", "/v1/shops", M{"name": "Bea's Jumbo"}), 201)
		want(t, api(t, s.B, "POST", "/v1/list", M{"text": "thee van Bea", "client_id": "e2e-bea-1"}), 201)
		as := api(t, s.A, "GET", "/v1/shops", nil)
		check(t, !strings.Contains(string(as.body), "Bea"), "A sees B's shop: %s", as)
		al := api(t, s.A, "GET", "/v1/list", nil)
		check(t, !strings.Contains(string(al.body), "Bea"), "A sees B's list: %s", al)

		// revoke: lapsed read-only
		rv := adm(t, "POST", "/admin/v1/grants/"+s.grantID+"/revoke", M{"reason": "e2e: revoke"})
		want(t, rv, 200)
		check(t, str(rv.m, "status") == "revoked", "revoke: %s", rv)
		requireState(t, s.B, "lapsed_read_only")
		w := api(t, s.B, "POST", "/v1/shops", M{"name": "no"})
		check(t, w.code == 403 && w.problem() == "read_only", "B write after revoke: %s", w)
		want(t, api(t, s.B, "GET", "/v1/shops", nil), 200)
		// a new grant brings B back (used by the limits step)
		g2 := adm(t, "POST", "/admin/v1/grants", M{"account_id": bid, "key": "hosted", "days": 60, "reason": "e2e: limits"}, "Idempotency-Key", "e2e-grant-b-2")
		want(t, g2, 201)
		requireState(t, s.B, "comped")
		au := adm(t, "GET", "/admin/v1/audit?limit=100", nil)
		want(t, au, 200)
		kinds := ""
		for _, e := range list(au.m, "entries") {
			kinds += str(e, "action") + " "
		}
		check(t, strings.Contains(kinds, "grant.create") && strings.Contains(kinds, "grant.revoke"), "audit rows: %s", kinds)
		note(t, "comped -> revoked -> lapsed_read_only -> comped again; B saw none of A's data (404)")
	})

	step(t, "8 usage limits: right limit named (day, month, upload size)", func(t *testing.T) {
		needs(t, s.bHouse != "", "B comped")
		now := serverNow(t)
		if now.Day() > 24 {
			// keep the two upload days inside one calendar month
			next := time.Date(now.Year(), now.Month()+1, 2, 10, 0, 0, 0, time.UTC)
			now = advance(t, next.Sub(now))
		}
		var ids []int64
		up := func(i int) resp {
			img, _, _ := synth(i, now)
			return upload(t, s.B, fmt.Sprintf("bea-%d.jpg", i), img)
		}
		// too large: a photo over max_upload_bytes (tiny: 1.5 MB) is refused before it counts
		big, _, _ := synth(0, now)
		big = append(big, make([]byte, 1_600_000)...)
		r := upload(t, s.B, "big.jpg", big)
		check(t, r.code == 413 && r.problem() == "file_too_large" && str(r.m, "limit") == "max_upload_bytes" && num(r.m, "limit_value") == 1500000, "oversize: %s", r)
		for i := 0; i < 3; i++ {
			r := up(i)
			want(t, r, 202)
			ids = append(ids, num(r.m, "id"))
		}
		r = up(3)
		check(t, r.code == 429 && r.problem() == "limit_reached" && str(r.m, "limit") == "receipts_per_day" && num(r.m, "limit_value") == 3 && num(r.m, "current") == 3, "4th upload of the day: %s", r)
		check(t, r.m["resets_at"] != nil, "resets_at missing: %s", r)
		check(t, r.hdr.Get("Retry-After") == "" || true, "")
		// next day: the day counter resets, the month counter does not
		advance(t, 25*time.Hour)
		now = serverNow(t)
		for i := 3; i < 5; i++ {
			r := up(i)
			want(t, r, 202)
			ids = append(ids, num(r.m, "id"))
		}
		r = up(5)
		check(t, r.code == 429 && r.problem() == "limit_reached" && str(r.m, "limit") == "receipts_per_month" && num(r.m, "limit_value") == 5, "6th upload of the month: %s", r)
		u := sub(account(t, s.B), "usage")
		check(t, num(u, "receipts_this_month") == 5, "usage %v", u)
		// A's household is unaffected by B's counters (A is lapsed, so read-only, not limit_reached)
		check(t, api(t, s.A, "GET", "/v1/receipts", nil).code == 200, "A reads")
		// the receipts B was allowed to upload are read by the real OCR
		var reads []string
		for _, id := range ids {
			rec := waitReceipt(t, s.B, id, 12*time.Minute)
			check(t, str(rec, "status") != "failed" && num(rec, "total_cents") > 0, "B receipt %d: %s total %d %v", id, str(rec, "status"), num(rec, "total_cents"), lineTexts(rec))
			reads = append(reads, fmt.Sprintf("%s/%dc", str(rec, "status"), num(rec, "total_cents")))
		}
		s.bRecs = ids
		note(t, "413 max_upload_bytes; 4th/day -> receipts_per_day; 6th/month -> receipts_per_month; B's 5 read: %s", strings.Join(reads, " "))
	})

	step(t, "11 isolation spot check over HTTP and Docker (guessed ids and paths)", func(t *testing.T) {
		needs(t, len(s.bRecs) == 5 && len(s.aFiles) > 0, "B's receipts and A's files")
		bset := map[int64]bool{}
		for _, id := range s.bRecs {
			bset[id] = true
		}
		aHashes := map[string]bool{}
		for _, h := range s.aHash {
			aHashes[h] = true
		}
		// B's own ids work; every other id (A has ids that B may also number) is 404 and A's bytes never show up
		for id := int64(1); id <= 40; id++ {
			code, img := imageOf(t, s.B, id)
			if bset[id] {
				check(t, code == 200 && !aHashes[sha(img)], "B's own image %d: %d", id, code)
			} else {
				check(t, code == 404, "B guesses image %d: %d", id, code)
			}
			r := api(t, s.B, "GET", fmt.Sprintf("/v1/receipts/%d", id), nil)
			check(t, bset[id] == (r.code == 200), "B GET receipt %d: %s", id, r)
		}
		for _, id := range s.aRecs {
			code, img := imageOf(t, s.A, id)
			check(t, code == 200 && sha(img) == s.aHash[id], "A's own image %d: %d", id, code)
		}
		// guessed file paths: the folder layout of A's household on disk, with B's token, and without
		guesses := 0
		for _, f := range s.aFiles {
			for _, p := range []string{
				"/" + f, "/v1/" + f, "/data/households/" + s.aHouse + "/" + f, "/households/" + s.aHouse + "/" + f,
				"/v1/households/" + s.aHouse + "/" + f, "/v1/receipts/../../households/" + s.aHouse + "/" + f,
				"/v1/receipts/%2e%2e/%2e%2e/households/" + s.aHouse + "/" + f, "/images/" + filepath.Base(f), "/v1/images/" + filepath.Base(f),
			} {
				for _, u := range []*user{s.B, nil} {
					r := api(t, u, "GET", p, nil)
					guesses++
					check(t, r.code >= 400 && r.code != 500 && !bytes.HasPrefix(r.body, []byte{0xff, 0xd8}) && !bytes.Contains(r.body, []byte("SQLite format")), "GET %s as %v: %s", p, u != nil, r)
				}
			}
		}
		// asking for A's household by header or query changes nothing
		for _, h := range [][]string{{"X-Household-ID", s.aHouse}, {"X-Household", s.aHouse}} {
			r := api(t, s.B, "GET", "/v1/receipts?household_id="+s.aHouse, nil, h...)
			want(t, r, 200)
			check(t, fmt.Sprint(receiptIDs(rows(r))) == fmt.Sprint(receiptIDsSorted(s.bRecs)), "B with %v sees %v", h, receiptIDs(rows(r)))
		}
		// on disk: separate folders, no file shared between the two households
		out := toolsPy(t, `
import os,hashlib,json
root='/data/households'
res={}
for h in sorted(os.listdir(root)):
    hs={}
    for d,_,fs in os.walk(os.path.join(root,h)):
        for n in fs:
            p=os.path.join(d,n)
            if n.endswith(('-wal','-shm')): continue
            hs[os.path.relpath(p,os.path.join(root,h))]=hashlib.sha256(open(p,'rb').read()).hexdigest()
    res[h]=hs
print(json.dumps(res))`)
		var tree map[string]map[string]string
		check(t, json.Unmarshal([]byte(out), &tree) == nil, "listing: %s", out)
		a, b := tree[s.aHouse], tree[s.bHouse]
		check(t, a != nil && b != nil && s.aHouse != s.bHouse, "two household folders expected, got %d", len(tree))
		for fa, ha := range a {
			for fb, hb := range b {
				if strings.HasSuffix(fa, ".db") && strings.HasSuffix(fb, ".db") {
					check(t, ha != hb, "the household databases are the same file content")
				}
				if strings.Contains(fa, "image") {
					check(t, ha != hb, "A's %s and B's %s are the same bytes", fa, fb)
				}
			}
		}
		s.bFiles = householdFiles(t, s.bHouse)
		note(t, "%d guessed paths refused, ids 1..40 and headers checked, %d files in A's folder and %d in B's share nothing", guesses, len(a), len(b))
	})

	step(t, "9 account deletion for A: request, cooling-off, erasure, fresh start", func(t *testing.T) {
		needs(t, s.aHouse != "" && len(s.aFiles) > 0, "A's household")
		bBefore := map[int64]string{}
		for _, id := range s.bRecs {
			_, img := imageOf(t, s.B, id)
			bBefore[id] = sha(img)
		}
		// refusals first
		w := api(t, s.A, "DELETE", "/v1/account", M{"confirm": "nope", "delete_household": true})
		check(t, w.code == 400 && w.problem() == "invalid_request", "wrong confirm: %s", w)
		w = api(t, s.A, "DELETE", "/v1/account", M{"confirm": delPhr})
		check(t, w.code == 409 && w.problem() == "owner_must_confirm_household", "owner without delete_household: %s", w)
		w = api(t, s.D, "DELETE", "/v1/account", M{"confirm": delPhr, "delete_household": true})
		check(t, w.code == 409 && w.problem() == "not_household_owner", "member with delete_household: %s", w)

		// stage 1
		d := api(t, s.A, "DELETE", "/v1/account", M{"confirm": delPhr, "delete_household": true})
		want(t, d, 202)
		check(t, str(d.m, "status") == "scheduled" && d.m["can_cancel"] == true, "stage 1: %s", d)
		da, err := time.Parse(time.RFC3339, str(d.m, "delete_after"))
		check(t, err == nil, "delete_after: %s", d)
		now := serverNow(t)
		check(t, da.Sub(now) > 6*24*time.Hour && da.Sub(now) <= 7*24*time.Hour+time.Minute, "delete_after %s is not 7 days from %s", da, now)
		d2 := api(t, s.A, "DELETE", "/v1/account", M{"confirm": delPhr, "delete_household": true})
		check(t, d2.code == 200 && str(d2.m, "delete_after") == str(d.m, "delete_after"), "repeat is idempotent: %s", d2)
		a := requireState(t, s.A, "pending_deletion")
		check(t, sub(a, "plan")["deletion_requested"] == true, "deletion_requested: %v", a["plan"])
		// cooling-off: data is still there, readable and exportable, not writable
		want(t, api(t, s.A, "GET", "/v1/receipts", nil), 200)
		w = api(t, s.A, "POST", "/v1/shops", M{"name": "no"})
		check(t, w.code == 403 && w.problem() == "read_only" && str(w.m, "plan_state") == "pending_deletion", "write in cooling-off: %s", w)
		requireState(t, s.D, "pending_deletion")
		check(t, fmt.Sprint(householdFiles(t, s.aHouse)) == fmt.Sprint(s.aFiles), "A's files changed during cooling-off")
		// cancelling brings the account back to its earlier state; then ask again
		c := api(t, s.A, "DELETE", "/v1/account/deletion", nil)
		want(t, c, 200)
		requireState(t, s.A, "lapsed_read_only")
		want(t, api(t, s.A, "DELETE", "/v1/account/deletion", nil), 409, "deletion_not_pending")
		want(t, api(t, s.A, "DELETE", "/v1/account", M{"confirm": delPhr, "delete_household": true}), 202)
		requireState(t, s.A, "pending_deletion")

		// time passes: 6 days later nothing is erased yet
		advance(t, 6*24*time.Hour)
		time.Sleep(6 * time.Second) // a few scheduler runs (interval 2 s)
		check(t, householdDirExists(t, s.aHouse), "A's folder erased after 6 days, before the 7 day cooling-off ended")
		requireState(t, s.A, "pending_deletion")
		// past delete_after: the scheduler tombstones, then erases after its 24 h hold
		advance(t, 25*time.Hour)
		erased := func() bool { return !householdDirExists(t, s.aHouse) }
		for i := 0; i < 2 && !erased(); i++ {
			time.Sleep(8 * time.Second)
			if !erased() {
				advance(t, 25*time.Hour)
				time.Sleep(8 * time.Second)
			}
		}
		eventually(t, 90*time.Second, "A's household to be erased", erased)
		check(t, householdFiles(t, s.aHouse) == nil, "files left in A's folder")
		// stage 2 removes the devices too: the old token is dead, the account row stays
		check(t, api(t, s.A, "GET", "/v1/account", nil).code == 401, "A's old device token still works after the erasure")
		fresh, rr := signIn(t, "Ama")
		s.A = fresh
		st := planState(t, s.A)
		check(t, (rr.code == 200 || rr.code == 201) && (st == "deleted" || st == "none"), "A signs in after the erasure: %d, state %q", rr.code, st)
		w = api(t, s.A, "GET", "/v1/shops", nil)
		check(t, w.code == 403 && w.problem() == "plan_required", "A after erasure: %s", w)
		dState := planState(t, s.D)
		check(t, dState == "none" || dState == "deleted", "member D after the household was erased: %q", dState)
		// B is untouched: same folder, same receipts, same bytes
		check(t, fmt.Sprint(householdFiles(t, s.bHouse)) == fmt.Sprint(s.bFiles), "B's files changed: %v vs %v", householdFiles(t, s.bHouse), s.bFiles)
		rs := api(t, s.B, "GET", "/v1/receipts", nil)
		want(t, rs, 200)
		check(t, fmt.Sprint(receiptIDs(rows(rs))) == fmt.Sprint(receiptIDsSorted(s.bRecs)), "B's receipts: %v", receiptIDs(rows(rs)))
		for id, h := range bBefore {
			code, img := imageOf(t, s.B, id)
			check(t, code == 200 && sha(img) == h, "B's image %d after A's erasure: %d", id, code)
		}
		requireState(t, s.B, "comped")
		// A starts fresh: a new subscription, a new empty household
		r := startPurchase(t, s.A, "otx-e2e-A-2", false)
		want(t, r, 200)
		requireState(t, s.A, "active")
		hh := householdOf(t, s.A)
		check(t, str(hh, "id") != s.aHouse, "new household has the old id")
		sh := api(t, s.A, "GET", "/v1/shops", nil)
		want(t, sh, 200)
		rc := api(t, s.A, "GET", "/v1/receipts", nil)
		want(t, rc, 200)
		check(t, len(rows(sh)) == 0 && len(rows(rc)) == 0 && !strings.Contains(string(sh.body), "Ama"), "fresh start is not empty: %s %s", sh, rc)
		check(t, !householdDirExists(t, s.aHouse), "the old folder came back")
		want(t, api(t, s.A, "POST", "/v1/shops", M{"name": "Fresh start"}), 201)
		note(t, "stage 1 -> cooling-off (reads yes, writes no) -> cancel/redo -> 7 days -> erased; B untouched; A restarted empty")
	})
}
