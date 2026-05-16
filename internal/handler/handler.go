package handler

import (
	"net/http"

	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/service"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/metrics"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// Healthz handles GET /healthz.
// @Summary      Health check
// @Description  Returns a simple health check response.
// @Tags         System
// @Success      200  {string}  string  "ok"
// @Router       /healthz [get]
func Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// Status handles GET /api/v1/status.
// @Summary      Service status
// @Description  Returns the current service status.
// @Tags         System
// @Success      200  {string}  string  "running"
// @Router       /api/v1/status [get]
func Status(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("running"))
}

// Register wires all HTTP handlers onto the provided router.
func Register(router *api.Router, svc *service.Service, logger logging.Logger, mr *metrics.Registry) {
	router.Root().Get("/healthz", Healthz)
	router.V1().Get("/status", Status)

	if mr != nil {
		router.Root().Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
			mr.Handler().ServeHTTP(w, r)
		})
	}

	// Swagger UI
	router.Root().Get("/api/docs/*", httpSwagger.Handler(
		httpSwagger.URL("/api/docs/doc.json"),
	))

	vmh := NewVMHandler(svc, logger, mr)
	vmh.Register(router.V1())

	lch := NewVMLifecycleHandler(svc, logger, mr)
	lch.Register(router.V1())

	dh := NewDiskHandler(svc, logger, mr)
	dh.Register(router.V1())

	nh := NewNetworkHandler(svc, logger, mr)
	nh.Register(router.V1())

	rh := NewResourceHandler(svc, logger, mr)
	rh.Register(router.V1())

	ch := NewConsoleHandler(svc, logger, mr)
	ch.Register(router.V1())
}