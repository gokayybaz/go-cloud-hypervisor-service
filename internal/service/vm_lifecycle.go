package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/store"
)

// ---------------------------------------------------------------------------
// VM lifecycle operations
// ---------------------------------------------------------------------------

// BootVM boots the VM identified by id.
func (s *Service) BootVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "boot", "running", func(ctx context.Context) error {
		if s.vmmClient == nil {
			return fmt.Errorf("vmm client not configured")
		}

		// VM config'i store'dan al ve CH'ye gönder
		vm, err := s.store.VMs.Get(id)
		if err != nil {
			return fmt.Errorf("get vm config: %w", err)
		}

		if err := s.vmmClient.Create(ctx, &vm.Config); err != nil {
			return fmt.Errorf("create vm in vmm: %w", err)
		}

		return s.vmmClient.Boot(ctx)
	})
}

// PauseVM pauses the VM identified by id.
func (s *Service) PauseVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "pause", "paused", func(ctx context.Context) error {
		if s.vmmClient == nil {
			return fmt.Errorf("vmm client not configured")
		}
		return s.vmmClient.Pause(ctx)
	})
}

// ResumeVM resumes the paused VM identified by id.
func (s *Service) ResumeVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "resume", "running", func(ctx context.Context) error {
		if s.vmmClient == nil {
			return fmt.Errorf("vmm client not configured")
		}
		return s.vmmClient.Resume(ctx)
	})
}

// ShutdownVM shuts down the VM identified by id.
func (s *Service) ShutdownVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "shutdown", "stopped", func(ctx context.Context) error {
		if s.vmmClient == nil {
			return fmt.Errorf("vmm client not configured")
		}
		return s.vmmClient.Shutdown(ctx)
	})
}

// RebootVM reboots the VM identified by id.
func (s *Service) RebootVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "reboot", "running", func(ctx context.Context) error {
		if s.vmmClient == nil {
			return fmt.Errorf("vmm client not configured")
		}
		return s.vmmClient.Reboot(ctx)
	})
}

// transition performs a lifecycle state transition.
//   - op: operation name for the log
//   - successStatus: VM status to set on success
//   - action: the VMM client call
func (s *Service) transition(ctx context.Context, id, user, op, successStatus string, action func(context.Context) error) error {
	vm, err := s.store.VMs.Get(id)
	if err != nil {
		s.logOperation(id, user, op, "error", err.Error())
		return fmt.Errorf("%s vm: %w", op, err)
	}

	before := vmState(vm)
	outcome := "success"
	msg := ""

	if err := action(ctx); err != nil {
		outcome = "error"
		msg = err.Error()
	}

	if outcome == "success" {
		vm.Status = successStatus
		if uerr := s.store.VMs.Update(vm); uerr != nil {
			s.logOperation(id, user, op, "error", uerr.Error())
			return fmt.Errorf("%s vm: %w", op, uerr)
		}
	}

	s.logOperation(id, user, op, outcome, msg)

	if outcome == "error" {
		return fmt.Errorf("%s vm: %s", op, msg)
	}

	// Capture after-state only on success.
	after := vmState(vm)
	s.emitEvent(op, id, user, before, after)

	s.logger.Info("vm transitioned", "id", id, "operation", op, "status", vm.Status, "user", user)
	return nil
}

// logOperation writes an entry to the VM operation log.
func (s *Service) logOperation(vmID, user, op, outcome, msg string) {
	entry := store.OperationLogEntry{
		Timestamp: time.Now().UTC(),
		VMID:      vmID,
		Operation: op,
		User:      user,
		Outcome:   outcome,
		Message:   msg,
	}
	if err := s.store.VMOperationLog.Write(entry); err != nil {
		s.logger.Error("failed to write operation log", "err", err)
	}
}
