package handler

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/service"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api/problem"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/metrics"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development.
	},
}

// ---------------------------------------------------------------------------
// Console Handler
// ---------------------------------------------------------------------------

// ConsoleHandler handles HTTP requests for console operations.
type ConsoleHandler struct {
	svc     *service.Service
	logger  logging.Logger
	metrics *metrics.Registry
}

// NewConsoleHandler creates a new consoleHandler.
func NewConsoleHandler(svc *service.Service, logger logging.Logger, mr *metrics.Registry) *ConsoleHandler {
	return &ConsoleHandler{svc: svc, logger: logger, metrics: mr}
}

func (h *ConsoleHandler) recordError(typ string) {
	if h.metrics != nil {
		h.metrics.ErrorsTotal.WithLabelValues(typ).Inc()
	}
}

// Register registers routes on the provided router.
func (h *ConsoleHandler) Register(r chi.Router) {
	r.Get("/vms/{id}/console", h.Stream)
}

// Stream streams the VM console over WebSocket.
// @Summary      VM Console
// @Description  Streams the serial console output of the virtual machine over WebSocket.
// @Tags         Console
// @Param        id   path  string  true  "VM ID"
// @Success      101  "WebSocket upgrade"
// @Failure      404  {object}  problem.Detail  "VM not found"
// @Failure      503  {object}  problem.Detail  "Console unavailable"
// @Router       /api/v1/vms/{id}/console [get]
func (h *ConsoleHandler) Stream(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "id")
	instance := r.URL.Path
	log := h.logger.WithContext(r.Context())

	// Verify VM exists before upgrading.
	_, err := h.svc.GetVM(r.Context(), vmID)
	if err != nil {
		h.recordError("vm_not_found")
		problem.NotFound(instance, fmt.Sprintf("vm %q not found", vmID)).Write(w)
		return
	}

	serialPath := fmt.Sprintf("/var/run/ch-api/%s-serial.sock", vmID)
	serialConn, err := net.Dial("unix", serialPath)
	if err != nil {
		log.Warn("serial socket unavailable", "path", serialPath, "err", err)
		h.recordError("console_unavailable")
		http.Error(w, "console unavailable", http.StatusServiceUnavailable)
		return
	}
	defer serialConn.Close()

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("websocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	start := time.Now()
	clientAddr := r.RemoteAddr
	log.Info("console session started", "vm_id", vmID, "client", clientAddr)

	// Copy serial -> websocket in a background goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := serialConn.Read(buf)
			if err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				return
			}
		}
	}()

	// Copy websocket -> serial in the main goroutine.
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Warn("websocket read error", "err", err)
			}
			break
		}
		if _, err := serialConn.Write(data); err != nil {
			log.Warn("serial write error", "err", err)
			break
		}
	}

	<-done
	duration := time.Since(start)
	log.Info("console session ended", "vm_id", vmID, "client", clientAddr, "duration_ms", duration.Milliseconds())
}
