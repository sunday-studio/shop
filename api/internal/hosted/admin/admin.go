// Package admin is the operator command line of the hosted mode: complimentary grants, account
// lookup by support code, the audit log, and a manual lifecycle run. It is a plain function
// (Run) the future main can call as `shop-hosted admin ...`; it talks to the control database
// directly and writes the same audit rows as the HTTP admin routes will.
//
//	grant create  --support-code K7PQ-4MXD --reason "Friend, beta tester" [--days 90 | --expires-at 2027-01-12] [--idempotency-key k]
//	grant list    [--status active|expired|revoked] [--account-id ID]
//	grant extend  GRANT_ID --expires-at 2027-02-01 | --days N  --reason "..."
//	grant revoke  GRANT_ID --reason "..."
//	account find  --support-code K7PQ-4MXD | --account-id ID | --email a@b
//	audit tail    [--subject ID] [--action grant.create] [--limit 50]
//	lifecycle run [--dry-run]
//
// Every mutating command needs an operator name: --operator NAME or the SHOP_OPERATOR variable
// (Env.Operator). Add --json to any command for machine output.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/hosted/grants"
	"shop/internal/hosted/lifecycle"
)

// Env is what the commands need.
type Env struct {
	Ctl       *control.Service
	Grants    *grants.Service
	Calc      *billing.Calculator
	Lifecycle *lifecycle.Runner // optional
	Operator  string            // default operator (e.g. from SHOP_OPERATOR)
	Out, Err  io.Writer
}

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = `usage: admin <command>
  grant create|list|extend|revoke
  account find
  audit tail
  lifecycle run [--dry-run]
`

// Run executes one command and returns the process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	if len(args) < 2 {
		fmt.Fprint(env.Err, usage)
		return exitUsage
	}
	var err error
	switch args[0] + " " + args[1] {
	case "grant create":
		err = grantCreate(ctx, args[2:], env)
	case "grant list":
		err = grantList(ctx, args[2:], env)
	case "grant extend":
		err = grantExtend(ctx, args[2:], env)
	case "grant revoke":
		err = grantRevoke(ctx, args[2:], env)
	case "account find":
		err = accountFind(ctx, args[2:], env)
	case "audit tail":
		err = auditTail(ctx, args[2:], env)
	case "lifecycle run":
		err = lifecycleRun(ctx, args[2:], env)
	default:
		fmt.Fprint(env.Err, usage)
		return exitUsage
	}
	var ue usageError
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &ue):
		fmt.Fprintf(env.Err, "error: %s\n", ue)
		return exitUsage
	default:
		fmt.Fprintf(env.Err, "error: %v\n", err)
		return exitError
	}
}

type usageError string

func (u usageError) Error() string { return string(u) }

func newFlags(name string, env Env) (*flag.FlagSet, *bool, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(env.Err)
	asJSON := fs.Bool("json", false, "machine-readable output")
	op := fs.String("operator", env.Operator, "operator name for the audit log")
	return fs, asJSON, op
}

func parse(fs *flag.FlagSet, args []string, wantPositional int) ([]string, error) {
	// allow the positional id before or after the flags
	var pos, rest []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			rest = append(rest, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !isBool(fs, args[i]) {
				i++
				rest = append(rest, args[i])
			}
		} else {
			pos = append(pos, args[i])
		}
	}
	if err := fs.Parse(rest); err != nil {
		return nil, usageError(err.Error())
	}
	if len(pos) != wantPositional {
		return nil, usageError(fmt.Sprintf("expected %d argument(s), got %d", wantPositional, len(pos)))
	}
	return pos, nil
}

func isBool(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(strings.TrimLeft(name, "-"))
	if f == nil {
		return false
	}
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

func requireOperator(op string) (string, error) {
	op = strings.TrimSpace(op)
	if op == "" || len(op) > 40 {
		return "", usageError("an operator name is required: --operator NAME (1 to 40 characters) or SHOP_OPERATOR")
	}
	return op, nil
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, usageError(fmt.Sprintf("cannot read %q as a date (use 2027-01-12 or 2027-01-12T10:00:00Z)", s))
}

// GrantJSON is the grant object of the contract (section 6.2).
type GrantJSON struct {
	ID            string     `json:"id"`
	AccountID     string     `json:"account_id"`
	SupportCode   string     `json:"support_code"`
	Key           string     `json:"key"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	Reason        string     `json:"reason"`
	CreatedBy     string     `json:"created_by"`
	RevokedAt     *time.Time `json:"revoked_at"`
	RevokedBy     *string    `json:"revoked_by"`
	RevokedReason *string    `json:"revoked_reason"`
}

func (e Env) grantJSON(ctx context.Context, g control.Grant) GrantJSON {
	out := GrantJSON{ID: g.ID, AccountID: g.AccountID, Key: g.Key, Status: string(grants.StatusAt(g, e.Ctl.S.Now())),
		CreatedAt: g.CreatedAt, ExpiresAt: g.ExpiresAt, Reason: g.Reason, CreatedBy: g.CreatedBy}
	if a, err := e.Ctl.S.AccountByID(ctx, g.AccountID); err == nil {
		out.SupportCode = a.SupportCode
	}
	if !g.RevokedAt.IsZero() {
		t, by, why := g.RevokedAt, g.RevokedBy, g.RevokedReason
		out.RevokedAt, out.RevokedBy, out.RevokedReason = &t, &by, &why
	}
	return out
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printGrants(w io.Writer, gs []GrantJSON) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSUPPORT CODE\tSTATUS\tEXPIRES\tCREATED BY\tREASON")
	for _, g := range gs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", g.ID, g.SupportCode, g.Status, control.FormatTime(g.ExpiresAt), g.CreatedBy, g.Reason)
	}
	tw.Flush()
}

func grantCreate(ctx context.Context, args []string, env Env) error {
	fs, asJSON, op := newFlags("grant create", env)
	support := fs.String("support-code", "", "the account's support code (Account > About)")
	account := fs.String("account-id", "", "the account id")
	days := fs.Int("days", 0, "length in days (default 90, maximum 366)")
	expires := fs.String("expires-at", "", "end date, instead of --days")
	reason := fs.String("reason", "", "why (required, internal)")
	key := fs.String("key", "hosted", "entitlement key")
	idem := fs.String("idempotency-key", "", "a repeat with the same key returns the first grant")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	operator, err := requireOperator(*op)
	if err != nil {
		return err
	}
	if *reason == "" {
		return usageError("--reason is required")
	}
	if *support == "" && *account == "" {
		return usageError("--support-code or --account-id is required")
	}
	in := grants.CreateInput{AccountID: *account, SupportCode: *support, Key: *key, Days: *days, Reason: *reason, Actor: operator, IdempotencyKey: *idem}
	if *expires != "" {
		if in.ExpiresAt, err = parseTime(*expires); err != nil {
			return err
		}
	}
	g, replay, err := env.Grants.Create(ctx, in)
	if err != nil {
		return err
	}
	gj := env.grantJSON(ctx, g)
	if *asJSON {
		return printJSON(env.Out, gj)
	}
	verb := "created"
	if replay {
		verb = "already existed (same idempotency key)"
	}
	fmt.Fprintf(env.Out, "grant %s %s for %s until %s\n", g.ID, verb, gj.SupportCode, control.FormatTime(g.ExpiresAt))
	return nil
}

func grantList(ctx context.Context, args []string, env Env) error {
	fs, asJSON, _ := newFlags("grant list", env)
	status := fs.String("status", "", "active, expired or revoked")
	account := fs.String("account-id", "", "only this account")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	switch *status {
	case "", "active", "expired", "revoked":
	default:
		return usageError("--status must be active, expired or revoked")
	}
	gs, err := env.Grants.List(ctx, grants.ListFilter{Status: grants.Status(*status), AccountID: *account})
	if err != nil {
		return err
	}
	out := make([]GrantJSON, 0, len(gs))
	for _, g := range gs {
		out = append(out, env.grantJSON(ctx, g))
	}
	if *asJSON {
		return printJSON(env.Out, map[string]any{"grants": out})
	}
	printGrants(env.Out, out)
	return nil
}

func grantExtend(ctx context.Context, args []string, env Env) error {
	fs, asJSON, op := newFlags("grant extend", env)
	expires := fs.String("expires-at", "", "new end date")
	days := fs.Int("days", 0, "extend by this many days from the current end")
	reason := fs.String("reason", "", "why (required)")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	operator, err := requireOperator(*op)
	if err != nil {
		return err
	}
	if *reason == "" {
		return usageError("--reason is required")
	}
	if (*expires == "") == (*days == 0) {
		return usageError("give exactly one of --expires-at or --days")
	}
	var to time.Time
	if *expires != "" {
		if to, err = parseTime(*expires); err != nil {
			return err
		}
	} else {
		g, err := env.Ctl.S.GrantByID(ctx, pos[0])
		if err != nil {
			return err
		}
		to = g.ExpiresAt.Add(time.Duration(*days) * 24 * time.Hour)
	}
	g, err := env.Grants.Extend(ctx, pos[0], to, *reason, operator)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(env.Out, env.grantJSON(ctx, g))
	}
	fmt.Fprintf(env.Out, "grant %s now ends %s\n", g.ID, control.FormatTime(g.ExpiresAt))
	return nil
}

func grantRevoke(ctx context.Context, args []string, env Env) error {
	fs, asJSON, op := newFlags("grant revoke", env)
	reason := fs.String("reason", "", "why (required)")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	operator, err := requireOperator(*op)
	if err != nil {
		return err
	}
	if *reason == "" {
		return usageError("--reason is required")
	}
	g, err := env.Grants.Revoke(ctx, pos[0], *reason, operator)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(env.Out, env.grantJSON(ctx, g))
	}
	fmt.Fprintf(env.Out, "grant %s revoked\n", g.ID)
	return nil
}

// AccountJSON is one row of the account lookup (contract 6.3).
type AccountJSON struct {
	ID          string    `json:"id"`
	SupportCode string    `json:"support_code"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
	PlanState   string    `json:"plan_state"`
	HouseholdID *string   `json:"household_id"`
}

func accountFind(ctx context.Context, args []string, env Env) error {
	fs, asJSON, _ := newFlags("account find", env)
	support := fs.String("support-code", "", "support code")
	id := fs.String("account-id", "", "account id")
	email := fs.String("email", "", "email address (may be a private relay address)")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	var found []control.Account
	var err error
	switch {
	case *support != "":
		var a control.Account
		if a, err = env.Ctl.S.AccountBySupportCode(ctx, *support); err == nil {
			found = append(found, a)
		}
	case *id != "":
		var a control.Account
		if a, err = env.Ctl.S.AccountByID(ctx, *id); err == nil {
			found = append(found, a)
		}
	case *email != "":
		found, err = env.Ctl.S.AccountsByEmail(ctx, *email)
	default:
		return usageError("give --support-code, --account-id or --email")
	}
	if err != nil && !errors.Is(err, control.ErrNotFound) {
		return err
	}
	out := make([]AccountJSON, 0, len(found))
	for _, a := range found {
		row := AccountJSON{ID: a.ID, SupportCode: a.SupportCode, Email: a.Email, DisplayName: a.DisplayName, CreatedAt: a.CreatedAt}
		if p, perr := env.Calc.ForAccount(ctx, a.ID); perr == nil {
			row.PlanState = p.State
		}
		if h, _, herr := env.Ctl.S.HouseholdOfAccount(ctx, a.ID); herr == nil {
			hid := h.ID
			row.HouseholdID = &hid
		}
		out = append(out, row)
	}
	if *asJSON {
		return printJSON(env.Out, map[string]any{"accounts": out})
	}
	if len(out) == 0 {
		fmt.Fprintln(env.Out, "no account found")
		return nil
	}
	tw := tabwriter.NewWriter(env.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSUPPORT CODE\tPLAN\tEMAIL\tNAME\tHOUSEHOLD")
	for _, r := range out {
		hh := "-"
		if r.HouseholdID != nil {
			hh = *r.HouseholdID
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.SupportCode, r.PlanState, r.Email, r.DisplayName, hh)
	}
	return tw.Flush()
}

func auditTail(ctx context.Context, args []string, env Env) error {
	fs, asJSON, _ := newFlags("audit tail", env)
	subject := fs.String("subject", "", "account or household id")
	action := fs.String("action", "", "e.g. grant.create")
	limit := fs.Int("limit", 50, "rows")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	entries, err := env.Ctl.S.ListAudit(ctx, control.AuditFilter{Subject: *subject, Action: *action, Limit: *limit})
	if err != nil {
		return err
	}
	if *asJSON {
		type row struct {
			ID      int64           `json:"id"`
			At      time.Time       `json:"at"`
			Actor   string          `json:"actor"`
			Action  string          `json:"action"`
			Subject string          `json:"subject"`
			Details json.RawMessage `json:"details"`
		}
		rows := make([]row, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, row{e.ID, e.At, e.Actor, e.Action, e.Subject, json.RawMessage(e.Details)})
		}
		return printJSON(env.Out, map[string]any{"entries": rows})
	}
	for _, e := range entries {
		fmt.Fprintf(env.Out, "%s  %-12s %-28s %s  %s\n", control.FormatTime(e.At), e.Actor, e.Action, e.Subject, e.Details)
	}
	return nil
}

func lifecycleRun(ctx context.Context, args []string, env Env) error {
	fs, asJSON, op := newFlags("lifecycle run", env)
	dry := fs.Bool("dry-run", false, "print what would happen, change nothing")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	if env.Lifecycle == nil {
		return errors.New("lifecycle runner is not configured")
	}
	if !*dry {
		if _, err := requireOperator(*op); err != nil {
			return err
		}
	}
	rep, err := env.Lifecycle.Run(ctx, lifecycle.Options{DryRun: *dry})
	if err != nil {
		return err
	}
	if *asJSON {
		type act struct{ Kind, Subject, Detail string }
		acts := make([]act, 0, len(rep.Actions))
		for _, a := range rep.Actions {
			acts = append(acts, act{a.Kind, a.Subject, a.Detail})
		}
		return printJSON(env.Out, map[string]any{"dry_run": rep.DryRun, "now": rep.Now, "actions": acts})
	}
	mode := ""
	if rep.DryRun {
		mode = " (dry run, nothing changed)"
	}
	fmt.Fprintf(env.Out, "lifecycle run at %s%s: %d action(s)\n", control.FormatTime(rep.Now), mode, len(rep.Actions))
	for _, a := range rep.Actions {
		fmt.Fprintf(env.Out, "  %-14s %s  %s\n", a.Kind, a.Subject, a.Detail)
	}
	return nil
}
