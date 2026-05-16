# Rate Limiting

The API implements sliding-window rate limiting on a per-IP and per-token
basis.  When a limit is exceeded the server returns `429 Too Many Requests`
with a `Retry-After` header indicating how many seconds the client must wait
before retrying.

## Algorithm

The limiter uses a **sliding window counter** algorithm.  Time is divided into
fixed windows of configurable size (default 60 seconds).  For each client key
(IP address or token subject) the server stores:

* `prevCount` — requests in the previous window
* `currCount` — requests in the current window
* `currWindow` — index of the current window

When a request arrives the server computes a weighted estimate:

```
weight    = elapsed_time_in_window / window_size
estimated = prevCount * (1 - weight) + currCount
```

If `estimated + 1 > limit` the request is rejected.  Otherwise `currCount` is
incremented and the request proceeds.

This approach is memory-efficient (two integers per key) and provides a
smoother transition between windows than a naive fixed-window counter.

## Dual-Key Enforcement

Every request is evaluated against **two independent limits**:

1. **Per-IP limit** — applied to all requests regardless of authentication.
   The client IP is extracted from `X-Forwarded-For`, `X-Real-Ip`, or
   `RemoteAddr` (in that order).
2. **Per-token limit** — applied only when the request carries a valid JWT.
   The limit is selected from the caller's **highest** role.

A request must pass **both** limits to proceed.  If either limit is exceeded
the request is rejected with `429`.

## Configuration

Rate limiting is enabled by default.  Tune it via configuration:

```yaml
rate_limit:
  enabled: true
  window: "1m"                # sliding window size
  ip_limit: 100               # requests per window per IP
  viewer_limit: 100           # requests per window per viewer token
  operator_limit: 200         # requests per window per operator token
  admin_limit: 500            # requests per window per admin token
```

Or via environment variables:

```bash
export CH_API_RATE_LIMIT_ENABLED=true
export CH_API_RATE_LIMIT_WINDOW=1m
export CH_API_RATE_LIMIT_IP_LIMIT=100
export CH_API_RATE_LIMIT_VIEWER_LIMIT=100
export CH_API_RATE_LIMIT_OPERATOR_LIMIT=200
export CH_API_RATE_LIMIT_ADMIN_LIMIT=500
```

### Disabling Rate Limiting

Set `enabled: false` (or `CH_API_RATE_LIMIT_ENABLED=false`) to bypass all
rate checks.  This is useful during load testing or when the API sits behind
an external rate-limiting proxy.

### Setting a Limit to Unlimited

Set any limit to `0` to disable that specific limit while keeping others
active.  For example, to rate-limit only by IP and not by token:

```yaml
rate_limit:
  ip_limit: 100
  viewer_limit: 0
  operator_limit: 0
  admin_limit: 0
```

## Default Limits

| Limit | Default | Typical Use Case |
|-------|---------|------------------|
| `ip_limit` | 100 req/min | Prevent brute-force or DoS from a single IP. |
| `viewer_limit` | 100 req/min | Read-only clients listing or querying VMs. |
| `operator_limit` | 200 req/min | Clients managing VM lifecycle and resources. |
| `admin_limit` | 500 req/min | Administrative clients creating/deleting VMs. |

## Error Response

When a limit is exceeded the API returns `429 Too Many Requests`:

```json
{
  "type": "https://github.com/org/ch-api/errors/rate-limit-exceeded",
  "title": "Too Many Requests",
  "status": 429,
  "detail": "rate limit exceeded for ip; retry after 42 seconds",
  "instance": "/api/v1/vms"
}
```

**Headers:**

```
Retry-After: 42
Content-Type: application/problem+json
```

The `Retry-After` value is the number of seconds until the current window
expires.  Clients should wait at least this long before retrying.

## Logging and Auditing

Every rate-limit denial is logged at **WARN** level with structured fields:

```json
{
  "level": "warn",
  "msg": "rate limit exceeded",
  "type": "ip",
  "key": "192.168.1.1",
  "retry_after_sec": 42,
  "method": "GET",
  "path": "/api/v1/vms",
  "trace_id": "abc-123"
}
```

Because the rate-limit middleware runs **before** the audit middleware,
denied requests are still recorded in the audit trail with status code `429`.

## Middleware Position

The rate-limit middleware sits early in the root router stack:

```
PanicRecovery → TraceID → RateLimit → Audit → AccessLogger → Metrics → Timeout
```

This placement ensures that:
* Rate-limited requests still carry a `trace_id` for log correlation.
* Rate-limited requests are audited and access-logged.
* No expensive downstream work is performed for rejected requests.

## Memory and Cleanup

The limiter stores one `window` struct (three integers) per active IP or
token subject.  A background goroutine removes stale entries after each
window expires, so memory usage is proportional to the number of distinct
clients seen within one window period.

## Graceful Shutdown

The rate limiter registers as a lifecycle component.  During graceful
shutdown the background cleanup goroutine is stopped cleanly before the
HTTP server shuts down.
