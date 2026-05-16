package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/org/ch-api/internal/service"
	"github.com/org/ch-api/pkg/api/problem"
	"github.com/org/ch-api/pkg/logging"
	"github.com/org/ch-api/pkg/metrics"
	"github.com/org/ch-api/pkg/resources"
	"github.com/org/ch-api/pkg/vmm"
)

// ---------------------------------------------------------------------------
// Resource DTOs
// ---------------------------------------------------------------------------

// PatchCPURequest is the JSON request body for patchCPU.
type PatchCPURequest struct {
	Count int `json:"count"`
}

// PatchMemoryRequest is the JSON request body for patchMemory.
type PatchMemoryRequest struct {
	SizeMB int `json:"size_mb"`
}

// CPUResponse is the JSON response for cPU.
type CPUResponse struct {
	Requested int `json:"requested"`
	Effective int `json:"effective"`
}

// MemoryResponse is the JSON response for memory.
type MemoryResponse struct {
	RequestedMB int `json:"requested_mb"`
	EffectiveMB int `json:"effective_mb"`
}

// ---------------------------------------------------------------------------
// Resource Handler
// ---------------------------------------------------------------------------

// ResourceHandler handles HTTP requests for resource operations.
type ResourceHandler struct {
	svc     *service.Service
	logger  logging.Logger
	metrics *metrics.Registry
}

// NewResourceHandler creates a new resourceHandler.
func NewResourceHandler(svc *service.Service, logger logging.Logger, mr *metrics.Registry) *ResourceHandler {
	return &ResourceHandler{svc: svc, logger: logger, metrics: mr}
}

func (h *ResourceHandler) recordError(typ string) {
	if h.metrics != nil {
		h.metrics.ErrorsTotal.WithLabelValues(typ).Inc()
	}
}

// Register registers routes on the provided router.
func (h *ResourceHandler) Register(r chi.Router) {
	r.Patch("/vms/{id}/cpu", h.PatchCPU)
	r.Patch("/vms/{id}/memory", h.PatchMemory)
}

// PatchCPU resizes the vCPU count of a VM.
// @Summary      Resize CPU
// @Description  Resizes the number of vCPUs for the virtual machine.
// @Tags         Resources
// @Accept       json
// @Produce      json
// @Param        id   path      string          true  "VM ID"
// @Param        body body      PatchCPURequest true  "CPU resize request"
// @Success      200  {object}  CPUResponse
// @Failure      400  {object}  problem.Detail  "Invalid request"
// @Failure      404  {object}  problem.Detail  "VM not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/cpu [patch]
func (h *ResourceHandler) PatchCPU(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "id")
	instance := r.URL.Path
	log := h.logger.WithContext(r.Context())

	var req PatchCPURequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.recordError("validation")
		problem.BadRequest(instance, "invalid JSON body").Write(w)
		return
	}
	if req.Count < 1 {
		h.recordError("validation")
		problem.BadRequest(instance, "count must be >= 1", problem.Field("count", "must be >= 1")).Write(w)
		return
	}

	vm, err := h.svc.GetVM(r.Context(), vmID)
	if err != nil {
		h.recordError("vm_not_found")
		problem.NotFound(instance, fmt.Sprintf("vm %q not found", vmID)).Write(w)
		return
	}

	limits := resources.DefaultLimits()
	effective := req.Count
	if req.Count < limits.MinCPUs {
		effective = limits.MinCPUs
		log.Warn("cpu clamped to minimum", "vm_id", vmID, "requested", req.Count, "effective", effective)
	}
	if req.Count > limits.MaxCPUs {
		effective = limits.MaxCPUs
		log.Warn("cpu clamped to maximum", "vm_id", vmID, "requested", req.Count, "effective", effective)
	}

	if vm.Config.CPUs == nil {
		vm.Config.CPUs = &vmm.CPUConfig{}
	}
	vm.Config.CPUs.BootVCPUs = effective
	if vm.Config.CPUs.MaxVCPUs < effective {
		vm.Config.CPUs.MaxVCPUs = effective
	}

	if err := h.svc.UpdateVM(r.Context(), vm); err != nil {
		log.Error("update vm failed", "err", err)
		h.recordError("internal")
		problem.InternalServerError(instance).Write(w)
		return
	}

	resp := CPUResponse{Requested: req.Count, Effective: effective}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// PatchMemory resizes the memory of a VM.
// @Summary      Resize Memory
// @Description  Resizes the memory allocated to the virtual machine.
// @Tags         Resources
// @Accept       json
// @Produce      json
// @Param        id   path      string             true  "VM ID"
// @Param        body body      PatchMemoryRequest true  "Memory resize request"
// @Success      200  {object}  MemoryResponse
// @Failure      400  {object}  problem.Detail  "Invalid request"
// @Failure      404  {object}  problem.Detail  "VM not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/memory [patch]
func (h *ResourceHandler) PatchMemory(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "id")
	instance := r.URL.Path
	log := h.logger.WithContext(r.Context())

	var req PatchMemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.recordError("validation")
		problem.BadRequest(instance, "invalid JSON body").Write(w)
		return
	}
	if req.SizeMB < 64 {
		h.recordError("validation")
		problem.BadRequest(instance, "size_mb must be >= 64", problem.Field("size_mb", "must be >= 64")).Write(w)
		return
	}

	vm, err := h.svc.GetVM(r.Context(), vmID)
	if err != nil {
		h.recordError("vm_not_found")
		problem.NotFound(instance, fmt.Sprintf("vm %q not found", vmID)).Write(w)
		return
	}

	limits := resources.DefaultLimits()
	effective := req.SizeMB
	if req.SizeMB < limits.MinMemoryMB {
		effective = limits.MinMemoryMB
		log.Warn("memory clamped to minimum", "vm_id", vmID, "requested", req.SizeMB, "effective", effective)
	}
	if req.SizeMB > limits.MaxMemoryMB {
		effective = limits.MaxMemoryMB
		log.Warn("memory clamped to maximum", "vm_id", vmID, "requested", req.SizeMB, "effective", effective)
	}
	if effective%limits.MemoryAlignMB != 0 {
		effective = (effective / limits.MemoryAlignMB) * limits.MemoryAlignMB
		log.Warn("memory clamped to alignment", "vm_id", vmID, "requested", req.SizeMB, "effective", effective)
	}

	if vm.Config.Memory == nil {
		vm.Config.Memory = &vmm.MemoryConfig{}
	}
	vm.Config.Memory.Size = int64(effective)

	if err := h.svc.UpdateVM(r.Context(), vm); err != nil {
		log.Error("update vm failed", "err", err)
		h.recordError("internal")
		problem.InternalServerError(instance).Write(w)
		return
	}

	resp := MemoryResponse{RequestedMB: req.SizeMB, EffectiveMB: effective}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
