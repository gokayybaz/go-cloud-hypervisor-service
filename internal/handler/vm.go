package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/service"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/store"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api/problem"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/metrics"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/vmm"
)

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

// CreateVMRequest is the JSON body for POST /vms.
type CreateVMRequest struct {
	Name    string          `json:"name"`
	CPUs    *CPURequest     `json:"cpus"`
	Memory  *MemoryRequest  `json:"memory"`
	Kernel  *KernelRequest  `json:"kernel"`
        Payload *PayloadRequest `json:"payload"`
	Disks   []DiskRequest   `json:"disks"`
	Net     []NetRequest    `json:"net,omitempty"`
	Console *ConsoleRequest `json:"console,omitempty"`
	Serial  *ConsoleRequest `json:"serial,omitempty"`
}

// CPURequest is the JSON request body for cPU.
type CPURequest struct {
	BootVCPUs int `json:"boot_vcpus"`
	MaxVCPUs  int `json:"max_vcpus"`
}

// MemoryRequest is the JSON request body for memory.
type MemoryRequest struct {
	Size int64 `json:"size"`
}

// KernelRequest is the JSON request body for kernel.
type KernelRequest struct {
	Path string `json:"path"`
}

// DiskRequest is the JSON request body for disk.
type DiskRequest struct {
	Path     string `json:"path"`
	Readonly bool   `json:"readonly,omitempty"`
	Direct   bool   `json:"direct,omitempty"`
}

// NetRequest is the JSON request body for net.
type NetRequest struct {
	Tap string `json:"tap,omitempty"`
	IP  string `json:"ip,omitempty"`
	Mac string `json:"mac,omitempty"`
}

type PayloadRequest struct {
    Firmware  string `json:"firmware"`
    Kernel    string `json:"kernel"`
    Cmdline   string `json:"cmdline"`
    Initramfs string `json:"initramfs"`
}

// ConsoleRequest is the JSON request body for console.
type ConsoleRequest struct {
	Mode string `json:"mode,omitempty"`
	Path string `json:"path,omitempty"`
}

// VMResponse is the JSON representation of a VM.
type VMResponse struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Status    string       `json:"status"`
	CreatedAt string       `json:"created_at"`
	Config    vmm.VmConfig `json:"config"`
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// Validate validates the request and returns a list of field errors.
func (req *CreateVMRequest) Validate() []problem.FieldError {
	var errs []problem.FieldError

	if req.Name == "" {
		errs = append(errs, problem.Field("name", "name is required"))
	}

	if req.CPUs == nil {
		errs = append(errs, problem.Field("cpus", "cpus is required"))
	} else {
		if req.CPUs.BootVCPUs < 1 {
			errs = append(errs, problem.Field("cpus.boot_vcpus", "must be >= 1"))
		}
		if req.CPUs.MaxVCPUs < req.CPUs.BootVCPUs {
			errs = append(errs, problem.Field("cpus.max_vcpus", "must be >= boot_vcpus (%d)", req.CPUs.BootVCPUs))
		}
	}

	if req.Memory == nil {
		errs = append(errs, problem.Field("memory", "memory is required"))
	} else {
		if req.Memory.Size < 64 {
			errs = append(errs, problem.Field("memory.size", "must be >= 64 MB"))
		}
	}

	if req.Kernel == nil && req.Payload == nil {
    errs = append(errs, problem.Field("kernel", "kernel or payload is required"))
} else if req.Kernel != nil && req.Kernel.Path == "" {
    errs = append(errs, problem.Field("kernel.path", "path is required"))
} else if req.Payload != nil && req.Payload.Firmware == "" && req.Payload.Kernel == "" {
    errs = append(errs, problem.Field("payload", "firmware or kernel path is required"))
}


	if len(req.Disks) == 0 {
		errs = append(errs, problem.Field("disks", "at least one disk is required"))
	} else {
		for i, d := range req.Disks {
			if d.Path == "" {
				errs = append(errs, problem.Field(fmt.Sprintf("disks[%d].path", i), "path is required"))
			}
		}
	}

	for i, n := range req.Net {
		if n.Tap == "" && n.IP == "" && n.Mac == "" {
			errs = append(errs, problem.Field(fmt.Sprintf("net[%d]", i), "at least one of tap, ip, or mac is required"))
		}
	}

	return errs
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// VMHandler handles HTTP requests for vM operations.
type VMHandler struct {
	svc     *service.Service
	logger  logging.Logger
	metrics *metrics.Registry
}

// NewVMHandler creates a new vMHandler.
func NewVMHandler(svc *service.Service, logger logging.Logger, mr *metrics.Registry) *VMHandler {
	return &VMHandler{svc: svc, logger: logger, metrics: mr}
}

// Register registers routes on the provided router.
func (h *VMHandler) Register(r chi.Router) {
	r.Post("/vms", h.Create)
	r.Get("/vms", h.List)
	r.Get("/vms/{id}", h.Get)
	r.Delete("/vms/{id}", h.Delete)
}

// Create handles POST /vms.
func (h *VMHandler) recordError(typ string) {
	if h.metrics != nil {
		h.metrics.ErrorsTotal.WithLabelValues(typ).Inc()
	}
}

// Create creates a new VM.
// @Summary      Create VM
// @Description  Creates a new virtual machine with the given configuration.
// @Tags         VMs
// @Accept       json
// @Produce      json
// @Param        body  body      CreateVMRequest  true  "VM configuration"
// @Success      201   {object}  VMResponse
// @Failure      400   {object}  problem.Detail  "Invalid request"
// @Failure      500   {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms [post]
func (h *VMHandler) Create(w http.ResponseWriter, r *http.Request) {
	log := h.logger.WithContext(r.Context())
	instance := r.URL.Path

	var req CreateVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.recordError("validation")
		problem.BadRequest(instance, "invalid JSON body").Write(w)
		return
	}

	if errs := req.Validate(); len(errs) > 0 {
		h.recordError("validation")
		problem.BadRequest(instance, "request body validation failed", errs...).Write(w)
		return
	}

	svcReq := service.CreateVMRequest{
                Name: req.Name,
                Config: vmm.VmConfig{
                        CPUs: &vmm.CPUConfig{
                                BootVCPUs: req.CPUs.BootVCPUs,
                                MaxVCPUs:  req.CPUs.MaxVCPUs,
                        },
                        Memory: &vmm.MemoryConfig{
                                Size: req.Memory.Size,
                        },
                  
                        Disks:   dtoToDisks(req.Disks),
                        Net:     dtoToNet(req.Net),
                        Console: dtoToConsole(req.Console),
                        Serial:  dtoToConsole(req.Serial),
                },
        }

if req.Kernel != nil {
    svcReq.Config.Kernel = &vmm.KernelConfig{
        Path: req.Kernel.Path,
    }
}

if req.Payload != nil {
    svcReq.Config.Payload = &vmm.PayloadConfig{
        Firmware:  req.Payload.Firmware,
        Kernel:    req.Payload.Kernel,
        Cmdline:   req.Payload.Cmdline,
        Initramfs: req.Payload.Initramfs,
    }
}

	vm, err := h.svc.CreateVM(r.Context(), svcReq)
	if err != nil {
		log.Error("create vm failed", "err", err)
		h.recordError("internal")
		problem.InternalServerError(instance).Write(w)
		return
	}

	resp := vmToResponse(vm)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// List returns all VMs.
// @Summary      List VMs
// @Description  Returns a list of all virtual machines.
// @Tags         VMs
// @Produce      json
// @Success      200  {array}   VMResponse
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms [get]
func (h *VMHandler) List(w http.ResponseWriter, r *http.Request) {
	log := h.logger.WithContext(r.Context())
	instance := r.URL.Path

	vms, err := h.svc.ListVMs(r.Context())
	if err != nil {
		log.Error("list vms failed", "err", err)
		h.recordError("internal")
		problem.InternalServerError(instance).Write(w)
		return
	}

	resp := make([]VMResponse, len(vms))
	for i, vm := range vms {
		resp[i] = *vmToResponse(vm)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Get returns a single VM by ID.
// @Summary      Get VM
// @Description  Returns the virtual machine with the given ID.
// @Tags         VMs
// @Produce      json
// @Param        id   path      string  true  "VM ID"
// @Success      200  {object}  VMResponse
// @Failure      404  {object}  problem.Detail  "VM not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id} [get]
func (h *VMHandler) Get(w http.ResponseWriter, r *http.Request) {
	log := h.logger.WithContext(r.Context())
	id := chi.URLParam(r, "id")
	instance := r.URL.Path

	vm, err := h.svc.GetVM(r.Context(), id)
	if err != nil {
		log.Warn("get vm not found", "id", id)
		h.recordError("vm_not_found")
		problem.NotFound(instance, fmt.Sprintf("vm %q not found", id)).Write(w)
		return
	}

	resp := vmToResponse(vm)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Delete removes a VM by ID.
// @Summary      Delete VM
// @Description  Deletes the virtual machine with the given ID.
// @Tags         VMs
// @Param        id   path  string  true  "VM ID"
// @Success      204
// @Failure      404  {object}  problem.Detail  "VM not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id} [delete]
func (h *VMHandler) Delete(w http.ResponseWriter, r *http.Request) {
	log := h.logger.WithContext(r.Context())
	id := chi.URLParam(r, "id")
	instance := r.URL.Path

	if err := h.svc.DeleteVM(r.Context(), id); err != nil {
		log.Warn("delete vm not found", "id", id)
		h.recordError("vm_not_found")
		problem.NotFound(instance, fmt.Sprintf("vm %q not found", id)).Write(w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func dtoToDisks(reqs []DiskRequest) []vmm.DiskConfig {
	out := make([]vmm.DiskConfig, len(reqs))
	for i, d := range reqs {
		out[i] = vmm.DiskConfig{
			Path:     d.Path,
			Readonly: d.Readonly,
			Direct:   d.Direct,
		}
	}
	return out
}

func dtoToNet(reqs []NetRequest) []vmm.NetConfig {
	out := make([]vmm.NetConfig, len(reqs))
	for i, n := range reqs {
		out[i] = vmm.NetConfig{
			Tap: n.Tap,
			IP:  n.IP,
			Mac: n.Mac,
		}
	}
	return out
}

func dtoToConsole(req *ConsoleRequest) *vmm.ConsoleConfig {
	if req == nil {
		return nil
	}
	return &vmm.ConsoleConfig{
		Mode: req.Mode,
		Path: req.Path,
	}
}

func vmToResponse(vm *store.VM) *VMResponse {
	return &VMResponse{
		ID:        vm.ID,
		Name:      vm.Name,
		Status:    vm.Status,
		CreatedAt: vm.CreatedAt.Format(time.RFC3339Nano),
		Config:    vm.Config,
	}
}
