package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
)

// Component is the unit of lifecycle management.  Each component is started
// in registration order and stopped in reverse order.
type Component interface {
	// Name returns a human-readable identifier used in logs.
	Name() string

	// Start is called during startup.  The provided context is cancelled when
	// the lifecycle manager begins shutdown.  Start should return as soon as
	// the component is ready; long-running work should be started in a
	// goroutine spawned by the component itself.
	Start(ctx context.Context) error

	// Stop is called during shutdown.  The provided context carries the
	// shutdown deadline.  Stop should complete before the context is cancelled
	// or the manager will log a timeout and continue.
	Stop(ctx context.Context) error
}

// Manager orchestrates the startup and graceful shutdown of a set of
// components.  It handles OS signals, logs every transition, and enforces
// shutdown timeouts.
type Manager struct {
	logger      logging.Logger
	components  []Component
	stopTimeout time.Duration
	signals     []os.Signal
}

// Option configures a Manager.
type Option func(*Manager)

// WithStopTimeout sets the maximum duration allowed for the entire shutdown
// sequence.  Each component receives a sub-context with a proportional share
// of this timeout.
func WithStopTimeout(d time.Duration) Option {
	return func(m *Manager) { m.stopTimeout = d }
}

// WithSignals overrides the default signal set (SIGINT, SIGTERM).
func WithSignals(sigs ...os.Signal) Option {
	return func(m *Manager) { m.signals = sigs }
}

// NewManager creates a lifecycle manager.
func NewManager(logger logging.Logger, opts ...Option) *Manager {
	m := &Manager{
		logger:      logger,
		stopTimeout: 30 * time.Second,
		signals:     []os.Signal{syscall.SIGINT, syscall.SIGTERM},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Register adds one or more components to the manager.  Components are
// started in the order they are registered and stopped in reverse order.
func (m *Manager) Register(c ...Component) {
	m.components = append(m.components, c...)
}

// Run starts all registered components, then blocks until an OS signal is
// received or the provided context is cancelled.  On shutdown it stops all
// components in reverse order with a timeout.  Returns a non-nil error if
// startup fails or if any component fails to stop gracefully.
func (m *Manager) Run(ctx context.Context) error {
	if len(m.components) == 0 {
		m.logger.Warn("lifecycle manager started with no components")
	}

	// Phase 1: start all components.
	startCtx, startCancel := context.WithCancel(ctx)
	defer startCancel()

	var started []Component
	for _, c := range m.components {
		m.logger.Info("starting component", "component", c.Name())
		if err := c.Start(startCtx); err != nil {
			m.logger.Error("component startup failed", "component", c.Name(), "err", err)
			// Stop already-started components before returning.
			_ = m.stopReverse(startCtx, started)
			return fmt.Errorf("start %s: %w", c.Name(), err)
		}
		m.logger.Info("component started", "component", c.Name())
		started = append(started, c)
	}

	// Phase 2: wait for signal or context cancellation.
	sigCtx, sigStop := signal.NotifyContext(ctx, m.signals...)
	defer sigStop()

	m.logger.Info("all components started, waiting for shutdown signal")
	<-sigCtx.Done()

	// Determine why we are shutting down.
	reason := "shutdown signal received"
	if ctx.Err() != nil {
		reason = "context cancelled"
	}
	m.logger.Info(reason)

	// Phase 3: graceful shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), m.stopTimeout)
	defer shutdownCancel()

	return m.stopReverse(shutdownCtx, m.components)
}

// stopReverse stops the provided components in reverse order.  The timeout
// is divided equally among the components.
func (m *Manager) stopReverse(ctx context.Context, components []Component) error {
	if len(components) == 0 {
		return nil
	}

	// Give each component a fair slice of the total shutdown time.
	slice := m.stopTimeout / time.Duration(len(components))
	if slice < 1*time.Second {
		slice = 1 * time.Second
	}

	var errs []error
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := len(components) - 1; i >= 0; i-- {
		c := components[i]
		m.logger.Info("stopping component", "component", c.Name(), "timeout", slice.String())

		stopCtx, cancel := context.WithTimeout(ctx, slice)

		wg.Add(1)
		go func(comp Component) {
			defer wg.Done()
			defer cancel()

			if err := comp.Stop(stopCtx); err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					m.logger.Warn("component stop timed out", "component", comp.Name())
					return
				}
				mu.Lock()
				errs = append(errs, fmt.Errorf("stop %s: %w", comp.Name(), err))
				mu.Unlock()
				m.logger.Error("component stop failed", "component", comp.Name(), "err", err)
				return
			}
			m.logger.Info("component stopped", "component", comp.Name())
		}(c)

		wg.Wait()

		// If the overall shutdown context has expired, log and continue with
		// the remaining components rather than leaving them hanging.
		if ctx.Err() != nil {
			m.logger.Warn("shutdown timeout exceeded, forcing remaining components", "remaining", i)
			break
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

