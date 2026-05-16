package service

import (
	"context"

	"github.com/org/ch-api/internal/store"
	"github.com/org/ch-api/pkg/eventlog"
	"github.com/org/ch-api/pkg/logging"
)

// vmmLifecycler is the subset of the VMM client used by lifecycle operations.
// *vmm.Client satisfies this interface automatically.
type vmmLifecycler interface {
	Boot(ctx context.Context) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Shutdown(ctx context.Context) error
	Reboot(ctx context.Context) error
}

// Service is the business logic layer that orchestrates store calls and VMM
// interactions.
type Service struct {
	store     *store.Store
	logger    logging.Logger
	vmmClient vmmLifecycler
	eventlog  eventlog.Writer
}

// New creates a Service backed by the given store and logger.
// The eventlog writer is optional and may be nil.
func New(s *store.Store, logger logging.Logger, el eventlog.Writer) *Service {
	return &Service{
		store:    s,
		logger:   logger,
		eventlog: el,
	}
}

// SetVMMClient injects the VMM client for lifecycle operations.
func (s *Service) SetVMMClient(c vmmLifecycler) {
	s.vmmClient = c
}