//go:build drill

// Package drill is the automatic restore drill (`make restore-drill`). In an isolated docker
// stack it creates data, takes a backup, wipes the stack's data volume, restores, and checks
// that the counts and the receipt images are identical. It does this twice:
//
//  1. restore from the backup folder, and
//  2. restore from a copy that came back from an off-site destination (rclone, through an
//     encrypted "crypt" remote that points at a throwaway folder), after losing both the
//     data volume and the backup folder.
//
// Isolation: compose project "shop-e2e" only (the same one `make e2e` uses; the two must not
// run at the same time), host port 18481, its own API image tag, throwaway volumes. The live
// stack (project "shop", port 8080, volume shop_shop-data) is never touched: every compose
// call goes through compose(), which refuses any other project, and every volume removal goes
// through removeVolume(), which refuses names outside shop-e2e_*.
package drill

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	project   = "shop-e2e"
	apiPort   = "18481"
	baseURL   = "http://127.0.0.1:" + apiPort
	pairPass  = "drill-correct-horse-battery"
	rcloneImg = "rclone/rclone:1.68.2"
)

var (
	repoRoot string
	cryptPw  string // obscured rclone password for the throwaway crypt remote
	tornDown bool
	tdMu     sync.Mutex
	token    string
	httpc    = &http.Client{Timeout: 60 * time.Second}
)

// ---- docker helpers (shop-e2e only) --------------------------------------------------

func compose(ctx context.Context, stdin string, args ...string) (string, error) {
	if project != "shop-e2e" {
		panic("refusing to run compose: project is " + project)
	}
	full := append([]string{"compose", "-p", project, "--profile", "offsite",
		"-f", filepath.Join(repoRoot, "deploy/docker-compose.yml"),
		"-f", filepath.Join(repoRoot, "deploy/docker-compose.e2e.yml"),
		"-f", filepath.Join(repoRoot, "deploy/docker-compose.drill.yml")}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "SHOP_VERSION=drill", "SHOP_COMMIT=drill", "DRILL_CRYPT_PASSWORD="+cryptPw)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

func removeVolume(name string) error {
	if !strings.HasPrefix(name, project+"_") {
		return fmt.Errorf("refusing to remove volume %q: not a %s volume", name, project)
	}
	fmt.Printf("removing volume %s (project %s)\n", name, project)
	out, err := exec.Command("docker", "volume", "rm", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func volumeExists(name string) bool {
	return exec.Command("docker", "volume", "inspect", name).Run() == nil
}

func projectCount(kind string, running bool) int {
	args := []string{"ps", "-a", "--filter", "label=com.docker.compose.project=" + project, "-q"}
	if kind == "volume" {
		args = []string{"volume", "ls", "--filter", "label=com.docker.compose.project=" + project, "-q"}
	} else if running {
		args = []string{"ps", "--filter", "label=com.docker.compose.project=" + project, "-q"}
	}
	out, _ := exec.Command("docker", args...).Output()
	return len(strings.Fields(string(out)))
}

func teardown() {
	tdMu.Lock()
	defer tdMu.Unlock()
	if tornDown {
		return
	}
	tornDown = true
	fmt.Printf("\nteardown: docker compose project %q (must be shop-e2e): down -v --remove-orphans\n", project)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if out, err := compose(ctx, "", "down", "-v", "--remove-orphans"); err != nil {
		results.add(result{name: "teardown (down -v)", status: "FAIL", note: err.Error() + ": " + trunc([]byte(out))})
		return
	}
	if projectCount("container", false) > 0 || projectCount("volume", false) > 0 {
		results.add(result{name: "teardown (down -v)", status: "FAIL", note: "containers or volumes left behind"})
		return
	}
	results.add(result{name: "teardown (down -v)", status: "PASS", note: "no shop-e2e containers or volumes left"})
}

// ---- main ------------------------------------------------------------------------------

func TestMain(m *testing.M) { os.Exit(run(m)) }

func run(m *testing.M) (code int) {
	wd, _ := os.Getwd()
	repoRoot = filepath.Clean(filepath.Join(wd, ".."))
	repoRoot = filepath.Clean(filepath.Join(repoRoot, ".."))
	if _, err := os.Stat(filepath.Join(repoRoot, "deploy/docker-compose.drill.yml")); err != nil {
		fmt.Println("cannot find deploy/docker-compose.drill.yml; run `make restore-drill` from the repo root")
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
		results.print()
		if results.anyFailed() {
			code = 1
		}
	}()

	if err := preflight(); err != nil {
		fmt.Println("preflight:", err)
		results.add(result{name: "preflight", status: "FAIL", note: err.Error()})
		return 1
	}
	// Leftovers of an earlier, stopped run of this same project are cleared; a running one
	// (for example `make e2e`) makes preflight refuse instead.
	if projectCount("container", false) > 0 || projectCount("volume", false) > 0 {
		fmt.Printf("found stopped leftovers of project %q: removing (down -v)\n", project)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		_, _ = compose(ctx, "", "down", "-v", "--remove-orphans")
		cancel()
	}
	defer teardown()
	return m.Run()
}

func preflight() error {
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		return fmt.Errorf("docker is not available: %s", strings.TrimSpace(string(out)))
	}
	if projectCount("container", true) > 0 {
		return fmt.Errorf("project %s has running containers (is `make e2e` running?); the drill uses the same project, so wait for it or stop it first", project)
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+apiPort)
	if err != nil {
		return fmt.Errorf("port %s is in use by something else: %v", apiPort, err)
	}
	l.Close()
	if exec.Command("docker", "image", "inspect", rcloneImg).Run() != nil {
		fmt.Println("pulling", rcloneImg, "(first run only) ...")
		if out, err := exec.Command("docker", "pull", rcloneImg).CombinedOutput(); err != nil {
			return fmt.Errorf("cannot get %s (needed for the off-site leg): %s", rcloneImg, strings.TrimSpace(string(out)))
		}
	}
	// A random password for the throwaway crypt remote, in rclone's obscured form.
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	out, err := exec.Command("docker", "run", "--rm", rcloneImg, "obscure", hex.EncodeToString(b)).Output()
	if err != nil {
		return fmt.Errorf("rclone obscure: %v", err)
	}
	cryptPw = strings.TrimSpace(string(out))
	return nil
}

// ---- results table -----------------------------------------------------------------------

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
	r.mu.Lock()
	defer r.mu.Unlock()
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
	pass, fail := 0, 0
	for _, x := range r.rows {
		if x.status == "FAIL" {
			fail++
		} else {
			pass++
		}
		d := ""
		if x.dur > 0 {
			d = fmt.Sprintf("%.1fs", x.dur.Seconds())
		}
		fmt.Printf("%-*s  %-6s  %6s  %s\n", w, x.name, x.status, d, x.note)
	}
	fmt.Println(line)
	fmt.Printf("%d passed, %d failed\n", pass, fail)
	if fail > 0 {
		fmt.Println("RESTORE DRILL: FAIL")
	} else {
		fmt.Println("RESTORE DRILL: PASS")
	}
}

var (
	notedMu sync.Mutex
	noted   = map[string]string{}
)

func note(t *testing.T, format string, a ...any) {
	notedMu.Lock()
	noted[t.Name()] = fmt.Sprintf(format, a...)
	notedMu.Unlock()
}

func check(t *testing.T, cond bool, format string, a ...any) {
	t.Helper()
	if !cond {
		msg := fmt.Sprintf(format, a...)
		note(t, "%s", msg)
		t.Fatal(msg)
	}
}

// step runs fn as a subtest; a failed step stops the drill (later steps depend on it).
func step(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	ok := t.Run(name, func(t *testing.T) {
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
			}
			results.add(result{name: name, status: st, dur: time.Since(start), note: n})
		}()
		fn(t)
	})
	if !ok {
		t.FailNow()
	}
}

func trunc(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}

// ---- API helpers ---------------------------------------------------------------------------

type M = map[string]any

func raw(method, path string, body any, authed bool) (int, []byte, error) {
	var rd io.Reader
	ct := ""
	if body != nil {
		j, _ := json.Marshal(body)
		rd, ct = bytes.NewReader(j), "application/json"
	}
	req, err := http.NewRequest(method, baseURL+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if authed && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

func call(t *testing.T, method, path string, body any, want int) []byte {
	t.Helper()
	code, b, err := raw(method, path, body, true)
	check(t, err == nil, "%s %s: %v", method, path, err)
	check(t, code == want, "%s %s: status %d, want %d: %s", method, path, code, want, trunc(b))
	return b
}

func obj(t *testing.T, method, path string, body any, want int) M {
	t.Helper()
	var m M
	check(t, json.Unmarshal(call(t, method, path, body, want), &m) == nil, "%s %s: not a JSON object", method, path)
	return m
}

func jpegBytes(seed int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 360, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 360; x++ {
			img.Set(x, y, color.RGBA{uint8(x*seed + y), uint8(y * (seed + 1)), uint8(x + y*seed), 255})
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	return buf.Bytes()
}

func upload(t *testing.T, data []byte) M {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="image"; filename="drill.jpg"`)
	h.Set("Content-Type", "image/jpeg")
	p, _ := w.CreatePart(h)
	p.Write(data)
	w.Close()
	req, _ := http.NewRequest("POST", baseURL+"/v1/receipts", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpc.Do(req)
	check(t, err == nil, "upload: %v", err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	check(t, resp.StatusCode == 201 || resp.StatusCode == 202, "upload: status %d: %s", resp.StatusCode, trunc(b))
	var m M
	check(t, json.Unmarshal(b, &m) == nil, "upload: not JSON: %s", trunc(b))
	return m
}

func waitHealthy(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		code, _, err := raw("GET", "/version", nil, false)
		if err == nil && code == 200 {
			return
		}
		check(t, time.Now().Before(deadline), "API did not come up on port %s", apiPort)
		time.Sleep(time.Second)
	}
}

// fingerprint captures what the drill compares before and after a restore.
type fingerprint struct {
	Counts     string   `json:"counts"`
	Receipts   []string `json:"receipts"`   // "id:sha256-of-image"
	Products   []string `json:"products"`   // name|category
	Categories []string `json:"categories"` // names
	List       []string `json:"list"`       // item texts
}

func capture(t *testing.T) fingerprint {
	t.Helper()
	var fp fingerprint
	var counts M
	check(t, json.Unmarshal(call(t, "GET", "/v1/receipts/counts", nil, 200), &counts) == nil, "counts")
	cb, _ := json.Marshal(counts) // map keys marshal sorted
	fp.Counts = string(cb)

	var page struct {
		Items []M `json:"items"`
	}
	check(t, json.Unmarshal(call(t, "GET", "/v1/receipts?limit=100", nil, 200), &page) == nil, "receipts page")
	for _, r := range page.Items {
		id := int64(r["id"].(float64))
		img := call(t, "GET", fmt.Sprintf("/v1/receipts/%d/image", id), nil, 200)
		sum := sha256.Sum256(img)
		fp.Receipts = append(fp.Receipts, fmt.Sprintf("%d:%s", id, hex.EncodeToString(sum[:8])))
	}
	var prods, cats, items []M
	check(t, json.Unmarshal(call(t, "GET", "/v1/products?limit=200", nil, 200), &prods) == nil, "products")
	check(t, json.Unmarshal(call(t, "GET", "/v1/categories", nil, 200), &cats) == nil, "categories")
	check(t, json.Unmarshal(call(t, "GET", "/v1/list", nil, 200), &items) == nil, "list")
	for _, p := range prods {
		fp.Products = append(fp.Products, fmt.Sprintf("%v|%v", p["name"], p["category_id"]))
	}
	for _, c := range cats {
		fp.Categories = append(fp.Categories, fmt.Sprint(c["name"]))
	}
	for _, i := range items {
		fp.List = append(fp.List, fmt.Sprint(i["text"]))
	}
	sort.Strings(fp.Receipts)
	sort.Strings(fp.Products)
	sort.Strings(fp.Categories)
	sort.Strings(fp.List)
	return fp
}

func (f fingerprint) String() string {
	return fmt.Sprintf("%d receipts, %d products, %d categories, %d list items", len(f.Receipts), len(f.Products), len(f.Categories), len(f.List))
}

func sameFingerprint(a, b fingerprint) string {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	if bytes.Equal(x, y) {
		return ""
	}
	return fmt.Sprintf("before: %s\nafter:  %s", x, y)
}

// ---- the drill ---------------------------------------------------------------------------------

func ctxBG() context.Context { return context.Background() }

func TestRestoreDrill(t *testing.T) {
	var before fingerprint

	step(t, "start isolated stack (api only)", func(t *testing.T) {
		out, err := compose(ctxBG(), "", "up", "-d", "--build", "--no-deps", "--wait", "--wait-timeout", "600", "api")
		check(t, err == nil, "compose up: %v: %s", err, trunc([]byte(out)))
		waitHealthy(t)
		note(t, "project %s, port %s, volumes shop-e2e_*", project, apiPort)
	})

	step(t, "set password + pair", func(t *testing.T) {
		out, err := compose(ctxBG(), pairPass+"\n", "exec", "-T", "api", "/usr/local/bin/shop", "password", "set")
		check(t, err == nil, "password set: %v: %s", err, trunc([]byte(out)))
		m := obj(t, "POST", "/v1/auth/pair", M{"password": pairPass, "device_name": "drill"}, 201)
		token, _ = m["token"].(string)
		check(t, token != "", "no device token in pair response")
	})

	step(t, "create data (receipts with images, products, categories, list)", func(t *testing.T) {
		for i := 1; i <= 3; i++ {
			upload(t, jpegBytes(i))
		}
		cat := obj(t, "POST", "/v1/categories", M{"name": "Drill Dairy"}, 201)
		obj(t, "POST", "/v1/categories", M{"name": "Drill Fruit"}, 201)
		obj(t, "POST", "/v1/products", M{"name": "Drill Milk", "category_id": cat["id"]}, 201)
		obj(t, "POST", "/v1/products", M{"name": "Drill Pear"}, 201)
		obj(t, "POST", "/v1/list", M{"client_id": "drill-1", "text": "Drill melk"}, 201)
		obj(t, "POST", "/v1/list", M{"client_id": "drill-2", "text": "Drill peer", "qty": 2}, 201)
		before = capture(t)
		check(t, len(before.Receipts) == 3 && len(before.Products) >= 2 && len(before.List) == 2, "unexpected data: %s", before)
		note(t, "%s", before)
	})

	backupRun := func(t *testing.T) {
		out, err := compose(ctxBG(), "", "run", "--rm", "--no-deps", "backup", "backup")
		check(t, err == nil, "backup: %v: %s", err, trunc([]byte(out)))
	}
	wipeData := func(t *testing.T) {
		fmt.Printf("wiping the data volume of project %s: stop + rm api, then docker volume rm %s_shop-data\n", project, project)
		_, _ = compose(ctxBG(), "", "rm", "-sf", "api")
		check(t, removeVolume(project+"_shop-data") == nil, "could not remove the data volume")
		check(t, !volumeExists(project+"_shop-data"), "data volume still exists")
	}
	restore := func(t *testing.T) {
		out, err := compose(ctxBG(), "", "run", "--rm", "--no-deps", "backup", "restore", "-yes", "-snapshot", "latest")
		check(t, err == nil, "restore: %v: %s", err, trunc([]byte(out)))
		out, err = compose(ctxBG(), "", "up", "-d", "--no-deps", "--wait", "--wait-timeout", "300", "api")
		check(t, err == nil, "api up after restore: %v: %s", err, trunc([]byte(out)))
		waitHealthy(t)
	}
	verify := func(t *testing.T) {
		// The device token lives in the database, so the same token must still work.
		after := capture(t)
		if diff := sameFingerprint(before, after); diff != "" {
			check(t, false, "data differs after restore\n%s", diff)
		}
		note(t, "identical: %s; device token still valid; image hashes equal", after)
	}

	step(t, "backup (snapshot + images)", func(t *testing.T) {
		// A fresh named volume is owned by root; the backup user needs to write to it
		// (a host folder behaves the way you set it up).
		out, err := compose(ctxBG(), "", "run", "--rm", "--no-deps", "--entrypoint", "chown", "offsite", "65532:65532", "/backup")
		check(t, err == nil, "prepare backup folder: %v: %s", err, trunc([]byte(out)))
		backupRun(t)
		out, err = compose(ctxBG(), "", "run", "--rm", "--no-deps", "--entrypoint", "ls", "offsite", "/backup/db")
		check(t, err == nil && strings.Contains(out, "shop-"), "no snapshot in the backup folder: %v: %s", err, trunc([]byte(out)))
	})

	// Leg 1: restore from the backup folder.
	step(t, "restore from the backup folder: wipe data volume, restore, verify", func(t *testing.T) {
		wipeData(t)
		restore(t)
		verify(t)
	})

	// Leg 2: restore from an off-site copy, after losing the backup folder too.
	step(t, "off-site copy (rclone, encrypted) + health status", func(t *testing.T) {
		backupRun(t)
		out, err := compose(ctxBG(), "", "run", "--rm", "--no-deps", "offsite", "once")
		check(t, err == nil, "offsite once: %v: %s", err, trunc([]byte(out)))
		// The OCR service is not part of the drill, so /healthz as a whole is degraded; the
		// backup check is what matters here.
		_, b, err := raw("GET", "/healthz", nil, false)
		var h struct {
			Checks map[string]struct {
				Ok     bool   `json:"ok"`
				Detail string `json:"detail"`
			} `json:"checks"`
		}
		check(t, err == nil && json.Unmarshal(b, &h) == nil, "healthz: %v %s", err, trunc(b))
		bk := h.Checks["backup"]
		check(t, bk.Ok && strings.Contains(bk.Detail, "drill-offsite copy"), "healthz backup check after off-site copy: %+v", bk)
		files, err := compose(ctxBG(), "", "run", "--rm", "--no-deps", "--entrypoint", "find", "offsite", "/offsite-store", "-type", "f")
		check(t, err == nil && strings.Count(files, "\n") >= 5, "off-site store looks empty: %s", trunc([]byte(files)))
		check(t, !strings.Contains(files, "last_backup") && !strings.Contains(files, ".db") && !strings.Contains(files, ".jpg"),
			"off-site store has readable file names (not encrypted): %s", trunc([]byte(files)))
		note(t, "%d encrypted files at the destination; /healthz shows the copy", strings.Count(files, "\n"))
	})

	step(t, "disaster: lose data volume AND backup folder", func(t *testing.T) {
		wipeData(t)
		check(t, removeVolume(project+"_backup-dir") == nil, "could not remove the backup folder volume")
		check(t, !volumeExists(project+"_backup-dir"), "backup folder volume still exists")
	})

	step(t, "restore from the off-site copy: pull, restore, verify", func(t *testing.T) {
		out, err := compose(ctxBG(), "", "run", "--rm", "--no-deps", "offsite", "pull")
		check(t, err == nil, "offsite pull: %v: %s", err, trunc([]byte(out)))
		restore(t)
		verify(t)
	})
}
