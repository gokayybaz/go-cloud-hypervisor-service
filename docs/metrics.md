# Metrics

The API exposes Prometheus-compatible metrics at `GET /metrics`.

## Quick Start

```bash
curl http://localhost:8080/metrics
```

## Metric Overview

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `chapi_http_requests_total` | Counter | `method`, `path`, `status` | Total HTTP requests received |
| `chapi_http_request_duration_seconds` | Histogram | `method`, `path` | HTTP request latency distribution |
| `chapi_vms_active` | Gauge | — | Current number of VMs in the store |
| `chapi_api_errors_total` | Counter | `type` | Total API errors by semantic type |
| `chapi_vmm_request_duration_seconds` | Histogram | `op` | VMM socket round-trip latency |

## Detailed Reference

### `chapi_http_requests_total`

**Type:** Counter

**Labels:**
- `method` — HTTP method (`GET`, `POST`, `DELETE`, `PATCH`)
- `path` — Request path (e.g. `/api/v1/vms`, `/healthz`)
- `status` — HTTP status code (e.g. `200`, `201`, `404`, `500`)

**Description:** Total number of HTTP requests received by the API server.

**Example queries:**
```promql
# Requests per second
rate(chapi_http_requests_total[5m])

# Error rate (5xx)
sum(rate(chapi_http_requests_total{status=~"5.."}[5m]))

# 404 rate for VM lookups
rate(chapi_http_requests_total{path="/api/v1/vms/{id}",status="404"}[5m])
```

---

### `chapi_http_request_duration_seconds`

**Type:** Histogram

**Labels:**
- `method` — HTTP method
- `path` — Request path

**Buckets:** `0.005`, `0.01`, `0.025`, `0.05`, `0.1`, `0.25`, `0.5`, `1`, `2.5`, `5`, `10` (seconds)

**Description:** Distribution of HTTP request processing time from the moment the request enters the metrics middleware until the response is fully written.

**Example queries:**
```promql
# 95th percentile latency per endpoint
histogram_quantile(0.95,
  sum(rate(chapi_http_request_duration_seconds_bucket[5m])) by (le, method, path)
)

# Average latency for VM creation
rate(chapi_http_request_duration_seconds_sum{method="POST",path="/api/v1/vms"}[5m])
/
rate(chapi_http_request_duration_seconds_count{method="POST",path="/api/v1/vms"}[5m])
```

---

### `chapi_vms_active`

**Type:** Gauge

**Labels:** None

**Description:** Current number of VMs stored in the in-memory VM store. Incremented on successful `Create` and decremented on successful `Delete`.

**Example queries:**
```promql
# Current VM count
chapi_vms_active

# VM count over time
chapi_vms_active[1h]
```

---

### `chapi_api_errors_total`

**Type:** Counter

**Labels:**
- `type` — Semantic error category:
  - `validation` — Request body or parameter validation failure (400)
  - `vm_not_found` — VM lookup returned not found (404)
  - `not_found` — Sub-resource (disk, interface) not found (404)
  - `internal` — Unexpected internal server error (500)
  - `vmm` — VMM lifecycle operation failure (500)

**Description:** Total number of API errors grouped by semantic type. This complements `chapi_http_requests_total{status=~"4..|5.."}` with operator-friendly labels.

**Example queries:**
```promql
# Validation errors per second
rate(chapi_api_errors_total{type="validation"}[5m])

# Total error rate by type
sum by (type) (rate(chapi_api_errors_total[5m]))
```

---

### `chapi_vmm_request_duration_seconds`

**Type:** Histogram

**Labels:**
- `op` — VMM operation name (`Ping`, `Version`, `Info`, `Create`, `Boot`, `Shutdown`, `Reboot`, `Pause`, `Resume`, `Delete`)

**Buckets:** `0.001`, `0.005`, `0.01`, `0.025`, `0.05`, `0.1`, `0.25`, `0.5`, `1`, `2.5`, `5`, `10` (seconds)

**Description:** Round-trip latency of every HTTP request made to the Cloud Hypervisor VMM socket, including retries. Measured from the start of `doJSON` until the response body is received.

**Example queries:**
```promql
# 99th percentile VMM latency per operation
histogram_quantile(0.99,
  sum(rate(chapi_vmm_request_duration_seconds_bucket[5m])) by (le, op)
)

# Average Boot latency
rate(chapi_vmm_request_duration_seconds_sum{op="Boot"}[5m])
/
rate(chapi_vmm_request_duration_seconds_count{op="Boot"}[5m])
```

## Middleware Ordering

The metrics middleware sits between `AccessLogger` and `Timeout` in the stack:

```
PanicRecovery → TraceID → Audit → AccessLogger → Metrics → Timeout → Handler
```

This placement ensures:
- `Metrics` sees the final status code written by the handler
- `Metrics` does not include audit-insert latency or access-log latency
- `Timeout` deadline is measured independently of metrics overhead

## Grafana Dashboard Snippets

### API Error Rate Panel
```promql
sum(rate(chapi_api_errors_total[5m])) by (type)
```

### VM Growth Panel
```promql
chapi_vms_active
```

### P95 API Latency Panel
```promql
histogram_quantile(0.95,
  sum(rate(chapi_http_request_duration_seconds_bucket[5m])) by (le, path)
)
```

### VMM Health Panel
```promql
histogram_quantile(0.99,
  sum(rate(chapi_vmm_request_duration_seconds_bucket[5m])) by (le, op)
)
```
