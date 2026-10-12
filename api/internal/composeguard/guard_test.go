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
