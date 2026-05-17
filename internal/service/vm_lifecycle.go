package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/store"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/cloudinit"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/vmm"
)

// ---------------------------------------------------------------------------
// VM lifecycle operations
// ---------------------------------------------------------------------------

// BootVM boots the VM identified by id.
func (s *Service) BootVM(ctx context.Context, id, user string) error {
	return s.transition(ctx, id, user, "boot", "running", func(ctx context.Context) error {
		vm, err := s.store.VMs.Get(id)
		if err != nil {
			return fmt.Errorf("get vm: %w", err)
		}

		// Step 1: Create VM disk from base image
		diskPath, err := s.imageMgr.CreateDisk(id)
		if err != nil {
			return fmt.Errorf("create disk: %w", err)
		}

		// Step 2: Generate SSH keypair
		keyPair, err := s.sshKeyMgr.Generate(id)
		if err != nil {
			_ = s.imageMgr.DeleteDisk(id)
			return fmt.Errorf("generate ssh key: %w", err)
		}

		// Step 3: Allocate and setup TAP network
		tapCfg, err := s.networkMgr.Allocate(id)
		if err != nil {
			_ = s.imageMgr.DeleteDisk(id)
			_ = s.sshKeyMgr.Delete(id)
			return fmt.Errorf("allocate network: %w", err)
		}
		if err := s.networkMgr.SetupTAP(tapCfg); err != nil {
			_ = s.networkMgr.Release(id)
			_ = s.imageMgr.DeleteDisk(id)
			_ = s.sshKeyMgr.Delete(id)
			return fmt.Errorf("setup tap: %w", err)
		}

		// Step 4: Inject cloud-init seed files directly into VM disk
		seedFiles := cloudinit.BuildSeedFiles(cloudinit.Config{
			InstanceID:   id,
			Hostname:     vm.Name,
			SSHPublicKey: strings.TrimSpace(keyPair.PublicKey),
			MAC:          tapCfg.MAC,
			VMIP:         tapCfg.VMIP,
			Gateway:      tapCfg.HostIP,
		})
		if err := s.imageMgr.InjectCloudInitSeed(id, seedFiles); err != nil {
			_ = s.networkMgr.TeardownTAP(tapCfg)
			_ = s.networkMgr.Release(id)
			_ = s.imageMgr.DeleteDisk(id)
			_ = s.sshKeyMgr.Delete(id)
			return fmt.Errorf("inject cloud-init seed: %w", err)
		}

		// Step 5: Allow DHCP through firewall
		if err := s.networkMgr.AllowDHCP(tapCfg.TAPName); err != nil {
			s.logger.Warn("ufw dhcp rule failed, continuing", "err", err)
		}

		// Step 6: Start DHCP server for this VM
		if err := s.dhcpMgr.StartForVM(id, tapCfg.TAPName, tapCfg.HostIP, tapCfg.VMIP); err != nil {
			_ = s.networkMgr.TeardownTAP(tapCfg)
			_ = s.networkMgr.Release(id)
			_ = s.imageMgr.DeleteDisk(id)
			_ = s.sshKeyMgr.Delete(id)
			return fmt.Errorf("start dhcp: %w", err)
		}

		// Step 7: Start Cloud Hypervisor process
		socketPath := fmt.Sprintf("/var/run/ch-api/%s.sock", id)
		serialSocket := fmt.Sprintf("/var/run/ch-api/%s-serial.sock", id)

		cmd := exec.Command("cloud-hypervisor", "--api-socket", socketPath)
		if err := cmd.Start(); err != nil {
			s.dhcpMgr.StopForVM(id)
			_ = s.networkMgr.TeardownTAP(tapCfg)
			_ = s.networkMgr.Release(id)
			_ = s.imageMgr.DeleteDisk(id)
			_ = s.sshKeyMgr.Delete(id)
			return fmt.Errorf("start cloud-hypervisor: %w", err)
		}
		s.chProcesses[id] = cmd.Process

		// Step 8: Wait for socket
		for i := 0; i < 20; i++ {
			if _, err := os.Stat(socketPath); err == nil {
				break
			}
			if i == 19 {
				_ = cmd.Process.Kill()
				s.dhcpMgr.StopForVM(id)
				_ = s.networkMgr.TeardownTAP(tapCfg)
				_ = s.networkMgr.Release(id)
				_ = s.imageMgr.DeleteDisk(id)
				_ = s.sshKeyMgr.Delete(id)
				return fmt.Errorf("timeout waiting for socket %s", socketPath)
			}
			time.Sleep(300 * time.Millisecond)
		}

		// Step 9: Create VMM client and store it
		client := vmm.New(vmm.Config{
			Transport: vmm.TransportUnixSock,
			Address:   socketPath,
			Logger:    s.logger,
			Metrics:   s.metrics,
		})
		s.vmmClients[id] = client

		// Step 10: Build VM config and send to CH
		chConfig := &vmm.VmConfig{
			CPUs:   vm.Config.CPUs,
			Memory: vm.Config.Memory,
			Payload: &vmm.PayloadConfig{
				Kernel:  s.imageMgr.KernelPath(),
				Cmdline: "console=hvc0 root=/dev/vda1 rw rootwait",
			},
			Disks: []vmm.DiskConfig{
				{Path: diskPath, Readonly: false},
			},
			Net: []vmm.NetConfig{
				{
					Tap:  tapCfg.TAPName,
					IP:   tapCfg.VMIP,
					Mask: "255.255.255.252",
				},
			},
			Serial: &vmm.ConsoleConfig{
				Mode:   "Socket",
				Socket: serialSocket,
			},
			Console: &vmm.ConsoleConfig{
				Mode: "Off",
			},
		}

		if err := client.Create(ctx, chConfig); err != nil {
			_ = cmd.Process.Kill()
			s.dhcpMgr.StopForVM(id)
			_ = s.networkMgr.TeardownTAP(tapCfg)
			_ = s.networkMgr.Release(id)
			_ = s.imageMgr.DeleteDisk(id)
			_ = s.sshKeyMgr.Delete(id)
			return fmt.Errorf("create vm in vmm: %w", err)
		}

		// Step 11: Boot the VM
		if err := client.Boot(ctx); err != nil {
			_ = cmd.Process.Kill()
			s.dhcpMgr.StopForVM(id)
			_ = s.networkMgr.TeardownTAP(tapCfg)
			_ = s.networkMgr.Release(id)
			_ = s.imageMgr.DeleteDisk(id)
			_ = s.sshKeyMgr.Delete(id)
			return fmt.Errorf("boot vm: %w", err)
		}

		s.logger.Info("vm fully provisioned",
			"id", id,
			"tap", tapCfg.TAPName,
			"vm_ip", tapCfg.VMIP,
			"host_ip", tapCfg.HostIP,
		)
		return nil
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

		// Cleanup after shutdown
		if proc, ok := s.chProcesses[id]; ok {
			_ = proc.Kill()
			delete(s.chProcesses, id)
		}
		if _, ok := s.vmmClients[id]; ok {
			delete(s.vmmClients, id)
		}

		// Stop DHCP
		s.dhcpMgr.StopForVM(id)

		// Teardown network
		if tapCfg, ok := s.networkMgr.Get(id); ok {
			_ = s.networkMgr.TeardownTAP(tapCfg)
			_ = s.networkMgr.Release(id)
		}

		// Remove socket files
		socketPath := fmt.Sprintf("/var/run/ch-api/%s.sock", id)
		serialSocket := fmt.Sprintf("/var/run/ch-api/%s-serial.sock", id)
		_ = os.Remove(socketPath)
		_ = os.Remove(serialSocket)

		// Remove disk
		_ = s.imageMgr.DeleteDisk(id)

		// Remove SSH keys
		_ = s.sshKeyMgr.Delete(id)

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
