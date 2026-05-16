package service

import (
	"context"
	"os"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/vmm"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/store"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/eventlog"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/network"
)

// vmmLifecycler is the subset of the VMM client used by lifecycle operations.
// *vmm.Client satisfies this interface automatically.

type vmmLifecycler interface {
	Create(ctx context.Context, cfg *vmm.VmConfig) error
	Boot(ctx context.Context) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Shutdown(ctx context.Context) error
	Reboot(ctx context.Context) error
}

// Service is the business logic layer that orchestrates store calls and VMM
// interactions.
type Service struct {
	store       *store.Store
	logger      logging.Logger
	vmmClients  map[string]vmmLifecycler
	chProcesses map[string]*os.Process
	networkMgr  *network.Manager
	eventlog    eventlog.Writer
}

// New creates a Service backed by the given store, logger, and network manager.
// The eventlog writer is optional and may be nil.
func New(s *store.Store, logger logging.Logger, el eventlog.Writer, networkMgr *network.Manager) *Service {
	return &Service{
		store:       s,
		logger:      logger,
		vmmClients:  make(map[string]vmmLifecycler),
		chProcesses: make(map[string]*os.Process),
		networkMgr:  networkMgr,
		eventlog:    el,
	}
}
