package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/org/ch-api/internal/service"
	"github.com/org/ch-api/pkg/api/problem"
	"github.com/org/ch-api/pkg/logging"
	"github.com/org/ch-api/pkg/metrics"
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

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("websocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	start := time.Now()
	clientAddr := r.RemoteAddr
	log.Info("console session started", "vm_id", vmID, "client", clientAddr)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Watch for client disconnect.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Warn("websocket read error", "err", err)
				}
				cancel()
				return
			}
		}
	}()

	// Stream simulated console output.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	msgCount := 0
	for {
		select {
		case <-ctx.Done():
			goto cleanup
		case <-done:
			goto cleanup
		case <-ticker.C:
			msgCount++
			line := fmt.Sprintf("[%s] vm=%s console line %d\n", time.Now().Format(time.RFC3339), vmID, msgCount)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
				log.Warn("websocket write error", "err", err)
				goto cleanup
			}
		}
	}

cleanup:
	duration := time.Since(start)
	log.Info("console session ended", "vm_id", vmID, "client", clientAddr, "duration_ms", duration.Milliseconds(), "lines_sent", msgCount)
}
