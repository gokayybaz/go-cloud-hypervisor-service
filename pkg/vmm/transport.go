package vmm

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/metrics"
)

// TransportType selects the underlying network transport.
type TransportType string

// TransportUnixSock selects the unixSock transport.
const (
	TransportHTTP     TransportType = "http"
	TransportUnixSock TransportType = "unix"
)

// Config holds client configuration.
type Config struct {
	// Transport selects how to reach the Cloud Hypervisor API.
	Transport TransportType

	// Address is either a host:port (for HTTP) or an absolute filesystem
	// path (for Unix socket).
	Address string

	// RequestTimeout is the per-request deadline.  Zero means no timeout.
	RequestTimeout time.Duration

	// RetryPolicy controls automatic retries.  Zero value disables retries.
	RetryPolicy RetryPolicy

	// Logger is an optional structured logger.  When provided every lifecycle
	// transition is logged with the trace_id extracted from the context.
	Logger logging.Logger

	// Metrics is an optional metrics registry for recording VMM round-trip
	// latency histograms.
	Metrics *metrics.Registry
}

// RetryPolicy configures exponential backoff retries.
type RetryPolicy struct {
	// MaxRetries is the maximum number of retry attempts.  Zero disables
	// retries.
	MaxRetries int

	// BaseDelay is the initial retry delay.
	BaseDelay time.Duration

	// MaxDelay caps the delay between retries.
	MaxDelay time.Duration

	// Multiplier is the factor by which the delay increases each attempt.
	Multiplier float64
}

// DefaultRetryPolicy returns a sensible retry configuration.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries: 3,
		BaseDelay:  250 * time.Millisecond,
		MaxDelay:   5 * time.Second,
		Multiplier: 2.0,
	}
}

// newHTTPClient builds an *http.Client with the configured transport and
// timeout.
func newHTTPClient(cfg Config) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if cfg.Transport == TransportUnixSock {
				dialer := net.Dialer{Timeout: cfg.RequestTimeout}
				return dialer.DialContext(ctx, "unix", cfg.Address)
			}
			dialer := net.Dialer{Timeout: cfg.RequestTimeout}
			return dialer.DialContext(ctx, network, addr)
		},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   cfg.RequestTimeout,
	}
}

// baseURL returns the scheme+host portion used for HTTP requests.  For Unix
// socket transport we still need a fake host so that net/http can build a
// valid URL; the custom dialer ignores it.
func baseURL(cfg Config) string {
	switch cfg.Transport {
	case TransportUnixSock:
		return "http://localhost"
	default:
		return "http://" + cfg.Address
	}
}