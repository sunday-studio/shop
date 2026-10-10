package spike

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"shop/internal/ocr"
)

// Engine is an OCR engine under test: an HTTP sidecar (URL) or a local
// command that prints the OCR contract JSON for an image path (Cmd).
type Engine struct {
	Name      string   `json:"name"`
	URL       string   `json:"url,omitempty"`
	Cmd       []string `json:"cmd,omitempty"`
	Container string   `json:"container,omitempty"` // docker container to sample memory from
	TimeoutS  int      `json:"timeout_s,omitempty"` // per-receipt timeout, default 120
	Notes     string   `json:"notes,omitempty"`
}

func (d Dir) LoadEngines() ([]Engine, error) {
	b, err := os.ReadFile(d.EnginesFile())
	if err != nil {
		return nil, err
	}
	var es []Engine
	if err := json.Unmarshal(b, &es); err != nil {
		return nil, fmt.Errorf("%s: %w", d.EnginesFile(), err)
	}
	for _, e := range es {
		if e.Name == "" || (e.URL == "") == (len(e.Cmd) == 0) {
			return nil, fmt.Errorf("engine %q: set exactly one of url or cmd", e.Name)
		}
	}
	return es, nil
}

// Recognize runs the engine on one image.
func (e Engine) Recognize(ctx context.Context, root, imagePath string) (ocr.Result, error) {
	if e.URL != "" {
		f, err := os.Open(imagePath)
		if err != nil {
			return ocr.Result{}, err
		}
		defer f.Close()
		timeout := 120 * time.Second
		if e.TimeoutS > 0 {
			timeout = time.Duration(e.TimeoutS) * time.Second
		}
		return ocr.NewWithTimeout(e.URL, timeout).Recognize(ctx, filepath.Base(imagePath), f)
	}
	cmd := e.Cmd[0]
	if !filepath.IsAbs(cmd) && strings.Contains(cmd, "/") {
		cmd = filepath.Join(root, cmd) // relative to the repo root
	}
	var out, stderr bytes.Buffer
	c := exec.CommandContext(ctx, cmd, append(e.Cmd[1:], imagePath)...)
	c.Stdout, c.Stderr = &out, &stderr
	if err := c.Run(); err != nil {
		return ocr.Result{}, fmt.Errorf("%s: %v: %s", e.Name, err, strings.TrimSpace(stderr.String()))
	}
	var res ocr.Result
	return res, json.Unmarshal(out.Bytes(), &res)
}

// Ready checks an HTTP engine's /healthz (commands are assumed ready).
func (e Engine) Ready(ctx context.Context) error {
	if e.URL == "" {
		return nil
	}
	h, err := ocr.New(e.URL).Health(ctx)
	if err != nil {
		return err
	}
	if !h.Ready {
		return errors.New("engine still loading")
	}
	return nil
}

// memSampler polls `docker stats` for a container and keeps the peak.
type memSampler struct {
	mu   sync.Mutex
	peak float64
	stop chan struct{}
	done chan struct{}
}

var memRe = regexp.MustCompile(`^([\d.]+)\s*([KMG]i?B)`)

func parseMem(s string) (float64, bool) {
	m := memRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	switch strings.TrimSuffix(m[2], "B") {
	case "K", "Ki":
		v /= 1024
	case "G", "Gi":
		v *= 1024
	}
	return v, true // MB
}

func startMemSampler(container string) *memSampler {
	s := &memSampler{stop: make(chan struct{}), done: make(chan struct{})}
	if container == "" {
		close(s.done)
		return s
	}
	go func() {
		defer close(s.done)
		for {
			out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.MemUsage}}", container).Output()
			if err == nil {
				if mb, ok := parseMem(string(out)); ok {
					s.mu.Lock()
					s.peak = max(s.peak, mb)
					s.mu.Unlock()
				}
			}
			select {
			case <-s.stop:
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
	return s
}

func (s *memSampler) Stop() float64 {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}
