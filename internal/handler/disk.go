package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/service"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api/problem"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/image"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/metrics"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/storage"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/vmm"
)

// ---------------------------------------------------------------------------
// Disk DTOs
// ---------------------------------------------------------------------------

// AddDiskRequest is the JSON request body for addDisk.
type AddDiskRequest struct {
	Path     string `json:"path"`
	Readonly bool   `json:"readonly,omitempty"`
	Direct   bool   `json:"direct,omitempty"`
}

// Validate validates the request and returns a list of field errors.
func (r *AddDiskRequest) Validate() []problem.FieldError {
	var errs []problem.FieldError
	if r.Path == "" {
		errs = append(errs, problem.Field("path", "path is required"))
	}
	return errs
}

// DiskResponse is the JSON response for disk.
type DiskResponse struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Format   string `json:"format"`
	Readonly bool   `json:"readonly"`
	Direct   bool   `json:"direct"`
}

// ---------------------------------------------------------------------------
// Disk Handler
// ---------------------------------------------------------------------------

// DiskHandler handles HTTP requests for disk operations.
type DiskHandler struct {
	svc     *service.Service
	logger  logging.Logger
	metrics *metrics.Registry
}

// NewDiskHandler creates a new diskHandler.
func NewDiskHandler(svc *service.Service, logger logging.Logger, mr *metrics.Registry) *DiskHandler {
	return &DiskHandler{svc: svc, logger: logger, metrics: mr}
}

func (h *DiskHandler) recordError(typ string) {
	if h.metrics != nil {
		h.metrics.ErrorsTotal.WithLabelValues(typ).Inc()
	}
}

// Register registers routes on the provided router.
func (h *DiskHandler) Register(r chi.Router) {
	r.Post("/vms/{id}/disks", h.Add)
	r.Delete("/vms/{id}/disks/{disk_id}", h.Remove)
	r.Post("/vms/{id}/disks/{disk_id}/snapshot", h.Snapshot)
}

// Add attaches a disk to a VM.
// @Summary      Add Disk
// @Description  Attaches a new disk to the virtual machine with the given ID.
// @Tags         Disks
// @Accept       json
// @Produce      json
// @Param        id      path      string           true  "VM ID"
// @Param        body    body      AddDiskRequest   true  "Disk configuration"
// @Success      201     {object}  DiskResponse
// @Failure      400     {object}  problem.Detail  "Invalid request"
// @Failure      404     {object}  problem.Detail  "VM or disk not found"
// @Failure      500     {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/disks [post]
func (h *DiskHandler) Add(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "id")
	instance := r.URL.Path
	log := h.logger.WithContext(r.Context())

	var req AddDiskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.recordError("validation")
		problem.BadRequest(instance, "invalid JSON body").Write(w)
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		h.recordError("validation")
		problem.BadRequest(instance, "validation failed", errs...).Write(w)
		return
	}

	vm, err := h.svc.GetVM(r.Context(), vmID)
	if err != nil {
		h.recordError("vm_not_found")
		problem.NotFound(instance, fmt.Sprintf("vm %q not found", vmID)).Write(w)
		return
	}

	// Detect format from path.
	format := image.DetectFormat(req.Path)
	if !image.IsSupported(format) {
		h.recordError("validation")
		problem.BadRequest(instance, fmt.Sprintf("unsupported disk format %q", format)).Write(w)
		return
	}

	diskID := fmt.Sprintf("disk-%d", len(vm.Config.Disks))
	vm.Config.Disks = append(vm.Config.Disks, vmm.DiskConfig{
		Path:     req.Path,
		Readonly: req.Readonly,
		Direct:   req.Direct,
	})

	if err := h.svc.UpdateVM(r.Context(), vm); err != nil {
		log.Error("update vm failed", "err", err)
		h.recordError("internal")
		problem.InternalServerError(instance).Write(w)
		return
	}

	resp := DiskResponse{
		ID:       diskID,
		Path:     req.Path,
		Format:   string(format),
		Readonly: req.Readonly,
		Direct:   req.Direct,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// Remove detaches a disk from a VM.
// @Summary      Remove Disk
// @Description  Detaches the disk from the virtual machine with the given ID.
// @Tags         Disks
// @Produce      json
// @Param        id       path  string  true  "VM ID"
// @Param        disk_id  path  string  true  "Disk ID"
// @Success      200  {array}   DiskResponse
// @Failure      404  {object}  problem.Detail  "VM or disk not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/disks/{disk_id} [delete]
func (h *DiskHandler) Remove(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "id")
	diskIdxStr := chi.URLParam(r, "disk_id")
	instance := r.URL.Path
	log := h.logger.WithContext(r.Context())

	vm, err := h.svc.GetVM(r.Context(), vmID)
	if err != nil {
		h.recordError("vm_not_found")
		problem.NotFound(instance, fmt.Sprintf("vm %q not found", vmID)).Write(w)
		return
	}

	// disk_id is an index like "disk-0" or just "0".
	idx := -1
	if _, err := fmt.Sscanf(diskIdxStr, "disk-%d", &idx); err != nil {
		fmt.Sscanf(diskIdxStr, "%d", &idx)
	}
	if idx < 0 || idx >= len(vm.Config.Disks) {
		h.recordError("not_found")
		problem.NotFound(instance, fmt.Sprintf("disk %q not found", diskIdxStr)).Write(w)
		return
	}

	vm.Config.Disks = append(vm.Config.Disks[:idx], vm.Config.Disks[idx+1:]...)
	if err := h.svc.UpdateVM(r.Context(), vm); err != nil {
		log.Error("update vm failed", "err", err)
		h.recordError("internal")
		problem.InternalServerError(instance).Write(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.diskList(&vm.Config))
}

// Snapshot creates a snapshot of a disk.
// @Summary      Snapshot Disk
// @Description  Creates a snapshot of the disk attached to the virtual machine.
// @Tags         Disks
// @Produce      json
// @Param        id       path  string  true  "VM ID"
// @Param        disk_id  path  string  true  "Disk ID"
// @Success      200  {array}   DiskResponse
// @Failure      404  {object}  problem.Detail  "VM or disk not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/disks/{disk_id}/snapshot [post]
func (h *DiskHandler) Snapshot(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "id")
	diskIdxStr := chi.URLParam(r, "disk_id")
	instance := r.URL.Path
	log := h.logger.WithContext(r.Context())

	vm, err := h.svc.GetVM(r.Context(), vmID)
	if err != nil {
		h.recordError("vm_not_found")
		problem.NotFound(instance, fmt.Sprintf("vm %q not found", vmID)).Write(w)
		return
	}

	idx := -1
	if _, err := fmt.Sscanf(diskIdxStr, "disk-%d", &idx); err != nil {
		fmt.Sscanf(diskIdxStr, "%d", &idx)
	}
	if idx < 0 || idx >= len(vm.Config.Disks) {
		h.recordError("not_found")
		problem.NotFound(instance, fmt.Sprintf("disk %q not found", diskIdxStr)).Write(w)
		return
	}

	disk := vm.Config.Disks[idx]
	snapPath := filepath.Join(filepath.Dir(disk.Path), fmt.Sprintf("snapshot-%s-%s", vmID, diskIdxStr))
	if err := storage.CopyFile(disk.Path, snapPath); err != nil {
		log.Error("snapshot copy failed", "err", err)
		h.recordError("internal")
		problem.InternalServerError(instance).Write(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.diskList(&vm.Config))
}

func (h *DiskHandler) diskList(cfg *vmm.VmConfig) []DiskResponse {
	out := make([]DiskResponse, len(cfg.Disks))
	for i, d := range cfg.Disks {
		out[i] = DiskResponse{
			ID:       fmt.Sprintf("disk-%d", i),
			Path:     d.Path,
			Format:   string(image.DetectFormat(d.Path)),
			Readonly: d.Readonly,
			Direct:   d.Direct,
		}
	}
	return out
}
