package store

import (
	"fmt"
	"sync"
	"time"

	"github.com/org/ch-api/pkg/metrics"
	"github.com/org/ch-api/pkg/vmm"
)

// VM represents a virtual machine managed by the API.
type VM struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Config    vmm.VmConfig `json:"config"`
	Status    string       `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
}

// VMStore is an in-memory store for VMs guarded by a mutex.
type VMStore struct {
	mu      sync.RWMutex
	vms     map[string]*VM
	metrics *metrics.Registry
}

// NewVMStore creates a new VMStore.
func NewVMStore(mr *metrics.Registry) *VMStore {
	return &VMStore{vms: make(map[string]*VM), metrics: mr}
}

// Create adds a new VM.  Returns an error if the ID already exists.
func (s *VMStore) Create(vm *VM) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.vms[vm.ID]; exists {
		return fmt.Errorf("vm %q already exists", vm.ID)
	}
	s.vms[vm.ID] = vm
	if s.metrics != nil {
		s.metrics.VMActive.Inc()
	}
	return nil
}

// Get returns a VM by ID.  Returns an error if not found.
func (s *VMStore) Get(id string) (*VM, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	vm, exists := s.vms[id]
	if !exists {
		return nil, fmt.Errorf("vm %q not found", id)
	}
	// Return a shallow copy to prevent callers from mutating the stored config
	// without locking.  Deep-copy is done at the handler layer if needed.
	cp := *vm
	return &cp, nil
}

// List returns a snapshot of all VMs.
func (s *VMStore) List() []*VM {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*VM, 0, len(s.vms))
	for _, vm := range s.vms {
		cp := *vm
		out = append(out, &cp)
	}
	return out
}

// Update replaces an existing VM.  Returns an error if not found.
func (s *VMStore) Update(vm *VM) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.vms[vm.ID]; !exists {
		return fmt.Errorf("vm %q not found", vm.ID)
	}
	s.vms[vm.ID] = vm
	return nil
}

// Delete removes a VM by ID.  Returns an error if not found.
func (s *VMStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.vms[id]; !exists {
		return fmt.Errorf("vm %q not found", id)
	}
	delete(s.vms, id)
	if s.metrics != nil {
		s.metrics.VMActive.Dec()
	}
	return nil
}

// Count returns the number of stored VMs.
func (s *VMStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.vms)
}
