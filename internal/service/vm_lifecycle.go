package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/store"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/vmm"
)

// ---------------------------------------------------------------------------
// VM lifecycle operations
// ---------------------------------------------------------------------------

// BootVM boots the VM identified by id.
func (s *Service) BootVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "boot", "running", func(ctx context.Context) error {
		diskPath, err := s.imageMgr.CreateDisk(id)
		if err != nil {
			return fmt.Errorf("create disk: %w", err)
		}

		tapCfg, err := s.networkMgr.Allocate(id)
		if err != nil {
			return fmt.Errorf("allocate network: %w", err)
		}
		if err := s.networkMgr.SetupTAP(tapCfg); err != nil {
			return fmt.Errorf("setup tap: %w", err)
		}

		socketPath := fmt.Sprintf("/var/run/ch-api/%s.sock", id)

		cmd := exec.Command("cloud-hypervisor", "--api-socket", socketPath)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start cloud-hypervisor: %w", err)
		}
		s.chProcesses[id] = cmd.Process

		// Wait for socket to appear (max 3 seconds)
		for i := 0; i < 10; i++ {
			if _, err := os.Stat(socketPath); err == nil {
				break
			}
			if i == 9 {
				return fmt.Errorf("timeout waiting for socket %s", socketPath)
			}
			time.Sleep(300 * time.Millisecond)
		}

		client := vmm.New(vmm.Config{
			Transport: vmm.TransportUnixSock,
			Address:   socketPath,
			Logger:    s.logger,
		})
		s.vmmClients[id] = client

		vm, err := s.store.VMs.Get(id)
		if err != nil {
			return fmt.Errorf("get vm: %w", err)
		}

		vm.Config.Disks = []vmm.DiskConfig{
			{Path: diskPath, Readonly: false},
		}

		vm.Config.Net = []vmm.NetConfig{
			{
				Tap:  tapCfg.TAPName,
				IP:   tapCfg.VMIP,
				Mask: "255.255.255.252",
			},
		}

		serialSocket := fmt.Sprintf("/var/run/ch-api/%s-serial.sock", id)
		vm.Config.Serial = &vmm.ConsoleConfig{
			Mode:   "Socket",
			Socket: serialSocket,
		}
		vm.Config.Console = &vmm.ConsoleConfig{
			Mode: "Off",
		}

		if err := client.Create(ctx, &vm.Config); err != nil {
			return fmt.Errorf("create vm in vmm: %w", err)
		}

		return client.Boot(ctx)
	})
}

// PauseVM pauses the VM identified by id.
func (s *Service) PauseVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "pause", "paused", func(ctx context.Context) error {
		client, ok := s.vmmClients[id]
		if !ok || client == nil {
			return fmt.Errorf("vmm client not found for vm %s", id)
		}
		return client.Pause(ctx)
	})
}

// ResumeVM resumes the paused VM identified by id.
func (s *Service) ResumeVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "resume", "running", func(ctx context.Context) error {
		client, ok := s.vmmClients[id]
		if !ok || client == nil {
			return fmt.Errorf("vmm client not found for vm %s", id)
		}
		return client.Resume(ctx)
	})
}

// ShutdownVM shuts down the VM identified by id.
func (s *Service) ShutdownVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "shutdown", "stopped", func(ctx context.Context) error {
		client, ok := s.vmmClients[id]
		if !ok || client == nil {
			return fmt.Errorf("vmm client not found for vm %s", id)
		}
		if err := client.Shutdown(ctx); err != nil {
			return err
		}
		if proc, ok := s.chProcesses[id]; ok {
			_ = proc.Kill()
			delete(s.chProcesses, id)
		}
		if _, ok := s.vmmClients[id]; ok {
			delete(s.vmmClients, id)
		}
		socketPath := fmt.Sprintf("/var/run/ch-api/%s.sock", id)
		_ = os.Remove(socketPath)
		if tapCfg, ok := s.networkMgr.Get(id); ok {
			_ = s.networkMgr.TeardownTAP(tapCfg)
			_ = s.networkMgr.Release(id)
		}
		_ = s.imageMgr.DeleteDisk(id)
		return nil
	})
}

// RebootVM reboots the VM identified by id.
func (s *Service) RebootVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "reboot", "running", func(ctx context.Context) error {
		client, ok := s.vmmClients[id]
		if !ok || client == nil {
			return fmt.Errorf("vmm client not found for vm %s", id)
		}
		return client.Reboot(ctx)
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
