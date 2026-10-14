package lifecycle

import (
	"context"
	"sync"
	"time"
)

// Reminder kinds sent during a lapse: day 0, 14, 25 and 29 after read-only starts.
const (
	ReminderLapseDay0  = "lapse_day_0"
	ReminderLapseDay14 = "lapse_day_14"
	ReminderLapseDay25 = "lapse_day_25"
	ReminderLapseDay29 = "lapse_day_29"
)

// Reminder is a "reminder due" record. The job does not send email; a mailer (later) consumes
// these. In-app banners come from the plan (delete_after), not from here.
type Reminder struct {
	HouseholdID      string
	OwnerAccountID   string
	Kind             string
	Anchor           string // the read-only start, so a new lapse produces new reminders
	DueAt            time.Time
	DeleteAfter      time.Time
	Email            string // empty when the address is not deliverable
	EmailDeliverable bool
}

// ReminderSink receives due reminders. Emit must be safe to call again for the same record
// (the job records an event only after Emit succeeds, so a crash can repeat one).
type ReminderSink interface {
	Emit(ctx context.Context, r Reminder) error
}

// MemorySink collects reminders in memory (tests and dry runs).
type MemorySink struct {
	mu   sync.Mutex
	List []Reminder
	Fail error
}

func (m *MemorySink) Emit(_ context.Context, r Reminder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.List = append(m.List, r)
	return nil
}

// Kinds returns the emitted kinds in order.
func (m *MemorySink) Kinds() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, r := range m.List {
		out = append(out, r.Kind)
	}
	return out
}
