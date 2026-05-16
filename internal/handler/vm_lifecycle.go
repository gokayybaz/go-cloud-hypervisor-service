package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/org/ch-api/internal/service"
	"github.com/org/ch-api/pkg/api/problem"
	"github.com/org/ch-api/pkg/logging"
	"github.com/org/ch-api/pkg/metrics"
)

// ---------------------------------------------------------------------------
// VM Lifecycle Handler
// ---------------------------------------------------------------------------

// VMLifecycleHandler handles HTTP requests for vMLifecycle operations.
type VMLifecycleHandler struct {
	svc     *service.Service
	logger  logging.Logger
	metrics *metrics.Registry
}

// NewVMLifecycleHandler creates a new vMLifecycleHandler.
func NewVMLifecycleHandler(svc *service.Service, logger logging.Logger, mr *metrics.Registry) *VMLifecycleHandler {
	return &VMLifecycleHandler{svc: svc, logger: logger, metrics: mr}
}

func (h *VMLifecycleHandler) recordError(typ string) {
	if h.metrics != nil {
		h.metrics.ErrorsTotal.WithLabelValues(typ).Inc()
	}
}

// Register registers routes on the provided router.
func (h *VMLifecycleHandler) Register(r chi.Router) {
	r.Post("/vms/{id}/boot", h.Boot)
	r.Post("/vms/{id}/pause", h.Pause)
	r.Post("/vms/{id}/resume", h.Resume)
	r.Post("/vms/{id}/shutdown", h.Shutdown)
	r.Post("/vms/{id}/reboot", h.Reboot)
}

// userIdentity extracts the caller identity from the request.
// It reads the X-User-ID header and falls back to "anonymous".
func userIdentity(r *http.Request) string {
	user := r.Header.Get("X-User-ID")
	if user == "" {
		user = "anonymous"
	}
	return user
}

// Boot boots a VM.
// @Summary      Boot VM
// @Description  Boots the virtual machine with the given ID.
// @Tags         VM Lifecycle
// @Param        id   path  string  true  "VM ID"
// @Success      204
// @Failure      404  {object}  problem.Detail  "VM not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/boot [post]
func (h *VMLifecycleHandler) Boot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := userIdentity(r)
	if err := h.svc.BootVM(r.Context(), id, user); err != nil {
		h.handleError(w, r, id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Pause pauses a running VM.
// @Summary      Pause VM
// @Description  Pauses the virtual machine with the given ID.
// @Tags         VM Lifecycle
// @Param        id   path  string  true  "VM ID"
// @Success      204
// @Failure      404  {object}  problem.Detail  "VM not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/pause [post]
func (h *VMLifecycleHandler) Pause(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := userIdentity(r)
	if err := h.svc.PauseVM(r.Context(), id, user); err != nil {
		h.handleError(w, r, id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Resume resumes a paused VM.
// @Summary      Resume VM
// @Description  Resumes the paused virtual machine with the given ID.
// @Tags         VM Lifecycle
// @Param        id   path  string  true  "VM ID"
// @Success      204
// @Failure      404  {object}  problem.Detail  "VM not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/resume [post]
func (h *VMLifecycleHandler) Resume(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := userIdentity(r)
	if err := h.svc.ResumeVM(r.Context(), id, user); err != nil {
		h.handleError(w, r, id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Shutdown shuts down a VM.
// @Summary      Shutdown VM
// @Description  Shuts down the virtual machine with the given ID.
// @Tags         VM Lifecycle
// @Param        id   path  string  true  "VM ID"
// @Success      204
// @Failure      404  {object}  problem.Detail  "VM not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/shutdown [post]
func (h *VMLifecycleHandler) Shutdown(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := userIdentity(r)
	if err := h.svc.ShutdownVM(r.Context(), id, user); err != nil {
		h.handleError(w, r, id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Reboot reboots a VM.
// @Summary      Reboot VM
// @Description  Reboots the virtual machine with the given ID.
// @Tags         VM Lifecycle
// @Param        id   path  string  true  "VM ID"
// @Success      204
// @Failure      404  {object}  problem.Detail  "VM not found"
// @Failure      500  {object}  problem.Detail  "Internal server error"
// @Router       /api/v1/vms/{id}/reboot [post]
func (h *VMLifecycleHandler) Reboot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := userIdentity(r)
	if err := h.svc.RebootVM(r.Context(), id, user); err != nil {
		h.handleError(w, r, id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleError maps service errors to RFC 7807 problem details.
func (h *VMLifecycleHandler) handleError(w http.ResponseWriter, r *http.Request, id string, err error) {
	log := h.logger.WithContext(r.Context())
	instance := r.URL.Path

	// Not-found errors from the store.
	if err != nil && (err.Error() == "boot vm: vm \""+id+"\" not found" ||
		containsSubstring(err.Error(), "not found")) {
		log.Warn("lifecycle vm not found", "id", id, "err", err)
		h.recordError("vm_not_found")
		problem.NotFound(instance, err.Error()).Write(w)
		return
	}

	log.Error("lifecycle operation failed", "id", id, "err", err)
	h.recordError("vmm")
	problem.InternalServerError(instance).Write(w)
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
