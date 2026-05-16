package lifecycle

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/org/ch-api/pkg/logging"
)

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Warn(string, ...any)  {}
func (l noopLogger) WithContext(context.Context) logging.Logger { return l }
func (l noopLogger) With(...any) logging.Logger                  { return l }

func TestManagerStartStopOrder(t *testing.T) {
	var order []string
	var mu sync.Mutex

	makeComp := func(name string) Component {
		return &testComponent{
			name: name,
			onStart: func() error {
				mu.Lock()
				order = append(order, "start:"+name)
				mu.Unlock()
				return nil
			},
			onStop: func() error {
				mu.Lock()
				order = append(order, "stop:"+name)
				mu.Unlock()
				return nil
			},
		}
	}

	mgr := NewManager(noopLogger{}, WithStopTimeout(5*time.Second))
	mgr.Register(makeComp("a"), makeComp("b"), makeComp("c"))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if err := mgr.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	want := []string{
		"start:a", "start:b", "start:c",
		"stop:c", "stop:b", "stop:a",
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()

	if len(got) != len(want) {
		t.Fatalf("order mismatch: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch at %d: got %v, want %v", i, got, want)
		}
	}
}

func TestManagerStartupFailureRollback(t *testing.T) {
	var stopped int32

	compA := &testComponent{
		name: "a",
		onStart: func() error { return nil },
		onStop:  func() error { atomic.AddInt32(&stopped, 1); return nil },
	}
	compB := &testComponent{
		name: "b",
		onStart: func() error { return errors.New("boom") },
		onStop:  func() error { atomic.AddInt32(&stopped, 1); return nil },
	}

	mgr := NewManager(noopLogger{}, WithStopTimeout(5*time.Second))
	mgr.Register(compA, compB)

	ctx := context.Background()
	err := mgr.Run(ctx)
	if err == nil {
		t.Fatal("expected startup error")
	}
	if atomic.LoadInt32(&stopped) != 1 {
		t.Fatalf("expected 1 stop call (rollback of a), got %d", stopped)
	}
}

func TestManagerStopTimeout(t *testing.T) {
	var stopCalled int32
	comp := &testComponent{
		name: "slow",
		onStart: func() error { return nil },
		onStopCtx: func(ctx context.Context) error {
			atomic.AddInt32(&stopCalled, 1)
			select {
			case <-time.After(5 * time.Second):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}

	mgr := NewManager(noopLogger{}, WithStopTimeout(100*time.Millisecond))
	mgr.Register(comp)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	// Should return without error because the component respects the context
	// and returns ctx.Err() when the deadline is exceeded.
	err := mgr.Run(ctx)
	if err != nil {
		t.Fatalf("expected no error on timeout, got %v", err)
	}
	if atomic.LoadInt32(&stopCalled) != 1 {
		t.Fatal("expected Stop to be called")
	}
}

func TestManagerNoComponents(t *testing.T) {
	mgr := NewManager(noopLogger{}, WithStopTimeout(100*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if err := mgr.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
}

type testComponent struct {
	name      string
	onStart   func() error
	onStop    func() error
	onStopCtx func(context.Context) error
}

func (c *testComponent) Name() string                { return c.name }
func (c *testComponent) Start(context.Context) error { return c.onStart() }
func (c *testComponent) Stop(ctx context.Context) error {
	if c.onStopCtx != nil {
		return c.onStopCtx(ctx)
	}
	return c.onStop()
}