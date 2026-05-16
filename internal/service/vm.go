package service

import (
	"context"
	"fmt"
	"time"

	"github.com/org/ch-api/internal/store"
	"github.com/org/ch-api/pkg/eventlog"
	"github.com/org/ch-api/pkg/vmm"
)

// CreateVMRequest is the business input for creating a VM.
type CreateVMRequest struct {
	Name   string
	Config vmm.VmConfig
}

// CreateVM creates a new VM record.
func (s *Service) CreateVM(_ context.Context, req CreateVMRequest) (*store.VM, error) {
	id := generateVMID()
	vm := &store.VM{
		ID:        id,
		Name:      req.Name,
		Config:    req.Config,
		Status:    "created",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.VMs.Create(vm); err != nil {
		return nil, fmt.Errorf("create vm: %w", err)
	}
	s.logger.Info("vm created", "id", vm.ID, "name", vm.Name)
	s.emitEvent("create", vm.ID, "", "", vmState(vm))
	return vm, nil
}

// GetVM returns a VM by ID.
func (s *Service) GetVM(_ context.Context, id string) (*store.VM, error) {
	return s.store.VMs.Get(id)
}

// ListVMs returns all VMs.
func (s *Service) ListVMs(_ context.Context) ([]*store.VM, error) {
	return s.store.VMs.List(), nil
}

// DeleteVM deletes a VM by ID.
func (s *Service) DeleteVM(_ context.Context, id string) error {
	vm, err := s.store.VMs.Get(id)
	if err != nil {
		return fmt.Errorf("delete vm: %w", err)
	}
	before := vmState(vm)
	if err := s.store.VMs.Delete(id); err != nil {
		return fmt.Errorf("delete vm: %w", err)
	}
	s.logger.Info("vm deleted", "id", id)
	s.emitEvent("delete", id, "", before, "")
	return nil
}

// UpdateVM updates an existing VM record.
func (s *Service) UpdateVM(_ context.Context, vm *store.VM) error {
	old, err := s.store.VMs.Get(vm.ID)
	if err != nil {
		return fmt.Errorf("update vm: %w", err)
	}
	before := vmState(old)
	if err := s.store.VMs.Update(vm); err != nil {
		return fmt.Errorf("update vm: %w", err)
	}
	s.emitEvent("update", vm.ID, "", before, vmState(vm))
	return nil
}

// generateVMID creates a simple VM identifier.
func generateVMID() string {
	return fmt.Sprintf("vm-%d", time.Now().UnixNano())
}

// vmState returns a concise human-readable description of a VM's state.
func vmState(vm *store.VM) string {
	cpus := 0
	mem := int64(0)
	if vm.Config.CPUs != nil {
		cpus = vm.Config.CPUs.BootVCPUs
	}
	if vm.Config.Memory != nil {
		mem = vm.Config.Memory.Size
	}
	return fmt.Sprintf("status=%s name=%q cpus=%d mem=%dMB disks=%d net=%d",
		vm.Status, vm.Name, cpus, mem, len(vm.Config.Disks), len(vm.Config.Net))
}

// emitEvent writes a changelog entry when an eventlog writer is configured.
func (s *Service) emitEvent(event, vmID, actor, before, after string) {
	if s.eventlog == nil {
		return
	}
	_ = s.eventlog.Write(eventlog.Entry{
		Timestamp: time.Now().UTC(),
		Event:     event,
		VMID:      vmID,
		Actor:     actor,
		Before:    before,
		After:     after,
	})
}
