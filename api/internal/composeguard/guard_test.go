// Package composeguard holds a test that keeps the isolated test harnesses (make e2e,
// make restore-drill) from ever addressing the live docker compose project "shop".
package composeguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// harness describes one isolated harness: its Go package, the project it may use, and
// the compose override file that must pin the same project name.
type harness struct {
	dir, project, override string
}

var harnesses = []harness{
	{"api/e2e", "shop-e2e", "deploy/docker-compose.e2e.yml"},
	{"api/drill", "shop-drill", "deploy/docker-compose.drill.yml"},
}

func TestHarnessesNeverAddressTheLiveStack(t *testing.T) {
	for _, h := range harnesses {
		files, _ := filepath.Glob(filepath.Join("..", "..", "..", h.dir, "*.go"))
		if len(files) == 0 {
			t.Fatalf("%s: no files", h.dir)
		}
		compose := 0
		for _, f := range files {
			b, _ := os.ReadFile(f)
			src := string(b)
			lines := strings.Split(src, "\n")
			for i, l := range lines {
				code := strings.TrimSpace(l)
				if strings.HasPrefix(code, "//") {
					continue
				}
				// Every compose invocation is built in func compose(), nowhere else.
				if strings.Contains(code, `"compose"`) {
					compose++
					if !strings.Contains(src, "func compose(") || !strings.Contains(strings.Join(lines[max(0, i-12):i+1], "\n"), `"-p", project`) {
						t.Errorf("%s:%d: compose call outside the guarded compose() wrapper: %s", f, i+1, code)
					}
				}
				// No stopping or killing containers by hand, and no name patterns.
				if regexp.MustCompile(`"(stop|kill|restart|pause)"`).MatchString(code) && strings.Contains(code, "docker") {
					t.Errorf("%s:%d: docker stop/kill/restart is not allowed in a harness: %s", f, i+1, code)
				}
				if strings.Contains(code, `"rm"`) && strings.Contains(code, `"docker"`) && !strings.Contains(code, `"volume", "rm", name`) {
					t.Errorf("%s:%d: raw docker rm: use compose() or removeVolume(): %s", f, i+1, code)
				}
			}
			if strings.Contains(src, "func compose(") {
				if !strings.Contains(src, `project != "`+h.project+`"`) || !strings.Contains(src, "refusing to run compose") {
					t.Errorf("%s: compose() must refuse any project but %s", f, h.project)
				}
				if !strings.Contains(src, `project  = "`+h.project+`"`) && !strings.Contains(src, `project   = "`+h.project+`"`) {
					t.Errorf("%s: project constant must be %s", f, h.project)
				}
			}
			if strings.Contains(src, "func removeVolume(") && !strings.Contains(src, `HasPrefix(name, project+"_")`) {
				t.Errorf("%s: removeVolume must check the project prefix", f)
			}
		}
		if compose == 0 {
			t.Errorf("%s: expected a compose call in the wrapper", h.dir)
		}
		ov := read(t, h.override)
		if !regexp.MustCompile(`(?m)^name: ` + h.project + `\b`).MatchString(ov) {
			t.Errorf("%s must set name: %s", h.override, h.project)
		}
	}
}

func TestMakefileHarnessTargetsDoNotCallCompose(t *testing.T) {
	mk := read(t, "Makefile")
	for _, target := range []string{"e2e", "restore-drill"} {
		re := regexp.MustCompile(`(?m)^` + target + `:.*\n((?:\t.*\n?)+)`)
		m := re.FindStringSubmatch(mk)
		if m == nil {
			t.Fatalf("Makefile target %s not found", target)
		}
		if strings.Contains(m[1], "compose") || strings.Contains(m[1], "$(COMPOSE)") || regexp.MustCompile(`docker\s+(stop|kill|rm|restart)`).MatchString(m[1]) {
			t.Errorf("target %s must only run the Go harness, got:\n%s", target, m[1])
		}
	}
}

func TestNoOverridePinsTheLiveProjectName(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "deploy", "*.yml"))
	files2, _ := filepath.Glob(filepath.Join("..", "..", "..", "deploy", "*", "*.yml"))
	for _, f := range append(files, files2...) {
		b, _ := os.ReadFile(f)
		base := filepath.Base(f)
		isLive := base == "docker-compose.yml" && filepath.Base(filepath.Dir(f)) == "deploy"
		hasLive := regexp.MustCompile(`(?m)^name: shop\s*(#.*)?$`).Match(b)
		if hasLive && !isLive {
			t.Errorf("%s sets name: shop; only deploy/docker-compose.yml may", f)
		}
		if strings.Contains(base, "e2e") || strings.Contains(base, "drill") {
			if !regexp.MustCompile(`(?m)^name: shop-(e2e|drill)\b`).Match(b) {
				t.Errorf("%s must set its own name:", f)
			}
		}
	}
}

// The hosted server kit (deploy/hosted) is shell, not Go: its scripts may start docker compose only
// through the two wrappers, each of which pins its project and refuses any other.
func hostedFiles(t *testing.T, exts ...string) []string {
	t.Helper()
	var out []string
	root := filepath.Join("..", "..", "..", "deploy", "hosted")
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		for _, e := range exts {
			if strings.HasSuffix(p, e) {
				out = append(out, p)
			}
		}
		return nil
	})
	if err != nil || len(out) == 0 {
		t.Fatalf("no files under %s: %v", root, err)
	}
	return out
}

func TestHostedKitScriptsCallComposeOnlyThroughWrappers(t *testing.T) {
	wrappers := map[string]string{
		filepath.Join("deploy", "hosted", "compose.sh"):         `PROJECT="shop-hosted"`,
		filepath.Join("deploy", "hosted", "test", "compose.sh"): `PROJECT="shop-hosted-test"`,
	}
	composeCall := regexp.MustCompile(`docker\s+compose(\s|$)|docker-compose(\s|$)`)
	destructive := regexp.MustCompile(`docker\s+(stop|kill|rm|restart|pause)\b|docker\s+system\s+prune|docker\s+volume\s+(rm|prune)|docker\s+container\s+(rm|prune|stop|kill)`)
	for _, f := range hostedFiles(t, ".sh") {
		rel, _ := filepath.Rel(filepath.Join("..", "..", ".."), f)
		b, _ := os.ReadFile(f)
		_, isWrapper := wrappers[rel]
		inTest := strings.Contains(rel, filepath.Join("hosted", "test")+string(filepath.Separator))
		for i, l := range strings.Split(string(b), "\n") {
			code := strings.TrimSpace(l)
			if strings.HasPrefix(code, "#") {
				continue
			}
			if composeCall.MatchString(code) && !isWrapper {
				t.Errorf("%s:%d: compose call outside the guarded wrapper: %s", rel, i+1, code)
			}
			if destructive.MatchString(code) {
				t.Errorf("%s:%d: raw docker stop/kill/rm/prune is not allowed in the kit: %s", rel, i+1, code)
			}
			if !inTest && regexp.MustCompile(`\bdown\b.*(\s-v\b|--volumes)`).MatchString(code) && !strings.Contains(rel, "compose.sh") {
				t.Errorf("%s:%d: 'down -v' is only allowed in the test harness: %s", rel, i+1, code)
			}
		}
	}
	for rel, pin := range wrappers {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", rel))
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, pin) || !strings.Contains(src, "refusing to run compose") {
			t.Errorf("%s must pin %s and refuse any other project", rel, pin)
		}
		if !strings.Contains(src, `-p "$PROJECT"`) || !strings.Contains(src, "--project-name") {
			t.Errorf("%s must pass -p \"$PROJECT\" and reject -p/--project-name from callers", rel)
		}
	}
	prod, _ := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "hosted", "compose.sh"))
	if !strings.Contains(string(prod), "refusing 'down") {
		t.Errorf("the production wrapper must refuse down -v")
	}
}

func TestHostedKitComposeFilesPinTheirOwnProject(t *testing.T) {
	want := map[string]string{
		"docker-compose.yml":      "shop-hosted",
		"docker-compose.test.yml": "shop-hosted-test",
	}
	for _, f := range hostedFiles(t, ".yml") {
		b, _ := os.ReadFile(f)
		base := filepath.Base(f)
		if regexp.MustCompile(`(?m)^name: shop\s*(#.*)?$`).Match(b) {
			t.Errorf("%s sets name: shop; only deploy/docker-compose.yml may", f)
		}
		if w, ok := want[base]; ok && !regexp.MustCompile(`(?m)^name: `+w+`\s*(#.*)?$`).Match(b) {
			t.Errorf("%s must set name: %s", f, w)
		}
		if base == "docker-compose.yml" && !regexp.MustCompile(`(?m)^\s+ports:\s*\["80:80"`).Match(b) {
			t.Errorf("%s: only caddy may publish ports (80/443)", f)
		}
	}
	mk := read(t, "Makefile")
	m := regexp.MustCompile(`(?m)^hosted-kit-test:.*\n((?:\t.*\n?)+)`).FindStringSubmatch(mk)
	if m == nil {
		t.Fatal("Makefile target hosted-kit-test not found")
	}
	if strings.Contains(m[1], "compose") || regexp.MustCompile(`docker\s+(stop|kill|rm|restart)`).MatchString(m[1]) {
		t.Errorf("target hosted-kit-test must only run deploy/hosted/test/run.sh, got:\n%s", m[1])
	}
}
