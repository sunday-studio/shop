//go:build e2e

// Package e2e runs the whole backend loop against a real, isolated docker stack
// (API + OCR + backup), over HTTP, the way the iOS app talks to it. Start it with
// `make e2e`. See docs/BACKEND-STATUS.md.
//
// Isolation: compose project "shop-e2e" (own network, volumes and containers), host
// port 18480, its own API image tag, a throwaway data volume. The live stack (project
// "shop", port 8080, volume shop_shop-data) is never touched, and every destructive
// compose call goes through compose(), which refuses any project but shop-e2e.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
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
	project  = "shop-e2e"
	apiPort  = "18480"
	baseURL  = "http://127.0.0.1:" + apiPort
	pairPass = "e2e-correct-horse-battery"
)

var (
	repoRoot string
	tornDown bool
	tdMu     sync.Mutex
	fakeOFF  *offServer
)

// compose runs docker compose for the e2e project only.
func compose(ctx context.Context, stdin string, args ...string) (string, error) {
	// The only place in this package that starts docker compose. It refuses any project
	// but shop-e2e, so the live stack ("shop") can never be addressed from here.
	if project != "shop-e2e" {
		panic("refusing to run compose: project is " + project)
	}
	full := append([]string{"compose", "-p", project,
		"-f", filepath.Join(repoRoot, "deploy/docker-compose.yml"),
		"-f", filepath.Join(repoRoot, "deploy/docker-compose.e2e.yml")}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+project, "SHOP_VERSION=e2e", "SHOP_COMMIT=e2e")
	if fakeOFF != nil {
		cmd.Env = append(cmd.Env, "E2E_OFF_URL="+fakeOFF.containerURL)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

func teardown() {
	tdMu.Lock()
	defer tdMu.Unlock()
	if tornDown {
		return
	}
	tornDown = true
	fmt.Printf("\nteardown: docker compose project %q (must be shop-e2e): down -v --remove-orphans\n", project)
	if project != "shop-e2e" {
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
	left, _ := exec.Command("docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+project, "-q").Output()
	vols, _ := exec.Command("docker", "volume", "ls", "--filter", "label=com.docker.compose.project="+project, "-q").Output()
	if len(bytes.TrimSpace(left)) > 0 || len(bytes.TrimSpace(vols)) > 0 {
		results.add(result{name: "teardown (down -v)", status: "FAIL", note: "containers or volumes left behind"})
		return
	}
	results.add(result{name: "teardown (down -v)", status: "PASS", note: "no shop-e2e containers or volumes left"})
}

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) (code int) {
	wd, _ := os.Getwd()
	repoRoot = filepath.Clean(filepath.Join(wd, "../.."))
	if _, err := os.Stat(filepath.Join(repoRoot, "deploy/docker-compose.e2e.yml")); err != nil {
		fmt.Println("cannot find deploy/docker-compose.e2e.yml; run `make e2e` from the repo root")
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
		results.print()
		if results.anyFailed() {
			code = 1
		}
	}()

	if err := preflight(); err != nil {
		fmt.Println("preflight:", err)
		results.add(result{name: "stack up", status: "FAIL", note: err.Error()})
		return 1
	}
	fakeOFF = startFakeOFF()
	defer fakeOFF.close()

	// A previous crashed run may have left shop-e2e behind: clear it (same guarded project only).
	teardownStale()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	fmt.Println("starting isolated stack (project shop-e2e, port " + apiPort + "): build + up --wait ...")
	if out, err := compose(ctx, "", "up", "-d", "--build", "--wait", "--wait-timeout", "900"); err != nil {
		fmt.Printf("stack failed to start: %v\n%s\n", err, out)
		logs, _ := compose(ctx, "", "logs", "--tail", "40")
		fmt.Println(logs)
		results.add(result{name: "stack up", status: "FAIL", note: "compose up failed"})
		return 1
	}
	results.add(result{name: "stack up (api + ocr + backup)", status: "PASS", dur: time.Since(start), note: "project shop-e2e, port " + apiPort})
	return m.Run()
}

func preflight() error {
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		return fmt.Errorf("docker is not available: %s", strings.TrimSpace(string(out)))
	}
	if out, _ := exec.Command("docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+project, "-q").Output(); len(bytes.TrimSpace(out)) == 0 {
		// no stale stack: the port must be free
		l, err := net.Listen("tcp", "127.0.0.1:"+apiPort)
		if err != nil {
			return fmt.Errorf("port %s is in use by something else: %v", apiPort, err)
		}
		l.Close()
	}
	return nil
}

func teardownStale() {
	out, _ := exec.Command("docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+project, "-q").Output()
	vols, _ := exec.Command("docker", "volume", "ls", "--filter", "label=com.docker.compose.project="+project, "-q").Output()
	if len(bytes.TrimSpace(out)) == 0 && len(bytes.TrimSpace(vols)) == 0 {
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

var httpc = &http.Client{Timeout: 60 * time.Second}
