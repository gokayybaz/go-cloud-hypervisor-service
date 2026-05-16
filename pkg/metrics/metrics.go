package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry owns all application Prometheus metrics and exposes a handler
// for scraping.  It is safe for concurrent use.
type Registry struct {
	Registry     *prometheus.Registry
	HTTPRequests *prometheus.CounterVec
	HTTPDuration *prometheus.HistogramVec
	VMActive     prometheus.Gauge
	ErrorsTotal  *prometheus.CounterVec
	VMMRTT       *prometheus.HistogramVec
}

// New creates a Registry with all metrics pre-registered.
func New() *Registry {
	r := prometheus.NewRegistry()

	httpRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "chapi",
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total number of HTTP requests received, labelled by method, path and status code.",
	}, []string{"method", "path", "status"})

	httpDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "chapi",
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request latency distribution in seconds, labelled by method and path.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "path"})

	vmActive := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "chapi",
		Subsystem: "vms",
		Name:      "active",
		Help:      "Current number of VMs stored in memory.",
	})

	errorsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "chapi",
		Subsystem: "api",
		Name:      "errors_total",
		Help:      "Total number of API errors, labelled by semantic type.",
	}, []string{"type"})

	vmmRTT := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "chapi",
		Subsystem: "vmm",
		Name:      "request_duration_seconds",
		Help:      "VMM socket round-trip latency in seconds, labelled by operation name.",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"op"})

	r.MustRegister(httpRequests, httpDuration, vmActive, errorsTotal, vmmRTT)

	return &Registry{
		Registry:     r,
		HTTPRequests: httpRequests,
		HTTPDuration: httpDuration,
		VMActive:     vmActive,
		ErrorsTotal:  errorsTotal,
		VMMRTT:       vmmRTT,
	}
}

// Handler returns an http.Handler that exposes the Prometheus metrics endpoint.
func (m *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

// Middleware returns an HTTP middleware that records request count and latency.
// It should be placed as close to the handler as possible so that it captures
// the final status code written by the response writer.
func (m *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		status := strconv.Itoa(ww.Status())
		m.HTTPRequests.WithLabelValues(r.Method, r.URL.Path, status).Inc()
		m.HTTPDuration.WithLabelValues(r.Method, r.URL.Path).Observe(time.Since(start).Seconds())
	})
}
