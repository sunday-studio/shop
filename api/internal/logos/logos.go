// Package logos fetches shop logos from logo.dev by domain and caches them
// on disk, so the app only ever talks to the Shop server (docs/IOS-BACKEND-NEEDS.md #4).
package logos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"shop/internal/match"
)

var (
	ErrDisabled = errors.New("logos disabled: LOGO_DEV_TOKEN not set")
	ErrNoLogo   = errors.New("no logo for this domain")
	ErrDomain   = errors.New("invalid domain")
)

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}$`)

// NormaliseDomain accepts "ah.nl", "https://www.ah.nl/" or "AH.NL" and
// returns "ah.nl".
func NormaliseDomain(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", ErrDomain
		}
		s = u.Hostname()
	}
	s = strings.TrimPrefix(strings.TrimSuffix(s, "/"), "www.")
	if !domainRe.MatchString(s) {
		return "", fmt.Errorf("%w: %q", ErrDomain, s)
	}
	return s, nil
}

// knownChains maps (folded) shop names to their Dutch web domains so most
// shops get a logo without typing anything.
var knownChains = map[string]string{
	"albert heijn": "ah.nl", "ah": "ah.nl", "jumbo": "jumbo.com", "lidl": "lidl.nl", "aldi": "aldi.nl",
	"plus": "plus.nl", "dirk": "dirk.nl", "dirk van den broek": "dirk.nl", "coop": "coop.nl",
	"spar": "spar.nl", "vomar": "vomar.nl", "hoogvliet": "hoogvliet.com", "dekamarkt": "dekamarkt.nl",
	"poiesz": "poiesz-supermarkten.nl", "ekoplaza": "ekoplaza.nl", "marqt": "marqt.com",
	"picnic": "picnic.app", "crisp": "crisp.nl", "action": "action.com", "kruidvat": "kruidvat.nl",
	"etos": "etos.nl", "hema": "hema.nl", "gall & gall": "gall.nl", "gall": "gall.nl",
	"makro": "makro.nl", "sligro": "sligro.nl", "nettorama": "nettorama.nl", "boni": "boni.nl",
	"jan linders": "janlinders.nl", "mcd": "mcdsupermarkt.nl", "deen": "deen.nl", "ah to go": "ah.nl",
}

// GuessDomain returns the domain of a known chain from the shop's chain or
// name ("Albert Heijn Zuid" → ah.nl), or "".
func GuessDomain(name, chain string) string {
	for _, s := range []string{chain, name} {
		f := strings.TrimSpace(match.Fold(s))
		if d, ok := knownChains[f]; ok {
			return d
		}
		// Longest known chain the name starts with ("ah to go" over "ah").
		best, domain := "", ""
		for k, d := range knownChains {
			if strings.HasPrefix(f, k+" ") && len(k) > len(best) {
				best, domain = k, d
			}
		}
		if domain != "" {
			return domain
		}
	}
	return ""
}

// Cache fetches logos from logo.dev and keeps them on disk.
type Cache struct {
	token string
	dir   string
	base  string
	http  *http.Client

	mu     sync.Mutex
	misses map[string]time.Time // domain → when logo.dev had nothing
}

const (
	missTTL  = 7 * 24 * time.Hour
	maxBytes = 2 << 20
)

func New(token, dir string) *Cache {
	return &Cache{token: token, dir: dir, base: "https://img.logo.dev", http: &http.Client{Timeout: 10 * time.Second},
		misses: map[string]time.Time{}}
}

// WithBaseURL points the cache at another server (tests).
func (c *Cache) WithBaseURL(u string) *Cache { c.base = strings.TrimRight(u, "/"); return c }

// Enabled reports whether a token is configured.
func (c *Cache) Enabled() bool { return c != nil && c.token != "" }

func (c *Cache) path(domain string) string { return filepath.Join(c.dir, "logos", domain+".png") }

// Path returns the cached logo file for a domain, fetching it on first use.
func (c *Cache) Path(ctx context.Context, domain string) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	domain, err := NormaliseDomain(domain)
	if err != nil || domain == "" {
		return "", ErrDomain
	}
	p := c.path(domain)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	c.mu.Lock()
	if t, ok := c.misses[domain]; ok && time.Since(t) < missTTL {
		c.mu.Unlock()
		return "", ErrNoLogo
	}
	c.mu.Unlock()

	u := fmt.Sprintf("%s/%s?token=%s&size=128&format=png&fallback=404", c.base, url.PathEscape(domain), url.QueryEscape(c.token))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("logo.dev: %w", redact(err, c.token))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		c.mu.Lock()
		c.misses[domain] = time.Now()
		c.mu.Unlock()
		return "", ErrNoLogo
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("logo.dev: %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "image/") {
		return "", fmt.Errorf("logo.dev: unexpected content type %q", ct)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}

// redact keeps the token out of logs (url.Error includes the URL).
func redact(err error, token string) error {
	return errors.New(strings.ReplaceAll(err.Error(), url.QueryEscape(token), "REDACTED"))
}
