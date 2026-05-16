package handler

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/service"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api/problem"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/metrics"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/vmm"
)

// ---------------------------------------------------------------------------
// Network DTOs
// ---------------------------------------------------------------------------

// AddInterfaceRequest is the JSON request body for addInterface.
type AddInterfaceRequest struct {
	Tap string `json:"tap,omitempty"`
	IP  string `json:"ip,omitempty"`
	Mac string `json:"mac,omitempty"`
	Mask string `json:"mask,omitempty"`
}

// Validate validates the request and returns a list of field errors.
func (r *AddInterfaceRequest) Validate() []problem.FieldError {
	var errs []problem.FieldError
	if r.Tap == "" && r.IP == "" && r.Mac == "" {
		errs = append(errs, problem.Field("interface", "at least one of tap, ip, or mac is required"))
	}
	if r.Mac != "" {
		if _, err := net.ParseMAC(r.Mac); err != nil {
			errs = append(errs, problem.Field("mac", "invalid MAC address: %v", err))
		}
	}
	if r.IP != "" {
		if net.ParseIP(r.IP) == nil {
			// Try CIDR.
			if _, _, err := net.ParseCIDR(r.IP); err != nil {
				errs = append(errs, problem.Field("ip", "invalid IP or CIDR: %v", err))
			}
		}
	}
	return errs
}

// PatchInterfaceRequest is the JSON request body for patchInterface.
type PatchInterfaceRequest struct {
	Tap  string `json:"tap,omitempty"`
	IP   string `json:"ip,omitempty"`
	Mac  string `json:"mac,omitempty"`
	Mask string `json:"mask,omitempty"`
}

// Validate validates the request and returns a list of field errors.
func (r *PatchInterfaceRequest) Validate() []problem.FieldError {
	var errs []problem.FieldError
	if r.Mac != "" {
		if _, err := net.ParseMAC(r.Mac); err != nil {
			errs = append(errs, problem.Field("mac", "invalid MAC address: %v", err))
		}
	}
	if r.IP != "" {
		if net.ParseIP(r.IP) == nil {
			if _, _, err := net.ParseCIDR(r.IP); err != nil {
				errs = append(errs, problem.Field("ip", "invalid IP or CIDR: %v", err))
			}
		}
	}
	return errs
}

// InterfaceResponse is the JSON response for interface.
type InterfaceResponse struct {
	ID   string `json:"id"`
	Tap  string `json:"tap,omitempty"`
	IP   string `json:"ip,omitempty"`
	Mac  string `json:"mac,omitempty"`
	Mask string `json:"mask,omitempty"`
}

// ---------------------------------------------------------------------------
// Network Handler
// ---------------------------------------------------------------------------

// NetworkHandler handles HTTP requests for network operations.
type NetworkHandler struct {
	svc     *service.Service
	logger  logging.Logger
	metrics *metrics.Registry
}

// NewNetworkHandler creates a new networkHandler.
func NewNetworkHandler(svc *service.Service, logger logging.Logger, mr *metrics.Registry) *NetworkHandler {
	return &NetworkHandler{svc: svc, logger: logger, metrics: mr}
}

func (h *NetworkHandler) recordError(typ string) {
	if h.metrics != nil {
		h.metrics.ErrorsTotal.WithLabelValues(typ).Inc()
	}
}

// Register registers routes on the provided router.
func (h *NetworkHandler) Register(r chi.Router) {
	r.Post("/vms/{id}/interfaces", h.Add)
	r.Delete("/vms/{id}/interfaces/{iface_id}", h.Remove)
	r.Patch("/vms/{id}/interfaces/{iface_id}", h.Patch)
}

// Add attaches a network interface to a VM.
// @Summary      Add Interface
// @Description  Attaches a new network interface to the virtual machine.
// @Tags         Network
// @Accept       json
// @Produce      json
// @Param        id    path      string                true  "VM ID"
// @Param        body  body      AddInterfaceRequest   true  "Interface configuration"
// @Success      201   {object}  InterfaceResponse
// @Failure      400   {object}  problem.Detail  "Invalid request"
// @Failure      404   {object}  problem.Detail  "VM not found"
// @Failure      500   {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/interfaces [post]
func (h *NetworkHandler) Add(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "id")
	instance := r.URL.Path
	log := h.logger.WithContext(r.Context())

	var req AddInterfaceRequest
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

	ifaceID := fmt.Sprintf("eth%d", len(vm.Config.Net))
	vm.Config.Net = append(vm.Config.Net, vmm.NetConfig{
		Tap:  req.Tap,
		IP:   req.IP,
		Mac:  req.Mac,
		Mask: req.Mask,
		Id:   ifaceID,
	})

	if err := h.svc.UpdateVM(r.Context(), vm); err != nil {
		log.Error("update vm failed", "err", err)
		h.recordError("internal")
		problem.InternalServerError(instance).Write(w)
		return
	}

	resp := InterfaceResponse{
		ID:   ifaceID,
		Tap:  req.Tap,
		IP:   req.IP,
		Mac:  req.Mac,
		Mask: req.Mask,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// Remove detaches a network interface from a VM.
// @Summary      Remove Interface
// @Description  Detaches the network interface from the virtual machine.
// @Tags         Network
// @Produce      json
// @Param        id       path  string  true  "VM ID"
// @Param        iface_id path  string  true  "Interface ID"
// @Success      200  {array}   InterfaceResponse
// @Failure      404  {object}  problem.Detail  "VM or interface not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/interfaces/{iface_id} [delete]
func (h *NetworkHandler) Remove(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "id")
	ifaceID := chi.URLParam(r, "iface_id")
	instance := r.URL.Path
	log := h.logger.WithContext(r.Context())

	vm, err := h.svc.GetVM(r.Context(), vmID)
	if err != nil {
		h.recordError("vm_not_found")
		problem.NotFound(instance, fmt.Sprintf("vm %q not found", vmID)).Write(w)
		return
	}

	found := -1
	for i, n := range vm.Config.Net {
		if n.Id == ifaceID {
			found = i
			break
		}
	}
	if found < 0 {
		h.recordError("not_found")
		problem.NotFound(instance, fmt.Sprintf("interface %q not found", ifaceID)).Write(w)
		return
	}

	vm.Config.Net = append(vm.Config.Net[:found], vm.Config.Net[found+1:]...)
	if err := h.svc.UpdateVM(r.Context(), vm); err != nil {
		log.Error("update vm failed", "err", err)
		h.recordError("internal")
		problem.InternalServerError(instance).Write(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.ifaceList(&vm.Config))
}

// Patch updates a network interface on a VM.
// @Summary      Patch Interface
// @Description  Updates fields of an existing network interface on the virtual machine.
// @Tags         Network
// @Accept       json
// @Produce      json
// @Param        id       path      string                 true  "VM ID"
// @Param        iface_id path      string                 true  "Interface ID"
// @Param        body     body      PatchInterfaceRequest  true  "Interface update"
// @Success      200  {array}   InterfaceResponse
// @Failure      400  {object}  problem.Detail  "Invalid request"
// @Failure      404  {object}  problem.Detail  "VM or interface not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/interfaces/{iface_id} [patch]
func (h *NetworkHandler) Patch(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "id")
	ifaceID := chi.URLParam(r, "iface_id")
	instance := r.URL.Path
	log := h.logger.WithContext(r.Context())

	var req PatchInterfaceRequest
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

	found := -1
	for i, n := range vm.Config.Net {
		if n.Id == ifaceID {
			found = i
			break
		}
	}
	if found < 0 {
		h.recordError("not_found")
		problem.NotFound(instance, fmt.Sprintf("interface %q not found", ifaceID)).Write(w)
		return
	}

	if req.Tap != "" {
		vm.Config.Net[found].Tap = req.Tap
	}
	if req.IP != "" {
		vm.Config.Net[found].IP = req.IP
	}
	if req.Mac != "" {
		vm.Config.Net[found].Mac = req.Mac
	}
	if req.Mask != "" {
		vm.Config.Net[found].Mask = req.Mask
	}

	if err := h.svc.UpdateVM(r.Context(), vm); err != nil {
		log.Error("update vm failed", "err", err)
		h.recordError("internal")
		problem.InternalServerError(instance).Write(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.ifaceList(&vm.Config))
}

func (h *NetworkHandler) ifaceList(cfg *vmm.VmConfig) []InterfaceResponse {
	out := make([]InterfaceResponse, len(cfg.Net))
	for i, n := range cfg.Net {
		out[i] = InterfaceResponse{
			ID:   n.Id,
			Tap:  n.Tap,
			IP:   n.IP,
			Mac:  n.Mac,
			Mask: n.Mask,
		}
	}
	return out
}
