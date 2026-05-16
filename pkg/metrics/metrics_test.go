package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestMiddlewareRecordsRequestAndDuration(t *testing.T) {
	mr := New()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	})

	ts := httptest.NewServer(mr.Middleware(handler))
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify HTTP request counter was incremented.
	d := &dto.Metric{}
	if err := mr.HTTPRequests.WithLabelValues("POST", "/api/v1/vms", "201").(prometheus.Counter).Write(d); err != nil {
		t.Fatalf("write counter dto: %v", err)
	}
	if d.Counter.GetValue() != 1 {
		t.Fatalf("expected counter 1, got %v", d.Counter.GetValue())
	}

	// Verify duration histogram has at least one observation.
	d = &dto.Metric{}
	if err := mr.HTTPDuration.WithLabelValues("POST", "/api/v1/vms").(prometheus.Histogram).Write(d); err != nil {
		t.Fatalf("write histogram dto: %v", err)
	}
	if d.Histogram.GetSampleCount() != 1 {
		t.Fatalf("expected histogram count 1, got %v", d.Histogram.GetSampleCount())
	}
}

func TestVMActiveGauge(t *testing.T) {
	mr := New()

	mr.VMActive.Set(5)
	d := &dto.Metric{}
	if err := mr.VMActive.Write(d); err != nil {
		t.Fatalf("write gauge dto: %v", err)
	}
	if d.Gauge.GetValue() != 5 {
		t.Fatalf("expected gauge 5, got %v", d.Gauge.GetValue())
	}
}

func TestErrorsTotalCounter(t *testing.T) {
	mr := New()

	mr.ErrorsTotal.WithLabelValues("validation").Inc()
	mr.ErrorsTotal.WithLabelValues("validation").Inc()
	mr.ErrorsTotal.WithLabelValues("internal").Inc()

	d := &dto.Metric{}
	if err := mr.ErrorsTotal.WithLabelValues("validation").(prometheus.Counter).Write(d); err != nil {
		t.Fatalf("write counter dto: %v", err)
	}
	if d.Counter.GetValue() != 2 {
		t.Fatalf("expected validation counter 2, got %v", d.Counter.GetValue())
	}
}

func TestVMMRTTHistogram(t *testing.T) {
	mr := New()

	mr.VMMRTT.WithLabelValues("Boot").Observe(0.042)

	d := &dto.Metric{}
	if err := mr.VMMRTT.WithLabelValues("Boot").(prometheus.Histogram).Write(d); err != nil {
		t.Fatalf("write histogram dto: %v", err)
	}
	if d.Histogram.GetSampleCount() != 1 {
		t.Fatalf("expected histogram count 1, got %v", d.Histogram.GetSampleCount())
	}
}

func TestMetricsHandler(t *testing.T) {
	mr := New()
	mr.HTTPRequests.WithLabelValues("GET", "/healthz", "200").Inc()

	ts := httptest.NewServer(mr.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected text/plain content-type, got %q", ct)
	}
}

func TestMiddlewareCapturesStatusCode(t *testing.T) {
	mr := New()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	})

	ts := httptest.NewServer(mr.Middleware(handler))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	d := &dto.Metric{}
	if err := mr.HTTPRequests.WithLabelValues("GET", "/test", "400").(prometheus.Counter).Write(d); err != nil {
		t.Fatalf("write counter dto: %v", err)
	}
	if d.Counter.GetValue() != 1 {
		t.Fatalf("expected counter 1 for 400, got %v", d.Counter.GetValue())
	}
}

func TestMiddlewareMeasuresDuration(t *testing.T) {
	mr := New()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mr.Middleware(handler))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	d := &dto.Metric{}
	if err := mr.HTTPDuration.WithLabelValues("GET", "/").(prometheus.Histogram).Write(d); err != nil {
		t.Fatalf("write histogram dto: %v", err)
	}
	if d.Histogram.GetSampleCount() != 1 {
		t.Fatalf("expected histogram count 1, got %v", d.Histogram.GetSampleCount())
	}
	// Verify the observation is at least 10ms.
	if d.Histogram.GetSampleSum() < 0.01 {
		t.Fatalf("expected sum >= 0.01, got %v", d.Histogram.GetSampleSum())
	}
}

func TestRegistryCollectsAllMetrics(t *testing.T) {
	mr := New()
	// Prime each metric so it appears in Gather.
	mr.HTTPRequests.WithLabelValues("GET", "/", "200").Inc()
	mr.HTTPDuration.WithLabelValues("GET", "/").Observe(0.001)
	mr.VMActive.Set(1)
	mr.ErrorsTotal.WithLabelValues("internal").Inc()
	mr.VMMRTT.WithLabelValues("Ping").Observe(0.005)

	families, err := mr.Registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	expected := map[string]bool{
		"chapi_http_requests_total":             false,
		"chapi_http_request_duration_seconds":   false,
		"chapi_vms_active":                      false,
		"chapi_api_errors_total":                false,
		"chapi_vmm_request_duration_seconds":    false,
	}
	for _, f := range families {
		if _, ok := expected[f.GetName()]; ok {
			expected[f.GetName()] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Fatalf("expected metric %q not found in registry", name)
		}
	}
}
