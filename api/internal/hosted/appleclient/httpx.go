package appleclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const maxBody = 4 << 20 // responses are small; never read more than 4 MiB

// Options are shared by all clients. The zero value is usable.
type Options struct {
	HTTP        *http.Client                                     // default: 15 s timeout, no redirects
	MaxAttempts int                                              // total tries on network errors, 5xx and 429 (default 3)
	BaseBackoff time.Duration                                    // default 300 ms, doubling, jittered, capped at 5 s
	Sleep       func(ctx context.Context, d time.Duration) error // default: real sleep (tests inject)
	Log         *slog.Logger
}

func (o Options) withDefaults() Options {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	// A client that follows redirects would forward the Authorization header; never do that.
	hc := *o.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.HTTP = &hc
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	if o.BaseBackoff <= 0 {
		o.BaseBackoff = 300 * time.Millisecond
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return o
}

type response struct {
	Status int
	Header http.Header
	Body   []byte
}

// errTransport marks a failure to get any HTTP answer (after all retries).
var errTransport = errors.New("apple request failed")

// send performs one logical request. mkReq builds a fresh request per attempt (fresh JWT,
// fresh body). Retries happen on network errors, 429 and 5xx when retry is true; the last
// response is returned even if it is a 5xx so callers can map it.
func send(ctx context.Context, o Options, retry bool, mkReq func() (*http.Request, error)) (*response, error) {
	attempts := o.MaxAttempts
	if !retry {
		attempts = 1
	}
	var last *response
	var lastErr error
	for i := 0; i < attempts; i++ {
		req, err := mkReq()
		if err != nil {
			return nil, err
		}
		req = req.WithContext(ctx)
		resp, err := o.HTTP.Do(req)
		var host string
		if req.URL != nil {
			host = req.URL.Host
		}
		if err != nil {
			// url.Error text can include the URL; keep only the cause kind.
			var ue *url.Error
			cause := "network error"
			if errors.As(err, &ue) && ue.Timeout() {
				cause = "timeout"
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("%w: %s", errTransport, cause)
			o.Log.Warn("apple request failed", "host", host, "attempt", i+1, "cause", cause)
		} else {
			body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
			resp.Body.Close()
			if rerr != nil {
				lastErr = fmt.Errorf("%w: reading response", errTransport)
			} else {
				last = &response{Status: resp.StatusCode, Header: resp.Header, Body: body}
				lastErr = nil
				o.Log.Debug("apple request", "host", host, "status", resp.StatusCode, "attempt", i+1)
				if resp.StatusCode != 429 && resp.StatusCode < 500 {
					return last, nil
				}
			}
		}
		if i+1 < attempts {
			if err := o.Sleep(ctx, backoff(o.BaseBackoff, i, last)); err != nil {
				return nil, err
			}
		}
	}
	if lastErr != nil && last == nil {
		return nil, lastErr
	}
	return last, nil
}

func backoff(base time.Duration, attempt int, last *response) time.Duration {
	d := base << attempt
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	d = d/2 + time.Duration(rand.Int64N(int64(d/2)+1)) // jitter: 50%..100%
	if last != nil && last.Status == 429 {
		if s, err := strconv.Atoi(last.Header.Get("Retry-After")); err == nil && s > 0 {
			ra := time.Duration(s) * time.Second
			if ra > 10*time.Second {
				ra = 10 * time.Second
			}
			if ra > d {
				d = ra
			}
		}
	}
	return d
}

func jsonBody(b []byte) io.Reader { return bytes.NewReader(b) }
