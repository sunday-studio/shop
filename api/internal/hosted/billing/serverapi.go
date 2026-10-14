package billing

import (
	"context"
	"errors"
	"sync"
)

// ErrAppleUnavailable means the App Store Server API could not be reached (HTTP 502,
// retryable) or is not configured. Nothing in this repository calls Apple yet.
var ErrAppleUnavailable = errors.New("apple unavailable")

// ErrAppleNotFound means Apple does not know the transaction.
var ErrAppleNotFound = errors.New("apple: transaction not found")

// SubscriptionStatus is what "Get All Subscription Statuses" tells us about one subscription,
// already verified and decoded by the client implementation. Status uses Apple's numbers:
// 1 active, 2 expired, 3 billing retry, 4 grace period, 5 revoked.
type SubscriptionStatus struct {
	Status      int
	Transaction TransactionInfo
	Renewal     RenewalInfo
	Environment string
}

// ServerAPI is the guarded interface to the App Store Server API. The real client (ES256
// JWT with the in-app-purchase key, calls to api.storekit.itunes.apple.com) is a later step
// and must verify every signed value it returns with the same Verifier. The default
// implementation is Unavailable, so no code path can reach Apple by accident.
type ServerAPI interface {
	// GetSubscriptionStatus returns the authoritative state for an original transaction id.
	GetSubscriptionStatus(ctx context.Context, originalTransactionID string) (SubscriptionStatus, error)
	// GetTransactionHistory returns the verified transactions of an original transaction id, oldest first.
	GetTransactionHistory(ctx context.Context, originalTransactionID string) ([]TransactionInfo, error)
	// SetAppAccountToken sets or replaces the appAccountToken of an original transaction.
	SetAppAccountToken(ctx context.Context, originalTransactionID, appAccountToken string) error
}

// Unavailable is the default ServerAPI: every call fails with ErrAppleUnavailable.
type Unavailable struct{}

func (Unavailable) GetSubscriptionStatus(context.Context, string) (SubscriptionStatus, error) {
	return SubscriptionStatus{}, ErrAppleUnavailable
}
func (Unavailable) GetTransactionHistory(context.Context, string) ([]TransactionInfo, error) {
	return nil, ErrAppleUnavailable
}
func (Unavailable) SetAppAccountToken(context.Context, string, string) error {
	return ErrAppleUnavailable
}

// FakeAPI is an in-memory ServerAPI for tests and local stacks.
type FakeAPI struct {
	mu       sync.Mutex
	statuses map[string]SubscriptionStatus
	history  map[string][]TransactionInfo
	Tokens   map[string]string // original transaction id -> token set through SetAppAccountToken
	Calls    []string
	Down     bool // every call fails with ErrAppleUnavailable
}

// NewFakeAPI returns an empty fake.
func NewFakeAPI() *FakeAPI {
	return &FakeAPI{statuses: map[string]SubscriptionStatus{}, history: map[string][]TransactionInfo{}, Tokens: map[string]string{}}
}

// SetStatus defines what Apple "says" for an original transaction id.
func (f *FakeAPI) SetStatus(originalTxID string, st SubscriptionStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[originalTxID] = st
}

// SetHistory defines the transaction history.
func (f *FakeAPI) SetHistory(originalTxID string, h []TransactionInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history[originalTxID] = h
}

func (f *FakeAPI) record(call string) error {
	f.Calls = append(f.Calls, call)
	if f.Down {
		return ErrAppleUnavailable
	}
	return nil
}

func (f *FakeAPI) GetSubscriptionStatus(_ context.Context, id string) (SubscriptionStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("status:" + id); err != nil {
		return SubscriptionStatus{}, err
	}
	st, ok := f.statuses[id]
	if !ok {
		return SubscriptionStatus{}, ErrAppleNotFound
	}
	return st, nil
}

func (f *FakeAPI) GetTransactionHistory(_ context.Context, id string) ([]TransactionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("history:" + id); err != nil {
		return nil, err
	}
	return f.history[id], nil
}

func (f *FakeAPI) SetAppAccountToken(_ context.Context, id, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("set_token:" + id); err != nil {
		return err
	}
	f.Tokens[id] = token
	return nil
}

// CallCount returns how many calls started with the prefix.
func (f *FakeAPI) CallCount(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.Calls {
		if len(c) >= len(prefix) && c[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}
