//go:build e2ehosted

// Package e2ehosted runs the WHOLE hosted paid product against a real, isolated docker stack
// (shop-hosted with the fake Apple, plus the real OCR), over HTTP, the way the iOS app talks to it.
// Start it with `make e2e-hosted`. See docs/BACKEND-STATUS.md.
//
// Isolation: compose project "shop-e2e-hosted" (own network, volume and containers), host ports
// 18500 (public API) and 18501 (admin API through a sidecar), its own API image tag, a throwaway
// data volume. The live stack (project "shop", port 8080, volume shop_shop-data) is never touched,
// and every compose call goes through compose(), which refuses any project but shop-e2e-hosted.
package e2ehosted

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	project   = "shop-e2e-hosted"
	apiPort   = "18500"
	adminPort = "18501"
	baseURL   = "http://127.0.0.1:" + apiPort
	adminURL  = "http://127.0.0.1:" + adminPort
	adminTok  = "sa_e2e_hosted_throwaway"
)

// liveBefore is the read-only fingerprint of the live stack (containers shop-*-1 and their start
// times, volume shop_shop-data) taken before anything starts; compared again after teardown.
var liveBefore string

func liveFingerprint() string {
	out, _ := exec.Command("docker", "inspect", "-f", "{{.Name}} {{.State.StartedAt}} {{.State.Status}}",
		"shop-api-1", "shop-ocr-1", "shop-backup-1").CombinedOutput()
	vol, _ := exec.Command("docker", "volume", "ls", "-q", "--filter", "name=^shop_shop-data$").Output()
	return strings.TrimSpace(string(out)) + " | volume: " + strings.TrimSpace(string(vol))
}

var (
	repoRoot string
	tornDown bool
	tdMu     sync.Mutex
)

func adminHash() string {
	h := sha256.Sum256([]byte(adminTok))
	return hex.EncodeToString(h[:])
}

// compose runs docker compose for the e2e-hosted project only.
func compose(ctx context.Context, stdin string, args ...string) (string, error) {
	// The only place in this package that starts docker compose. It refuses any project
	// but shop-e2e-hosted, so the live stack ("shop") can never be addressed from here.
	if project != "shop-e2e-hosted" {
		panic("refusing to run compose: project is " + project)
	}
	full := append([]string{"compose", "-p", project,
		"-f", filepath.Join(repoRoot, "deploy/docker-compose.e2e-hosted.yml")}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+project, "E2E_ADMIN_HASH="+adminHash())
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

func leftovers() (containers, volumes int) {
	out, _ := exec.Command("docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+project, "-q").Output()
	vols, _ := exec.Command("docker", "volume", "ls", "--filter", "label=com.docker.compose.project="+project, "-q").Output()
	return len(strings.Fields(string(out))), len(strings.Fields(string(vols)))
}

func teardown() {
	tdMu.Lock()
	defer tdMu.Unlock()
	if tornDown {
		return
	}
	tornDown = true
	if os.Getenv("E2E_KEEP") == "1" {
		fmt.Printf("\nE2E_KEEP=1: leaving compose project %q running\n", project)
		return
	}
	fmt.Printf("\nteardown: docker compose project %q (must be shop-e2e-hosted): down -v --remove-orphans\n", project)
	if project != "shop-e2e-hosted" {
		fmt.Println("teardown refused: wrong project name")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := compose(ctx, "", "down", "-v", "--remove-orphans")
	if err != nil {
		fmt.Printf("teardown FAILED: %v\n%s\n", err, out)
		results.add(result{name: "teardown (down -v)", status: "FAIL", note: err.Error()})
		return
	}
	if c, v := leftovers(); c > 0 || v > 0 {
		results.add(result{name: "teardown (down -v)", status: "FAIL", note: fmt.Sprintf("%d containers, %d volumes left behind", c, v)})
		return
	}
	results.add(result{name: "teardown (down -v)", status: "PASS", note: "no shop-e2e-hosted containers or volumes left"})
}

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) (code int) {
	wd, _ := os.Getwd()
	repoRoot = filepath.Clean(filepath.Join(wd, "../.."))
	if _, err := os.Stat(filepath.Join(repoRoot, "deploy/docker-compose.e2e-hosted.yml")); err != nil {
		fmt.Println("cannot find deploy/docker-compose.e2e-hosted.yml; run `make e2e-hosted` from the repo root")
		return 2
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\ninterrupted")
		teardown()
		results.print()
		os.Exit(130)
	}()
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("panic:", r)
			code = 1
		}
		teardown()
		if os.Getenv("E2E_KEEP") != "1" {
			if after := liveFingerprint(); after == liveBefore {
				results.add(result{name: "live stack untouched", status: "PASS", note: "same containers, same StartedAt, volume present"})
			} else {
				results.add(result{name: "live stack untouched", status: "FAIL", note: "before: " + liveBefore + " after: " + after})
			}
		}
		results.print()
		if results.anyFailed() {
			code = 1
		}
	}()

	liveBefore = liveFingerprint()
	fmt.Println("live stack (read only, never touched):", strings.ReplaceAll(liveBefore, "\n", "; "))
	if err := preflight(); err != nil {
		fmt.Println("preflight:", err)
		results.add(result{name: "stack up", status: "FAIL", note: err.Error()})
		return 1
	}
	// A previous crashed run may have left shop-e2e-hosted behind: clear it (same guarded project only).
	if os.Getenv("E2E_REUSE") != "1" {
		teardownStale()
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	fmt.Println("starting isolated stack (project " + project + ", ports " + apiPort + "/" + adminPort + "): build + up --wait ...")
	if out, err := compose(ctx, "", "up", "-d", "--build", "--wait", "--wait-timeout", "900"); err != nil {
		fmt.Printf("stack failed to start: %v\n%s\n", err, out)
		logs, _ := compose(ctx, "", "logs", "--tail", "40")
		fmt.Println(logs)
		results.add(result{name: "stack up", status: "FAIL", note: "compose up failed"})
		return 1
	}
	results.add(result{name: "stack up (hosted api + ocr + sidecar)", status: "PASS", dur: time.Since(start), note: "project " + project + ", ports " + apiPort + "/" + adminPort})
	return m.Run()
}

func preflight() error {
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		return fmt.Errorf("docker is not available: %s", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("docker", "image", "inspect", "shop-ocr:ppocrv5", "--format", "{{.Id}}").CombinedOutput(); err != nil {
		return fmt.Errorf("the OCR image shop-ocr:ppocrv5 is missing (run `make e2e` once, or: docker build -t shop-ocr:ppocrv5 --build-arg OCR_ENGINE=ppocrv5 ocr): %s", strings.TrimSpace(string(out)))
	}
	if c, _ := leftovers(); c == 0 {
		for _, p := range []string{apiPort, adminPort} {
			l, err := net.Listen("tcp", "127.0.0.1:"+p)
			if err != nil {
				return fmt.Errorf("port %s is in use by something else: %v", p, err)
			}
			l.Close()
		}
	}
	return nil
}

func teardownStale() {
	if c, v := leftovers(); c == 0 && v == 0 {
		return
	}
	fmt.Printf("found leftovers of project %q from an earlier run: removing (down -v)\n", project)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	compose(ctx, "", "down", "-v", "--remove-orphans")
}

// ---- results table -------------------------------------------------------------

type result struct {
	name, status, note string
	dur                time.Duration
}

type resultSet struct {
	mu   sync.Mutex
	rows []result
}

var results = &resultSet{}

func (r *resultSet) add(x result) { r.mu.Lock(); r.rows = append(r.rows, x); r.mu.Unlock() }

func (r *resultSet) anyFailed() bool {
	for _, x := range r.rows {
		if x.status == "FAIL" {
			return true
		}
	}
	return false
}

func (r *resultSet) print() {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := 4
	for _, x := range r.rows {
		if len(x.name) > w {
			w = len(x.name)
		}
	}
	line := strings.Repeat("-", w+60)
	fmt.Println("\n" + line)
	fmt.Printf("%-*s  %-6s  %6s  %s\n", w, "STEP", "RESULT", "TIME", "NOTE")
	fmt.Println(line)
	pass, fail, skip := 0, 0, 0
	for _, x := range r.rows {
		switch x.status {
		case "PASS":
			pass++
		case "FAIL":
			fail++
		default:
			skip++
		}
		d := ""
		if x.dur > 0 {
			d = fmt.Sprintf("%.1fs", x.dur.Seconds())
		}
		fmt.Printf("%-*s  %-6s  %6s  %s\n", w, x.name, x.status, d, x.note)
	}
	fmt.Println(line)
	fmt.Printf("%d passed, %d failed, %d skipped\n", pass, fail, skip)
	if fail > 0 {
		fmt.Println("RESULT: FAIL")
	} else {
		fmt.Println("RESULT: PASS")
	}
}

// noted holds the last note a step recorded (failure detail or skip reason or facts).
var (
	notedMu sync.Mutex
	noted   = map[string]string{}
)

func note(t *testing.T, format string, a ...any) {
	notedMu.Lock()
	noted[t.Name()] = fmt.Sprintf(format, a...)
	notedMu.Unlock()
}

// step runs fn as a named subtest and records it in the results table.
func step(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		start := time.Now()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
				note(t, "panic: %v", r)
			}
			notedMu.Lock()
			n := noted[t.Name()]
			notedMu.Unlock()
			st := "PASS"
			if t.Failed() {
				st = "FAIL"
			} else if t.Skipped() {
				st = "SKIP"
			}
			results.add(result{name: name, status: st, dur: time.Since(start), note: n})
		}()
		fn(t)
	})
}

// check fails the step (and stops it) when cond is false.
func check(t *testing.T, cond bool, format string, a ...any) {
	t.Helper()
	if !cond {
		msg := fmt.Sprintf(format, a...)
		note(t, "%s", msg)
		t.Fatal(msg)
	}
}

func skip(t *testing.T, format string, a ...any) {
	t.Helper()
	note(t, format, a...)
	t.Skipf(format, a...)
}
