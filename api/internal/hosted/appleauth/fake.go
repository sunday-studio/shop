package appleauth

import (
	"context"
	"sync"
)

// FakeApple stands in for Apple's token and revoke endpoints in tests and local stacks.
// Exchange accepts any code registered with AddCode; Revoke records tokens and can be made to fail.
type FakeApple struct {
	mu         sync.Mutex
	codes      map[string]string
	Revoked    []string
	FailRevoke bool
	Down       bool
	Exchanges  int
}

// NewFakeApple returns an empty fake.
func NewFakeApple() *FakeApple { return &FakeApple{codes: map[string]string{}} }

// AddCode makes an authorization code valid and tells which refresh token it yields.
func (f *FakeApple) AddCode(code, refreshToken string) {
	f.mu.Lock()
	f.codes[code] = refreshToken
	f.mu.Unlock()
}

func (f *FakeApple) Exchange(_ context.Context, code string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Exchanges++
	if f.Down {
		return "", ErrAppleUnavailable
	}
	rt, ok := f.codes[code]
	if !ok {
		return "", ErrCodeInvalid
	}
	return rt, nil
}

func (f *FakeApple) Revoke(_ context.Context, refreshToken string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailRevoke || f.Down {
		return ErrAppleUnavailable
	}
	f.Revoked = append(f.Revoked, refreshToken)
	return nil
}

// RevokedTokens returns a copy of the revoked tokens.
func (f *FakeApple) RevokedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Revoked...)
}
