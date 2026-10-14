package hclock

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Offset is the system clock plus a forward offset. It exists only for the fake-Apple development
// mode (SHOP_APPLE_MODE=fake, which the configuration refuses together with the production
// environment) so an end-to-end test can move the server through a trial, a lapse and a deletion
// in minutes. The offset can only grow, is capped, and is kept in a file so a restart of the
// container does not send the clock back. Production never builds one.
type Offset struct {
	mu   sync.Mutex
	off  time.Duration
	file string
}

// MaxOffset is the largest total offset an Offset accepts (10 years).
const MaxOffset = 10 * 365 * 24 * time.Hour

// NewOffset loads the offset from file (a missing file means zero).
func NewOffset(file string) (*Offset, error) {
	o := &Offset{file: file}
	b, err := os.ReadFile(file)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, err
	default:
		n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil || n < 0 || time.Duration(n) > MaxOffset {
			return nil, fmt.Errorf("hclock: %s does not hold a valid offset", file)
		}
		o.off = time.Duration(n)
	}
	return o, nil
}

func (o *Offset) Now() time.Time {
	o.mu.Lock()
	defer o.mu.Unlock()
	return time.Now().UTC().Add(o.off)
}

// Offset is the current offset.
func (o *Offset) Offset() time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.off
}

// Advance moves the clock forward by d (d > 0) and saves the offset.
func (o *Offset) Advance(d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("hclock: the clock only moves forward")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.off+d > MaxOffset {
		return fmt.Errorf("hclock: offset would exceed %s", MaxOffset)
	}
	if o.file != "" {
		if err := os.WriteFile(o.file, []byte(strconv.FormatInt(int64(o.off+d), 10)), 0o600); err != nil {
			return err
		}
	}
	o.off += d
	return nil
}
