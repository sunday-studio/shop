// Package limits is the usage meter and the limit checks of the hosted mode: receipts per
// month and per day, storage per household, upload size, queued jobs, exports per day, open
// invites, and concurrent requests. Limits are configurable (defaults below, overridable per
// household) and a hit returns the contract's limit_reached / file_too_large / rate_limited
// errors with the numbers the app shows.
//
// Counters live in the control database (usage_counters), so they survive restarts and are
// updated in one transaction together with the check (no check-then-act race).
package limits

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"shop/internal/hosted/control"
)

// Limits are the per-household limits. A value of 0 or less means "no limit" (JSON null).
type Limits struct {
	HouseholdMembers     int64 `json:"household_members"`
	MaxUploadBytes       int64 `json:"max_upload_bytes"`
	ReceiptsPerMonth     int64 `json:"receipts_per_month"`
	ReceiptsPerMonthSoft int64 `json:"receipts_per_month_soft"`
	ReceiptsPerDay       int64 `json:"receipts_per_day"`
	StorageBytes         int64 `json:"storage_bytes"`
	MaxQueuedJobs        int64 `json:"max_queued_jobs"`
	ExportsPerDay        int64 `json:"exports_per_day"`
	OpenInvites          int64 `json:"open_invites"`
	// ConcurrentRequests is not part of the public limits object.
	ConcurrentRequests int64 `json:"-"`
}

// Defaults are the launch values (contract 1.2, hosted plan section 6).
func Defaults() Limits {
	return Limits{
		HouseholdMembers: 4, MaxUploadBytes: 10 << 20,
		ReceiptsPerMonth: 300, ReceiptsPerMonthSoft: 150, ReceiptsPerDay: 25,
		StorageBytes: 2 << 30, MaxQueuedJobs: 50, ExportsPerDay: 3, OpenInvites: 5,
		ConcurrentRequests: 16,
	}
}

// Public is the limits object of GET /v1/server-info and GET /v1/account (null = unlimited).
type Public struct {
	HouseholdMembers     *int64 `json:"household_members"`
	MaxUploadBytes       *int64 `json:"max_upload_bytes"`
	ReceiptsPerMonth     *int64 `json:"receipts_per_month"`
	ReceiptsPerMonthSoft *int64 `json:"receipts_per_month_soft"`
	ReceiptsPerDay       *int64 `json:"receipts_per_day"`
	StorageBytes         *int64 `json:"storage_bytes"`
	MaxQueuedJobs        *int64 `json:"max_queued_jobs"`
	ExportsPerDay        *int64 `json:"exports_per_day"`
	OpenInvites          *int64 `json:"open_invites"`
}

func ptr(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}

// Public renders the limits for the API.
func (l Limits) Public() Public {
	return Public{ptr(l.HouseholdMembers), ptr(l.MaxUploadBytes), ptr(l.ReceiptsPerMonth), ptr(l.ReceiptsPerMonthSoft),
		ptr(l.ReceiptsPerDay), ptr(l.StorageBytes), ptr(l.MaxQueuedJobs), ptr(l.ExportsPerDay), ptr(l.OpenInvites)}
}

// ControlConfig applies the member and invite limits to the household invariants.
func (l Limits) ControlConfig(base control.Config) control.Config {
	if l.HouseholdMembers > 0 {
		base.MemberCap = int(l.HouseholdMembers)
	}
	if l.OpenInvites > 0 {
		base.MaxOpenInvites = int(l.OpenInvites)
	}
	return base
}

// ApplyOverride merges a household's override JSON (households.limits_override) over l.
// Keys are the public names; a number replaces the value and null means unlimited.
// An empty string changes nothing.
func (l Limits) ApplyOverride(raw string) (Limits, error) {
	if raw == "" {
		return l, nil
	}
	var m map[string]*int64
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return l, err
	}
	set := func(key string, dst *int64) {
		if v, ok := m[key]; ok {
			if v == nil {
				*dst = 0
			} else {
				*dst = *v
			}
		}
	}
	set("household_members", &l.HouseholdMembers)
	set("max_upload_bytes", &l.MaxUploadBytes)
	set("receipts_per_month", &l.ReceiptsPerMonth)
	set("receipts_per_month_soft", &l.ReceiptsPerMonthSoft)
	set("receipts_per_day", &l.ReceiptsPerDay)
	set("storage_bytes", &l.StorageBytes)
	set("max_queued_jobs", &l.MaxQueuedJobs)
	set("exports_per_day", &l.ExportsPerDay)
	set("open_invites", &l.OpenInvites)
	set("concurrent_requests", &l.ConcurrentRequests)
	return l, nil
}

// Counter names.
const (
	metricReceipts = "receipts"
	metricExports  = "exports"
	metricStorage  = "storage_bytes"
	periodGauge    = "gauge"
)

func monthKey(t time.Time) string { return t.UTC().Format("2006-01") }
func dayKey(t time.Time) string   { return t.UTC().Format("2006-01-02") }

// MonthResetsAt is the start of the next month, UTC.
func MonthResetsAt(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}

// DayResetsAt is the next midnight, UTC.
func DayResetsAt(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
}

// Meter keeps the counters and answers the limit questions.
type Meter struct {
	S        *control.Store
	Defaults Limits
}

// NewMeter uses Defaults() unless defaults is given.
func NewMeter(s *control.Store, defaults ...Limits) *Meter {
	d := Defaults()
	if len(defaults) > 0 {
		d = defaults[0]
	}
	return &Meter{S: s, Defaults: d}
}

// LimitsFor returns the defaults with the household's override applied.
func (m *Meter) LimitsFor(ctx context.Context, householdID string) (Limits, error) {
	h, err := m.S.HouseholdByID(ctx, householdID)
	if err != nil {
		return Limits{}, err
	}
	return m.Defaults.ApplyOverride(h.LimitsOverride)
}

func over(v, limit int64) bool { return limit > 0 && v > limit }

// CheckUpload refuses a single file over max_upload_bytes (413 file_too_large). Call it before
// reading the body when the size is known and again on the real size.
func (m *Meter) CheckUpload(ctx context.Context, householdID string, size int64) error {
	l, err := m.LimitsFor(ctx, householdID)
	if err != nil {
		return err
	}
	if over(size, l.MaxUploadBytes) {
		return &control.LimitError{Limit: "max_upload_bytes", Value: l.MaxUploadBytes, Current: size, FileTooLarge: true}
	}
	return nil
}

// ReceiptStatus is what RecordReceipt reports on success.
type ReceiptStatus struct {
	ThisMonth   int64
	Today       int64
	StorageUsed int64
	SoftReached bool // receipts this month have reached receipts_per_month_soft (the app warns)
}

// RecordReceipt checks and counts one receipt upload of size bytes in a single transaction.
// Order of checks: receipts_per_month, receipts_per_day, storage_bytes. On a hit nothing is
// counted. Call Release with the same size if the upload fails afterwards.
func (m *Meter) RecordReceipt(ctx context.Context, householdID string, size int64) (ReceiptStatus, error) {
	var st ReceiptStatus
	l, err := m.LimitsFor(ctx, householdID)
	if err != nil {
		return st, err
	}
	now := m.S.Now()
	err = m.S.Tx(ctx, func(tx *control.Store) error {
		month, err := tx.Counter(ctx, householdID, monthKey(now), metricReceipts)
		if err != nil {
			return err
		}
		day, err := tx.Counter(ctx, householdID, dayKey(now), metricReceipts)
		if err != nil {
			return err
		}
		used, err := tx.Counter(ctx, householdID, periodGauge, metricStorage)
		if err != nil {
			return err
		}
		if over(month+1, l.ReceiptsPerMonth) {
			return &control.LimitError{Limit: "receipts_per_month", Value: l.ReceiptsPerMonth, Current: month, ResetsAt: MonthResetsAt(now)}
		}
		if over(day+1, l.ReceiptsPerDay) {
			return &control.LimitError{Limit: "receipts_per_day", Value: l.ReceiptsPerDay, Current: day, ResetsAt: DayResetsAt(now)}
		}
		if over(used+size, l.StorageBytes) {
			return &control.LimitError{Limit: "storage_bytes", Value: l.StorageBytes, Current: used}
		}
		if st.ThisMonth, err = tx.AddCounter(ctx, householdID, monthKey(now), metricReceipts, 1); err != nil {
			return err
		}
		if st.Today, err = tx.AddCounter(ctx, householdID, dayKey(now), metricReceipts, 1); err != nil {
			return err
		}
		if st.StorageUsed, err = tx.AddCounter(ctx, householdID, periodGauge, metricStorage, size); err != nil {
			return err
		}
		st.SoftReached = l.ReceiptsPerMonthSoft > 0 && st.ThisMonth >= l.ReceiptsPerMonthSoft
		return nil
	})
	return st, err
}

// Release undoes RecordReceipt after a failed upload.
func (m *Meter) Release(ctx context.Context, householdID string, size int64) error {
	now := m.S.Now()
	return m.S.Tx(ctx, func(tx *control.Store) error {
		if _, err := tx.AddCounter(ctx, householdID, monthKey(now), metricReceipts, -1); err != nil {
			return err
		}
		if _, err := tx.AddCounter(ctx, householdID, dayKey(now), metricReceipts, -1); err != nil {
			return err
		}
		_, err := tx.AddCounter(ctx, householdID, periodGauge, metricStorage, -size)
		return err
	})
}

// AddStorage adjusts the storage gauge (negative when images are deleted).
func (m *Meter) AddStorage(ctx context.Context, householdID string, delta int64) (int64, error) {
	return m.S.AddCounter(ctx, householdID, periodGauge, metricStorage, delta)
}

// SetStorage replaces the gauge with a measured value (periodic recount).
func (m *Meter) SetStorage(ctx context.Context, householdID string, bytes int64) error {
	return m.S.SetCounter(ctx, householdID, periodGauge, metricStorage, bytes)
}

// CheckQueue refuses when the household already has max_queued_jobs queued (the count lives in
// the household database and is passed in).
func (m *Meter) CheckQueue(ctx context.Context, householdID string, queued int64) error {
	l, err := m.LimitsFor(ctx, householdID)
	if err != nil {
		return err
	}
	if l.MaxQueuedJobs > 0 && queued >= l.MaxQueuedJobs {
		return &control.LimitError{Limit: "max_queued_jobs", Value: l.MaxQueuedJobs, Current: queued}
	}
	return nil
}

// StartExport counts one export for today, or refuses with exports_per_day.
func (m *Meter) StartExport(ctx context.Context, householdID string) error {
	l, err := m.LimitsFor(ctx, householdID)
	if err != nil {
		return err
	}
	now := m.S.Now()
	return m.S.Tx(ctx, func(tx *control.Store) error {
		n, err := tx.Counter(ctx, householdID, dayKey(now), metricExports)
		if err != nil {
			return err
		}
		if l.ExportsPerDay > 0 && n >= l.ExportsPerDay {
			return &control.LimitError{Limit: "exports_per_day", Value: l.ExportsPerDay, Current: n, ResetsAt: DayResetsAt(now)}
		}
		_, err = tx.AddCounter(ctx, householdID, dayKey(now), metricExports, 1)
		return err
	})
}

// Usage is the usage object of GET /v1/account.
type Usage struct {
	Period            string    `json:"period"`
	ResetsAt          time.Time `json:"resets_at"`
	ReceiptsThisMonth int64     `json:"receipts_this_month"`
	ReceiptsToday     int64     `json:"receipts_today"`
	StorageBytes      int64     `json:"storage_bytes"`
	QueuedJobs        int64     `json:"queued_jobs"`
	ExportsToday      int64     `json:"exports_today"`
	OpenInvites       int64     `json:"open_invites"`
	Members           int64     `json:"members"`
}

// UsageFor reads the counters. queued comes from the household database.
func (m *Meter) UsageFor(ctx context.Context, householdID string, queued int64) (Usage, error) {
	now := m.S.Now()
	u := Usage{Period: monthKey(now), ResetsAt: MonthResetsAt(now), QueuedJobs: queued}
	var err error
	if u.ReceiptsThisMonth, err = m.S.Counter(ctx, householdID, monthKey(now), metricReceipts); err != nil {
		return u, err
	}
	if u.ReceiptsToday, err = m.S.Counter(ctx, householdID, dayKey(now), metricReceipts); err != nil {
		return u, err
	}
	if u.StorageBytes, err = m.S.Counter(ctx, householdID, periodGauge, metricStorage); err != nil {
		return u, err
	}
	if u.ExportsToday, err = m.S.Counter(ctx, householdID, dayKey(now), metricExports); err != nil {
		return u, err
	}
	n, err := m.S.CountMembers(ctx, householdID)
	if err != nil {
		return u, err
	}
	u.Members = int64(n)
	open, err := m.S.OpenInvites(ctx, householdID, now)
	u.OpenInvites = int64(len(open))
	return u, err
}

// ---- concurrent requests ----

// ErrBusy is returned when a household (or the whole server) has too many requests in flight.
// It maps to 429 rate_limited with retry_after_seconds 1.
var ErrBusy = &control.Error{Code: "rate_limited", Status: http.StatusTooManyRequests, Msg: "too many requests in flight", RetryAfterSeconds: 1}

// Gate limits requests in flight per household and overall. It lives in memory.
type Gate struct {
	mu       sync.Mutex
	perHH    map[string]int64
	total    int64
	TotalMax int64 // 0 = unlimited
}

// NewGate returns a gate with a global cap (0 = none).
func NewGate(totalMax int64) *Gate { return &Gate{perHH: map[string]int64{}, TotalMax: totalMax} }

// Acquire admits a request or returns ErrBusy. Call the returned function when it ends.
func (g *Gate) Acquire(householdID string, perHouseholdMax int64) (release func(), err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if (perHouseholdMax > 0 && g.perHH[householdID] >= perHouseholdMax) || (g.TotalMax > 0 && g.total >= g.TotalMax) {
		return nil, ErrBusy
	}
	g.perHH[householdID]++
	g.total++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.perHH[householdID]--
			if g.perHH[householdID] <= 0 {
				delete(g.perHH, householdID)
			}
			g.total--
		})
	}, nil
}

// InFlight reports requests in flight for a household.
func (g *Gate) InFlight(householdID string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.perHH[householdID]
}
