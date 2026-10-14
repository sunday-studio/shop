package hostedapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"shop/internal/hosted/lifecycle"
)

// LogSink writes reminders to the log (the default consumer).
type LogSink struct{ Log *slog.Logger }

func (s LogSink) Emit(_ context.Context, r lifecycle.Reminder) error {
	l := s.Log
	if l == nil {
		l = slog.Default()
	}
	l.Info("reminder due", "kind", r.Kind, "household", r.HouseholdID, "delete_after", r.DeleteAfter, "deliverable", r.EmailDeliverable)
	return nil
}

// WebhookSink POSTs every reminder as JSON to a URL (a mailer or chat bridge on the operator's side).
// The body carries the owner's email when it is deliverable; the URL is operator configuration.
type WebhookSink struct {
	URL    string
	Secret string // sent as the X-Shop-Secret header when set
	Client *http.Client
}

func (s WebhookSink) Emit(ctx context.Context, r lifecycle.Reminder) error {
	body, _ := json.Marshal(map[string]any{"kind": r.Kind, "household_id": r.HouseholdID, "owner_account_id": r.OwnerAccountID,
		"due_at": r.DueAt, "delete_after": r.DeleteAfter, "email": r.Email, "email_deliverable": r.EmailDeliverable})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.Secret != "" {
		req.Header.Set("X-Shop-Secret", s.Secret)
	}
	c := s.Client
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("reminder webhook answered %d", resp.StatusCode)
	}
	return nil
}

// SMTPSink mails the owner. Reminders without a deliverable address are skipped (the in-app banner
// is the main channel). Send is replaceable for tests.
type SMTPSink struct {
	Addr, From, User, Pass string
	Support                string // support address shown in the mail
	Send                   func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

func (s SMTPSink) Emit(_ context.Context, r lifecycle.Reminder) error {
	if !r.EmailDeliverable || r.Email == "" {
		return nil
	}
	if strings.ContainsAny(r.Email, "\r\n") {
		return nil
	}
	subject, line := "Your Shop subscription ended", "Your Shop household is read-only. You can still look at everything and export it."
	switch r.Kind {
	case lifecycle.ReminderLapseDay14:
		subject = "Your Shop household is still read-only"
	case lifecycle.ReminderLapseDay25:
		subject = "Export your Shop data soon"
	case lifecycle.ReminderLapseDay29:
		subject = "Last day: your Shop data will be deleted"
	}
	body := fmt.Sprintf("%s\r\n\r\nThe household will be deleted after %s unless you subscribe again.\r\nQuestions: %s\r\n", line, r.DeleteAfter.UTC().Format("2 January 2006"), s.Support)
	msg := []byte("From: " + s.From + "\r\nTo: " + r.Email + "\r\nSubject: " + subject + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body)
	var auth smtp.Auth
	if s.User != "" {
		host := s.Addr
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		auth = smtp.PlainAuth("", s.User, s.Pass, host)
	}
	send := s.Send
	if send == nil {
		send = smtp.SendMail
	}
	return send(s.Addr, auth, s.From, []string{r.Email}, msg)
}

// MultiSink fans a reminder out to several sinks; it fails when any sink fails (the job retries).
type MultiSink []lifecycle.ReminderSink

func (m MultiSink) Emit(ctx context.Context, r lifecycle.Reminder) error {
	var first error
	for _, s := range m {
		if err := s.Emit(ctx, r); err != nil && first == nil {
			first = err
		}
	}
	return first
}
