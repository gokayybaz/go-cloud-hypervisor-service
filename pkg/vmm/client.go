package vmm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
)

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client communicates with the Cloud Hypervisor REST API.
type Client struct {
	cfg    Config
	client *http.Client
	base   string
	logger logging.Logger
}

// New creates a Client from cfg.
func New(cfg Config) *Client {
	if cfg.RetryPolicy.MaxRetries == 0 {
		cfg.RetryPolicy = DefaultRetryPolicy()
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = noopLogger{}
	}

	return &Client{
		cfg:    cfg,
		client: newHTTPClient(cfg),
		base:   baseURL(cfg),
		logger: cfg.Logger,
	}
}

// Close closes idle connections.
func (c *Client) Close() error {
	c.client.CloseIdleConnections()
	return nil
}

// String returns a human-readable description (no secrets).
func (c *Client) String() string {
	switch c.cfg.Transport {
	case TransportUnixSock:
		return fmt.Sprintf("vmm.Client(unix://%s)", c.cfg.Address)
	default:
		return fmt.Sprintf("vmm.Client(http://%s)", c.cfg.Address)
	}
}

// ---------------------------------------------------------------------------
// Logging helper
// ---------------------------------------------------------------------------

func (c *Client) log(ctx context.Context) logging.Logger {
	return c.logger.WithContext(ctx)
}

func (c *Client) logTransition(ctx context.Context, op, state string, args ...any) {
	log := c.log(ctx)
	a := append([]any{"op", op, "state", state}, args...)
	if state == "failed" {
		log.Error("vmm transition", a...)
		return
	}
	log.Info("vmm transition", a...)
}

// ---------------------------------------------------------------------------
// High-level operations
// ---------------------------------------------------------------------------

// Ping checks whether the VMM is alive.
func (c *Client) Ping(ctx context.Context) error {
	c.logTransition(ctx, "Ping", "starting")
	err := c.doJSON(ctx, "Ping", http.MethodGet, "/api/v1/vmm.ping", nil, nil)
	if err != nil {
		c.logTransition(ctx, "Ping", "failed", "err", err)
		return err
	}
	c.logTransition(ctx, "Ping", "succeeded")
	return nil
}

// Version returns the VMM version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	c.logTransition(ctx, "Version", "starting")
	var v struct {
		BuildVersion string `json:"build_version"`
	}
	if err := c.doJSON(ctx, "Version", http.MethodGet, "/api/v1/vmm.version", nil, &v); err != nil {
		c.logTransition(ctx, "Version", "failed", "err", err)
		return "", err
	}
	c.logTransition(ctx, "Version", "succeeded", "version", v.BuildVersion)
	return v.BuildVersion, nil
}

// VmInfo holds information about a running VM.
type VmInfo struct {
	Config VmConfig `json:"config"`
	State  string   `json:"state"`
	Memory int64    `json:"memory_actual_size,omitempty"`
	Cpus   int      `json:"cpus_actual_count,omitempty"`
}

// Info returns information about the running VM.
func (c *Client) Info(ctx context.Context) (*VmInfo, error) {
	c.logTransition(ctx, "Info", "starting")
	var info VmInfo
	if err := c.doJSON(ctx, "Info", http.MethodGet, "/api/v1/vm.info", nil, &info); err != nil {
		c.logTransition(ctx, "Info", "failed", "err", err)
		return nil, err
	}
	c.logTransition(ctx, "Info", "succeeded", "state", info.State)
	return &info, nil
}

// Create creates a VM from the given configuration.
func (c *Client) Create(ctx context.Context, cfg *VmConfig) error {
	c.logTransition(ctx, "Create", "starting", "vcpus", cfg.CPUs.BootVCPUs, "memory", cfg.Memory.Size)
	if err := c.doJSON(ctx, "Create", http.MethodPut, "/api/v1/vm.create", cfg, nil); err != nil {
		c.logTransition(ctx, "Create", "failed", "err", err)
		return err
	}
	c.logTransition(ctx, "Create", "succeeded")
	return nil
}

// Boot boots the created VM.
func (c *Client) Boot(ctx context.Context) error {
	c.logTransition(ctx, "Boot", "starting")
	if err := c.doJSON(ctx, "Boot", http.MethodPut, "/api/v1/vm.boot", nil, nil); err != nil {
		c.logTransition(ctx, "Boot", "failed", "err", err)
		return err
	}
	c.logTransition(ctx, "Boot", "succeeded")
	return nil
}

// Shutdown shuts down the VM gracefully.
func (c *Client) Shutdown(ctx context.Context) error {
	c.logTransition(ctx, "Shutdown", "starting")
	if err := c.doJSON(ctx, "Shutdown", http.MethodPut, "/api/v1/vm.shutdown", nil, nil); err != nil {
		c.logTransition(ctx, "Shutdown", "failed", "err", err)
		return err
	}
	c.logTransition(ctx, "Shutdown", "succeeded")
	return nil
}

// Reboot reboots the VM.
func (c *Client) Reboot(ctx context.Context) error {
	c.logTransition(ctx, "Reboot", "starting")
	if err := c.doJSON(ctx, "Reboot", http.MethodPut, "/api/v1/vm.reboot", nil, nil); err != nil {
		c.logTransition(ctx, "Reboot", "failed", "err", err)
		return err
	}
	c.logTransition(ctx, "Reboot", "succeeded")
	return nil
}

// Pause pauses the VM.
func (c *Client) Pause(ctx context.Context) error {
	c.logTransition(ctx, "Pause", "starting")
	if err := c.doJSON(ctx, "Pause", http.MethodPut, "/api/v1/vm.pause", nil, nil); err != nil {
		c.logTransition(ctx, "Pause", "failed", "err", err)
		return err
	}
	c.logTransition(ctx, "Pause", "succeeded")
	return nil
}

// Resume resumes a paused VM.
func (c *Client) Resume(ctx context.Context) error {
	c.logTransition(ctx, "Resume", "starting")
	if err := c.doJSON(ctx, "Resume", http.MethodPut, "/api/v1/vm.resume", nil, nil); err != nil {
		c.logTransition(ctx, "Resume", "failed", "err", err)
		return err
	}
	c.logTransition(ctx, "Resume", "succeeded")
	return nil
}

// Delete deletes the VM.
func (c *Client) Delete(ctx context.Context) error {
	c.logTransition(ctx, "Delete", "starting")
	if err := c.doJSON(ctx, "Delete", http.MethodDelete, "/api/v1/vm", nil, nil); err != nil {
		c.logTransition(ctx, "Delete", "failed", "err", err)
		return err
	}
	c.logTransition(ctx, "Delete", "succeeded")
	return nil
}

// ---------------------------------------------------------------------------
// Request helpers with retry
// ---------------------------------------------------------------------------

func (c *Client) doJSON(ctx context.Context, op, method, path string, body, dst any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return NewError(op, ErrCodeInvalidRequest, 0, "marshal request body", "", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bodyReader)
	if err != nil {
		return NewError(op, ErrCodeInvalidRequest, 0, "build request", "", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := c.doWithRetry(ctx, op, req)
	if c.cfg.Metrics != nil {
		c.cfg.Metrics.VMMRTT.WithLabelValues(op).Observe(time.Since(start).Seconds())
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	respBodyStr := string(respBody)

	if resp.StatusCode >= 400 {
		code := ErrCodeAPI
		if resp.StatusCode == http.StatusNotFound {
			code = ErrCodeNotFound
		}
		return NewError(op, code, resp.StatusCode,
			fmt.Sprintf("%s %s: %s", method, path, respBodyStr),
			respBodyStr, nil)
	}

	if dst != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, dst); err != nil {
			return NewError(op, ErrCodeInvalidRequest, 0, "decode response", respBodyStr, err)
		}
	}
	return nil
}

func (c *Client) doWithRetry(ctx context.Context, op string, req *http.Request) (*http.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= c.cfg.RetryPolicy.MaxRetries; attempt++ {
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			if !isRetryableError(err) {
				return nil, c.wrapError(op, lastErr, 0, "")
			}
			if attempt == c.cfg.RetryPolicy.MaxRetries {
				break
			}
			delay := backoffDelay(attempt, c.cfg.RetryPolicy)
			c.log(ctx).Debug("vmm retry", "op", op, "attempt", attempt+1, "delay", delay.String())
			select {
			case <-req.Context().Done():
				return nil, NewError(op, ErrCodeTimeout, 0, "request cancelled during retry", "", req.Context().Err())
			case <-time.After(delay):
			}
			continue
		}

		if !shouldRetryStatus(resp.StatusCode) {
			return resp, nil
		}

		// Retryable HTTP status — drain body to reuse connection.
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		respBodyStr := string(respBody)
		lastErr = NewError(op, ErrCodeAPI, resp.StatusCode,
			fmt.Sprintf("HTTP %d", resp.StatusCode), respBodyStr, nil)

		if attempt == c.cfg.RetryPolicy.MaxRetries {
			break
		}

		delay := backoffDelay(attempt, c.cfg.RetryPolicy)
		c.log(ctx).Debug("vmm retry", "op", op, "attempt", attempt+1, "delay", delay.String(), "status", resp.StatusCode)
		select {
		case <-req.Context().Done():
			return nil, NewError(op, ErrCodeTimeout, 0, "request cancelled during retry", "", req.Context().Err())
		case <-time.After(delay):
		}
	}

	return nil, NewError(op, ErrCodeRetryExhausted, 0,
		"all retry attempts exhausted", "", lastErr)
}

func (c *Client) wrapError(op string, err error, status int, body string) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return NewError(op, ErrCodeTimeout, 0, "request timed out", body, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return NewError(op, ErrCodeTimeout, 0, "network timeout", body, err)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return NewError(op, ErrCodeTimeout, 0, "URL timeout", body, err)
		}
		return NewError(op, ErrCodeConnection, 0, "connection failed", body, err)
	}
	if status >= 400 {
		code := ErrCodeAPI
		if status == http.StatusNotFound {
			code = ErrCodeNotFound
		}
		return NewError(op, code, status, "HTTP error", body, err)
	}
	return NewError(op, ErrCodeUnknown, status, "request failed", body, err)
}