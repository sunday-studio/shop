package control_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"shop/internal/hosted/control"
	"shop/internal/hosted/hclock"
)

var t0 = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

type env struct {
	ctx   context.Context
	clock *hclock.Fake
	s     *control.Store
	svc   *control.Service
	plan  map[string]bool
	path  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{ctx: context.Background(), clock: hclock.NewFake(t0), plan: map[string]bool{}}
	e.path = filepath.Join(t.TempDir(), "control.db")
	s, err := control.Open(e.path, e.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e.s = s
	e.svc = control.NewService(s, control.DefaultConfig(), func(_ context.Context, id string, _ time.Time) (bool, error) { return e.plan[id], nil })
	return e
}

func (e *env) account(t *testing.T, sub string) control.Account {
	t.Helper()
	a, _, err := e.svc.UpsertAccount(e.ctx, sub, sub+"@example.com", false, "")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (e *env) household(t *testing.T, owner control.Account) control.Household {
	t.Helper()
	h, _, err := e.svc.EnsureHousehold(e.ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func wantCode(t *testing.T, err error, want *control.Error) {
	t.Helper()
	if !control.Is(err, want) {
		t.Fatalf("got %v, want code %s", err, want.Code)
	}
}

func TestReopenKeepsDataAndMigrationsAreIdempotent(t *testing.T) {
	e := newEnv(t)
	a := e.account(t, "sub1")
	path := filepath.Join(t.TempDir(), "c2.db")
	s2, err := control.Open(path, e.clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.InsertAccount(e.ctx, control.Account{AppleSub: "x"}); err != nil {
		t.Fatal(err)
	}
	s2.Close()
	s3, err := control.Open(path, e.clock) // second open must not fail or lose rows
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	if _, err := s3.AccountBySub(e.ctx, "x"); err != nil {
		t.Fatal(err)
	}
	_ = a
}

func TestAccountUpsert(t *testing.T) {
	e := newEnv(t)
	a, created, err := e.svc.UpsertAccount(e.ctx, "sub-ama", "ama@privaterelay.appleid.com", true, "")
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if a.AppAccountToken == "" || len(a.AppAccountToken) != 36 || a.SupportCode == "" || len(a.SupportCode) != 9 || a.SupportCode[4] != '-' {
		t.Fatalf("bad generated fields: %+v", a)
	}
	// second sign-in: same account, display name set only once
	a2, created, err := e.svc.UpsertAccount(e.ctx, "sub-ama", "ama@privaterelay.appleid.com", true, "Ama B.")
	if err != nil || created || a2.ID != a.ID || a2.DisplayName != "Ama B." {
		t.Fatalf("second: %+v created=%v err=%v", a2, created, err)
	}
	a3, _, _ := e.svc.UpsertAccount(e.ctx, "sub-ama", "", false, "Other")
	if a3.DisplayName != "Ama B." {
		t.Fatalf("display name must not be overwritten: %q", a3.DisplayName)
	}
	if a3.AppAccountToken != a.AppAccountToken {
		t.Fatal("appAccountToken must never change")
	}
	byTok, err := e.s.AccountByAppToken(e.ctx, strings.ToUpper(a.AppAccountToken))
	if err != nil || byTok.ID != a.ID {
		t.Fatalf("lookup by token: %v", err)
	}
	bySupport, err := e.s.AccountBySupportCode(e.ctx, strings.ToLower(a.SupportCode))
	if err != nil || bySupport.ID != a.ID {
		t.Fatalf("lookup by support code: %v", err)
	}
}

func TestEnsureHouseholdIsIdempotentAndOneOwner(t *testing.T) {
	e := newEnv(t)
	a := e.account(t, "a")
	h1, created, err := e.svc.EnsureHousehold(e.ctx, a.ID)
	if err != nil || !created {
		t.Fatal(err, created)
	}
	h2, created, err := e.svc.EnsureHousehold(e.ctx, a.ID)
	if err != nil || created || h2.ID != h1.ID {
		t.Fatalf("second ensure: %v %v", err, created)
	}
	b := e.account(t, "b")
	// DB level: a second owner row in the same household is refused
	err = e.s.AddMember(e.ctx, control.Member{HouseholdID: h1.ID, AccountID: b.ID, Role: control.RoleOwner})
	if err == nil {
		t.Fatal("two owners must be refused by the database")
	}
	// an account cannot be in two households
	c := e.account(t, "c")
	h3 := e.household(t, c)
	if err := e.s.AddMember(e.ctx, control.Member{HouseholdID: h3.ID, AccountID: a.ID, Role: control.RoleMember}); err == nil {
		t.Fatal("one household per account")
	}
}

func fillInvite(t *testing.T, e *env, owner control.Account) (string, control.Invite) {
	t.Helper()
	inv, code, err := e.svc.CreateInvite(e.ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	return code, inv
}

func TestInviteHashedSingleUseAndFormats(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	h := e.household(t, owner)
	code, inv := fillInvite(t, e, owner)
	if len(code) != 14 || code[4] != '-' || code[9] != '-' {
		t.Fatalf("code format: %q", code)
	}
	if strings.Contains(inv.CodeHash, strings.ReplaceAll(code, "-", "")) || inv.CodeHash != control.HashCode(code) {
		t.Fatal("only a hash may be stored")
	}
	if inv.ExpiresAt != t0.Add(7*24*time.Hour) {
		t.Fatalf("expiry %v", inv.ExpiresAt)
	}
	b := e.account(t, "b")
	// lower case, no dashes, accepted
	got, err := e.svc.AcceptInvite(e.ctx, b.ID, strings.ToLower(strings.ReplaceAll(code, "-", "")))
	if err != nil || got.ID != h.ID {
		t.Fatalf("accept: %v", err)
	}
	// single use: another account cannot reuse it
	c := e.account(t, "c")
	_, err = e.svc.AcceptInvite(e.ctx, c.ID, code)
	wantCode(t, err, control.ErrInviteInvalid)
	// the member is a member, the owner unchanged
	members, _ := e.s.Members(e.ctx, h.ID)
	if len(members) != 2 || members[0].Role != control.RoleOwner || members[1].AccountID != b.ID {
		t.Fatalf("members: %+v", members)
	}
}

func TestInviteConcurrentAcceptOnlyOneWins(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	e.household(t, owner)
	code, _ := fillInvite(t, e, owner)
	var accts []control.Account
	for i := 0; i < 6; i++ {
		accts = append(accts, e.account(t, "m"+string(rune('a'+i))))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for _, a := range accts {
		wg.Add(1)
		go func(a control.Account) {
			defer wg.Done()
			if _, err := e.svc.AcceptInvite(e.ctx, a.ID, code); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(a)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("invite accepted %d times", wins)
	}
}

func TestInviteExpiryRevokeAndInvalidCodes(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	e.household(t, owner)
	b := e.account(t, "b")
	code, inv := fillInvite(t, e, owner)
	_, err := e.svc.AcceptInvite(e.ctx, b.ID, "AAAA-AAAA-AAAA")
	wantCode(t, err, control.ErrInviteInvalid)
	e.clock.Advance(7*24*time.Hour + time.Second)
	_, err = e.svc.AcceptInvite(e.ctx, b.ID, code)
	wantCode(t, err, control.ErrInviteInvalid)

	code2, inv2 := fillInvite(t, e, owner)
	if err := e.svc.RevokeInvite(e.ctx, owner.ID, inv2.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.AcceptInvite(e.ctx, b.ID, code2)
	wantCode(t, err, control.ErrInviteInvalid)
	wantCode(t, e.svc.RevokeInvite(e.ctx, owner.ID, inv2.ID), control.ErrNotFound)
	_ = inv
}

func TestHouseholdCapCountsMembersAndOpenInvites(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	h := e.household(t, owner)
	var codes []string
	// owner + 3 open invites = 4: the fourth invite is refused
	for i := 0; i < 3; i++ {
		c, _ := fillInvite(t, e, owner)
		codes = append(codes, c)
	}
	_, _, err := e.svc.CreateInvite(e.ctx, owner.ID)
	wantCode(t, err, control.ErrHouseholdFull)
	for i, c := range codes {
		m := e.account(t, "m"+string(rune('a'+i)))
		if _, err := e.svc.AcceptInvite(e.ctx, m.ID, c); err != nil {
			t.Fatalf("member %d: %v", i, err)
		}
	}
	if n, _ := e.s.CountMembers(e.ctx, h.ID); n != 4 {
		t.Fatalf("members %d", n)
	}
	_, _, err = e.svc.CreateInvite(e.ctx, owner.ID)
	wantCode(t, err, control.ErrHouseholdFull)
	// A fifth person with a valid (older) invite cannot slip in: build one directly.
	extra := e.account(t, "extra")
	if err := e.s.InsertInvite(e.ctx, control.Invite{ID: control.NewUUID(), HouseholdID: h.ID, CodeHash: control.HashCode("ZZZZ-ZZZZ-ZZZZ"), CreatedBy: owner.ID, CreatedAt: t0, ExpiresAt: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.AcceptInvite(e.ctx, extra.ID, "ZZZZ-ZZZZ-ZZZZ")
	wantCode(t, err, control.ErrHouseholdFull)
	// the full-household failure did not consume the invite
	inv, _ := e.s.InviteByHash(e.ctx, control.HashCode("ZZZZ-ZZZZ-ZZZZ"))
	if !inv.UsedAt.IsZero() {
		t.Fatal("failed join consumed the invite")
	}
}

func TestOpenInviteLimit(t *testing.T) {
	e := newEnv(t)
	e.svc.Cfg.MemberCap = 20
	e.svc.Cfg.MaxOpenInvites = 2
	owner := e.account(t, "o")
	e.household(t, owner)
	fillInvite(t, e, owner)
	fillInvite(t, e, owner)
	_, _, err := e.svc.CreateInvite(e.ctx, owner.ID)
	le, ok := control.IsLimit(err)
	if !ok || le.Limit != "open_invites" || le.Value != 2 || control.Code(err) != "limit_reached" {
		t.Fatalf("got %v", err)
	}
}

func TestAcceptRules(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	e.household(t, owner)
	other := e.account(t, "other")
	e.household(t, other)
	code, _ := fillInvite(t, e, owner)
	// an account in another household cannot join
	_, err := e.svc.AcceptInvite(e.ctx, other.ID, code)
	wantCode(t, err, control.ErrAlreadyInHousehold)
	// the owner cannot join their own household again either
	_, err = e.svc.AcceptInvite(e.ctx, owner.ID, code)
	wantCode(t, err, control.ErrAlreadyInHousehold)
	// locked account
	locked := e.account(t, "locked")
	e.s.SetAccountStatus(e.ctx, locked.ID, control.AccountLocked)
	_, err = e.svc.AcceptInvite(e.ctx, locked.ID, code)
	wantCode(t, err, control.ErrAccountLocked)
	// the invite is still usable after the failures
	b := e.account(t, "b")
	if _, err := e.svc.AcceptInvite(e.ctx, b.ID, code); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyOwnerManages(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	e.household(t, owner)
	code, _ := fillInvite(t, e, owner)
	m := e.account(t, "m")
	e.svc.AcceptInvite(e.ctx, m.ID, code)
	_, _, err := e.svc.CreateInvite(e.ctx, m.ID)
	wantCode(t, err, control.ErrNotOwner)
	wantCode(t, e.svc.RemoveMember(e.ctx, m.ID, owner.ID), control.ErrNotOwner)
	wantCode(t, e.svc.RemoveMember(e.ctx, owner.ID, owner.ID), control.ErrCannotRemoveOwner)
	stranger := e.account(t, "s")
	wantCode(t, e.svc.RemoveMember(e.ctx, owner.ID, stranger.ID), control.ErrNotFound)
	_, _, err = e.svc.CreateInvite(e.ctx, stranger.ID)
	wantCode(t, err, control.ErrNoHousehold)

	if err := e.svc.RemoveMember(e.ctx, owner.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.MemberOf(e.ctx, m.ID); !control.Is(err, control.ErrNoHousehold) {
		t.Fatalf("member still in household: %v", err)
	}
}

func TestLeave(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	e.household(t, owner)
	code, _ := fillInvite(t, e, owner)
	m := e.account(t, "m")
	e.svc.AcceptInvite(e.ctx, m.ID, code)
	wantCode(t, e.svc.Leave(e.ctx, owner.ID), control.ErrOwnerCannotLeave)
	if err := e.svc.Leave(e.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.svc.Leave(e.ctx, m.ID), control.ErrNoHousehold)
	// can join another household afterwards
	o2 := e.account(t, "o2")
	e.household(t, o2)
	c2, _ := fillInvite(t, e, o2)
	if _, err := e.svc.AcceptInvite(e.ctx, m.ID, c2); err != nil {
		t.Fatal(err)
	}
}

func TestTransferRules(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	h := e.household(t, owner)
	code, _ := fillInvite(t, e, owner)
	m := e.account(t, "m")
	e.svc.AcceptInvite(e.ctx, m.ID, code)
	outsider := e.account(t, "x")

	_, err := e.svc.Transfer(e.ctx, m.ID, owner.ID, true)
	wantCode(t, err, control.ErrNotOwner)
	_, err = e.svc.Transfer(e.ctx, owner.ID, outsider.ID, true)
	wantCode(t, err, control.ErrNotFound)
	_, err = e.svc.Transfer(e.ctx, owner.ID, owner.ID, true)
	wantCode(t, err, control.ErrInvalidRequest)
	// target without a plan needs the confirmation
	_, err = e.svc.Transfer(e.ctx, owner.ID, m.ID, false)
	wantCode(t, err, control.ErrTransferTargetNoPlan)
	// with a plan it works without
	e.plan[m.ID] = true
	got, err := e.svc.Transfer(e.ctx, owner.ID, m.ID, false)
	if err != nil || got.OwnerAccountID != m.ID {
		t.Fatalf("transfer: %v %+v", err, got)
	}
	members, _ := e.s.Members(e.ctx, h.ID)
	if members[0].AccountID != m.ID || members[0].Role != control.RoleOwner || members[1].AccountID != owner.ID || members[1].Role != control.RoleMember {
		t.Fatalf("roles after transfer: %+v", members)
	}
	hh, _ := e.s.HouseholdByID(e.ctx, h.ID)
	if hh.OwnerAccountID != m.ID {
		t.Fatal("household owner column not updated")
	}
	// old owner is now a member and can leave
	if err := e.svc.Leave(e.ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	// confirm_plan_change lets a plan-less target take over
	code2, _ := fillInvite2(t, e, m)
	z := e.account(t, "z")
	e.svc.AcceptInvite(e.ctx, z.ID, code2)
	if _, err := e.svc.Transfer(e.ctx, m.ID, z.ID, true); err != nil {
		t.Fatal(err)
	}
	hh2, _ := e.s.HouseholdByID(e.ctx, h.ID)
	if !hh2.ForcedLapseAt.Equal(t0) {
		t.Fatalf("plan-less transfer must start the lapse clock, got %v", hh2.ForcedLapseAt)
	}
}

func fillInvite2(t *testing.T, e *env, owner control.Account) (string, control.Invite) {
	return fillInvite(t, e, owner)
}

func TestViewHousehold(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	e.household(t, owner)
	code, _ := fillInvite(t, e, owner)
	fillInvite(t, e, owner)
	m := e.account(t, "m")
	e.svc.AcceptInvite(e.ctx, m.ID, code)
	ov, err := e.svc.ViewHousehold(e.ctx, owner.ID)
	if err != nil || ov.YourRole != "owner" || len(ov.Members) != 2 || len(ov.PendingInvites) != 1 {
		t.Fatalf("owner view: %+v %v", ov, err)
	}
	mv, _ := e.svc.ViewHousehold(e.ctx, m.ID)
	if mv.YourRole != "member" || mv.PendingInvites != nil {
		t.Fatalf("member view must not list invites: %+v", mv)
	}
	_, err = e.svc.ViewHousehold(e.ctx, e.account(t, "n").ID)
	wantCode(t, err, control.ErrNoHousehold)
}

func TestDevicesAndAuthenticate(t *testing.T) {
	e := newEnv(t)
	a := e.account(t, "a")
	d, tok, err := e.svc.CreateDevice(e.ctx, a.ID, "Ama's iPhone")
	if err != nil || !strings.HasPrefix(tok, "st_") || len(tok) < 40 {
		t.Fatalf("token %q %v", tok, err)
	}
	if d.TokenHash == tok || d.TokenHash != control.HashSecret(tok) {
		t.Fatal("only the hash is stored")
	}
	p, err := e.svc.Authenticate(e.ctx, tok)
	if err != nil || p.Account.ID != a.ID || p.Device.ID != d.ID {
		t.Fatalf("auth: %v", err)
	}
	if _, err := e.svc.Authenticate(e.ctx, "st_nope"); !control.Is(err, control.ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
	d2, tok2, _ := e.svc.CreateDevice(e.ctx, a.ID, "iPad")
	b := e.account(t, "b")
	// a device of another account cannot be revoked
	if ok, _ := e.s.RevokeDevice(e.ctx, d.ID, b.ID, t0); ok {
		t.Fatal("revoked someone else's device")
	}
	if n, _ := e.s.RevokeAccountDevices(e.ctx, a.ID, d.ID, t0); n != 1 {
		t.Fatalf("revoked %d", n)
	}
	if _, err := e.svc.Authenticate(e.ctx, tok2); !control.Is(err, control.ErrNotFound) {
		t.Fatal("revoked token still works")
	}
	if _, err := e.svc.Authenticate(e.ctx, tok); err != nil {
		t.Fatal("kept device must still work")
	}
	_ = d2
	e.s.SetAccountStatus(e.ctx, a.ID, control.AccountLocked)
	if _, err := e.svc.Authenticate(e.ctx, tok); !control.Is(err, control.ErrAccountLocked) {
		t.Fatalf("locked: %v", err)
	}
	list, _ := e.s.ListDevices(e.ctx, a.ID)
	if len(list) != 1 {
		t.Fatalf("list: %d", len(list))
	}
}

func TestAccountDeletionTwoStages(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	h := e.household(t, owner)
	e.s.SetRefreshToken(e.ctx, owner.ID, []byte("sealed"))
	keep, _, _ := e.svc.CreateDevice(e.ctx, owner.ID, "phone")
	_, tok2, _ := e.svc.CreateDevice(e.ctx, owner.ID, "ipad")

	_, err := e.svc.RequestAccountDeletion(e.ctx, owner.ID, "nope", true, keep.ID)
	wantCode(t, err, control.ErrInvalidRequest)
	_, err = e.svc.RequestAccountDeletion(e.ctx, owner.ID, control.DeleteConfirmText, false, keep.ID)
	wantCode(t, err, control.ErrOwnerMustConfirm)

	st, err := e.svc.RequestAccountDeletion(e.ctx, owner.ID, control.DeleteConfirmText, true, keep.ID)
	if err != nil || !st.RequestedAt.Equal(t0) || !st.DeleteAfter.Equal(t0.Add(7*24*time.Hour)) {
		t.Fatalf("status %+v %v", st, err)
	}
	if _, err := e.svc.Authenticate(e.ctx, tok2); err == nil {
		t.Fatal("other devices must be revoked")
	}
	pend, _ := e.s.PendingRevocations(e.ctx)
	if len(pend) != 1 || pend[0].AccountID != owner.ID || string(pend[0].TokenEnc) != "sealed" {
		t.Fatalf("pending revocations: %+v", pend)
	}
	hh, _ := e.s.HouseholdByID(e.ctx, h.ID)
	if !hh.DeletionRequestedAt.Equal(t0) {
		t.Fatal("household not flagged")
	}
	// idempotent, even later
	e.clock.Advance(time.Hour)
	st2, err := e.svc.RequestAccountDeletion(e.ctx, owner.ID, control.DeleteConfirmText, true, keep.ID)
	if err != nil || !st2.RequestedAt.Equal(st.RequestedAt) {
		t.Fatalf("not idempotent: %+v %v", st2, err)
	}
	// cancel
	if err := e.svc.CancelAccountDeletion(e.ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	hh, _ = e.s.HouseholdByID(e.ctx, h.ID)
	a, _ := e.s.AccountByID(e.ctx, owner.ID)
	if !hh.DeletionRequestedAt.IsZero() || !a.DeletionRequestedAt.IsZero() {
		t.Fatal("cancel did not clear")
	}
	wantCode(t, e.svc.CancelAccountDeletion(e.ctx, owner.ID), control.ErrDeletionNotPending)
}

func TestMemberDeletionDetaches(t *testing.T) {
	e := newEnv(t)
	owner := e.account(t, "o")
	h := e.household(t, owner)
	code, _ := fillInvite(t, e, owner)
	m := e.account(t, "m")
	e.svc.AcceptInvite(e.ctx, m.ID, code)
	_, err := e.svc.RequestAccountDeletion(e.ctx, m.ID, control.DeleteConfirmText, true, 0)
	wantCode(t, err, control.ErrNotHouseholdOwnerDelete)
	if _, err := e.svc.RequestAccountDeletion(e.ctx, m.ID, control.DeleteConfirmText, false, 0); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.s.CountMembers(e.ctx, h.ID); n != 1 {
		t.Fatalf("members %d", n)
	}
	hh, _ := e.s.HouseholdByID(e.ctx, h.ID)
	if !hh.DeletionRequestedAt.IsZero() {
		t.Fatal("member deletion must leave the household intact")
	}
}

func TestNonceSingleUseAndExpiry(t *testing.T) {
	e := newEnv(t)
	h := control.HashSecret("n1")
	if err := e.s.InsertNonce(e.ctx, h, t0.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.s.ConsumeNonce(e.ctx, "unknown", t0); ok {
		t.Fatal("unknown nonce accepted")
	}
	if ok, _ := e.s.ConsumeNonce(e.ctx, h, t0); !ok {
		t.Fatal("fresh nonce refused")
	}
	if ok, _ := e.s.ConsumeNonce(e.ctx, h, t0); ok {
		t.Fatal("nonce reused")
	}
	h2 := control.HashSecret("n2")
	e.s.InsertNonce(e.ctx, h2, t0.Add(time.Minute))
	if ok, _ := e.s.ConsumeNonce(e.ctx, h2, t0.Add(2*time.Minute)); ok {
		t.Fatal("expired nonce accepted")
	}
}

func TestAuditIsAppendOnly(t *testing.T) {
	e := newEnv(t)
	if err := e.s.AppendAudit(e.ctx, "casprine", "grant.create", "acc1", map[string]any{"days": 90}); err != nil {
		t.Fatal(err)
	}
	list, _ := e.s.ListAudit(e.ctx, control.AuditFilter{Subject: "acc1"})
	if len(list) != 1 || list[0].Actor != "casprine" || !strings.Contains(list[0].Details, `"days":90`) {
		t.Fatalf("%+v", list)
	}
	// raw UPDATE / DELETE must be refused by triggers
	db := openRaw(t, e)
	if _, err := db.Exec(`UPDATE audit_log SET actor='x'`); err == nil {
		t.Fatal("update allowed")
	}
	if _, err := db.Exec(`DELETE FROM audit_log`); err == nil {
		t.Fatal("delete allowed")
	}
}

func TestCountersAndNotificationsAndLifecycleEvents(t *testing.T) {
	e := newEnv(t)
	v, _ := e.s.AddCounter(e.ctx, "h1", "2026-10", "receipts", 2)
	v, _ = e.s.AddCounter(e.ctx, "h1", "2026-10", "receipts", 3)
	if v != 5 {
		t.Fatalf("counter %d", v)
	}
	v, _ = e.s.AddCounter(e.ctx, "h1", "2026-10", "receipts", -100)
	if v != 0 {
		t.Fatalf("floor %d", v)
	}
	e.s.SetCounter(e.ctx, "h1", "gauge", "storage_bytes", 123)
	if g, _ := e.s.Counter(e.ctx, "h1", "gauge", "storage_bytes"); g != 123 {
		t.Fatal(g)
	}
	n := control.NotificationRecord{UUID: "u1", Type: "DID_RENEW"}
	if ok, _ := e.s.InsertNotification(e.ctx, n); !ok {
		t.Fatal("first insert")
	}
	if ok, _ := e.s.InsertNotification(e.ctx, n); ok {
		t.Fatal("duplicate insert")
	}
	if ok, _ := e.s.InsertLifecycleEvent(e.ctx, "h1", "lapse_day_0", "2026-10-14", t0); !ok {
		t.Fatal("event")
	}
	if ok, _ := e.s.InsertLifecycleEvent(e.ctx, "h1", "lapse_day_0", "2026-10-14", t0); ok {
		t.Fatal("event dup")
	}
}

func TestEraseAccountKeepsLedgerLink(t *testing.T) {
	e := newEnv(t)
	a, _, _ := e.svc.UpsertAccount(e.ctx, "sub-z", "z@example.com", false, "Zed")
	e.s.SetRefreshToken(e.ctx, a.ID, []byte("tok"))
	if err := e.s.EraseAccount(e.ctx, a.ID, t0); err != nil {
		t.Fatal(err)
	}
	got, _ := e.s.AccountByID(e.ctx, a.ID)
	if got.Status != control.AccountDeleted || got.Email != "" || got.DisplayName != "" || len(got.RefreshTokenEnc) != 0 || got.AppleSub == "sub-z" {
		t.Fatalf("not erased: %+v", got)
	}
	if got.AppAccountToken != a.AppAccountToken {
		t.Fatal("ledger token must survive")
	}
	// the Apple id can sign up again as a fresh account
	b, created, err := e.svc.UpsertAccount(e.ctx, "sub-z", "", false, "")
	if err != nil || !created || b.ID == a.ID {
		t.Fatalf("re-signup: %v %v", err, created)
	}
}

func TestTxRollsBack(t *testing.T) {
	e := newEnv(t)
	boom := errors.New("boom")
	err := e.s.Tx(e.ctx, func(tx *control.Store) error {
		if _, err := tx.InsertAccount(e.ctx, control.Account{AppleSub: "rolled"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := e.s.AccountBySub(e.ctx, "rolled"); !control.Is(err, control.ErrNotFound) {
		t.Fatal("tx not rolled back")
	}
}
