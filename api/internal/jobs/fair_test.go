package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shop/internal/store"
	"shop/internal/store/db"
)

type fakeSource struct {
	queues map[string]*Queue
	conns  map[string]*sql.DB
}

func (s *fakeSource) IDs(context.Context) ([]string, error) {
	var ids []string
	for id := range s.queues {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *fakeSource) Queue(_ context.Context, id string) (*Queue, func(), error) {
	return s.queues[id], func() {}, nil
}

func newSource(t *testing.T, ids ...string) *fakeSource {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &fakeSource{queues: map[string]*Queue{}, conns: map[string]*sql.DB{}}
	for _, id := range ids {
		conn, err := store.Open(filepath.Join(t.TempDir(), id+".db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		if err := store.Migrate(context.Background(), conn); err != nil {
			t.Fatal(err)
		}
		s.conns[id] = conn
		s.queues[id] = New(conn, log)
	}
	return s
}

func enqueue(t *testing.T, s *fakeSource, id string, n int) {
	t.Helper()
	for i := range n {
		if err := Enqueue(context.Background(), db.New(s.conns[id]), "work", map[string]any{"h": id, "i": i}); err != nil {
			t.Fatal(err)
		}
	}
}

// A household with a long backlog must not starve a household with one job, and no household ever
// runs two jobs at once.
func TestFairRoundRobinAndOnePerHousehold(t *testing.T) {
	s := newSource(t, "a", "b", "c")
	enqueue(t, s, "a", 50)
	enqueue(t, s, "b", 1)
	enqueue(t, s, "c", 2)

	var mu sync.Mutex
	var order []string
	running := map[string]int{}
	maxRunning := map[string]int{}
	for id, q := range s.queues {
		q.Register("work", Handler{Run: func(ctx context.Context, payload []byte) error {
			mu.Lock()
			running[id]++
			maxRunning[id] = max(maxRunning[id], running[id])
			mu.Unlock()
			time.Sleep(3 * time.Millisecond)
			mu.Lock()
			running[id]--
			order = append(order, id)
			mu.Unlock()
			return nil
		}})
	}
	f := &Fair{Source: s, Workers: 4, Sweep: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Run(ctx); close(done) }()
	deadline := time.After(20 * time.Second)
	for {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n == 53 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d of 53 jobs ran", n)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	for id, m := range maxRunning {
		if m != 1 {
			t.Errorf("household %s ran %d jobs at once", id, m)
		}
	}
	pos := func(id string) int {
		for i, h := range order {
			if h == id {
				return i
			}
		}
		return -1
	}
	if p := pos("b"); p < 0 || p > 3 {
		t.Errorf("b's only job ran at position %d; order starts %v", p, order[:8])
	}
	// c's two jobs are done long before a's 50.
	lastC := -1
	for i, h := range order {
		if h == "c" {
			lastC = i
		}
	}
	if lastC > 12 {
		t.Errorf("c's jobs finished at position %d; order %v", lastC, order[:16])
	}
}

// Hint wakes the scheduler for a household that enqueues after the sweep.
func TestFairHintPicksUpNewWork(t *testing.T) {
	s := newSource(t, "a")
	var ran atomic.Int32
	s.queues["a"].Register("work", Handler{Run: func(context.Context, []byte) error { ran.Add(1); return nil }})
	f := &Fair{Source: s, Workers: 1, Sweep: time.Hour}
	s.queues["a"].SetNotifyHook(func() { f.Hint("a") })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond) // the first sweep found nothing
	enqueue(t, s, "a", 1)
	s.queues["a"].Notify()
	for i := 0; i < 200 && ran.Load() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if ran.Load() != 1 {
		t.Fatalf("job ran %d times", ran.Load())
	}
	_ = fmt.Sprint()
}
