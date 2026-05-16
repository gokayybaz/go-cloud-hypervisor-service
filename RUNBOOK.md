# Cloud Hypervisor API — Operations Runbook

This document is the single source of truth for installing, operating, and
troubleshooting the Cloud Hypervisor API (`ch-api`) in production.

> **Last updated:** 2026-05-16  
> **Module:** `github.com/org/ch-api`  
> **Owner:** Platform / Infrastructure team

---

## 1. Installation on a Fresh Bare-Metal Server

### 1.1 Prerequisites

- **OS:** Linux 5.10+ with KVM support (`/dev/kvm` accessible)
- **CPU:** x86_64 with VT-x/AMD-V and IOMMU (for PCI passthrough)
- **RAM:** 4 GB minimum, 8 GB recommended
- **Disk:** 20 GB for OS + images + VM data
- **Network:** Static IP or DHCP reservation; ports 8080 (API), 9090 (metrics) open
- **User:** Root or sudo access
- **Dependencies:** `cloud-hypervisor` binary in `$PATH`, compiled kernel image

Verify KVM:

```bash
ls -la /dev/kvm
# Should show: crw-rw---- 1 root kvm /dev/kvm
```

If missing, enable in BIOS and load the module:

```bash
sudo modprobe kvm_intel   # or kvm_amd
sudo usermod -aG kvm $USER
```

### 1.2 Quick Install (systemd)

```bash
# 1. Download the release tarball
curl -LO https://github.com/org/ch-api/releases/download/v1.0.0/ch-api-v1.0.0-linux-amd64.tar.gz
tar xzf ch-api-v1.0.0-linux-amd64.tar.gz

# 2. Run the Makefile install target
sudo make install

# 3. Configure (optional)
sudo mkdir -p /etc/ch-api
sudo tee /etc/ch-api/config.yaml <<'EOF'
server:
  port: "8080"
  host: "0.0.0.0"
  shutdown_timeout: "15s"

log:
  level: "info"
  format: "json"

auth:
  enabled: true
  secret: "$(openssl rand -hex 32)"
  rbac_enabled: true

cloud_hypervisor:
  binary_path: "cloud-hypervisor"
  socket_path: "/var/run/ch-api/ch.sock"
EOF
sudo chmod 640 /etc/ch-api/config.yaml
sudo chown root:ch-api /etc/ch-api/config.yaml

# 4. Verify
sudo systemctl status ch-api
curl -f http://localhost:8080/healthz
```

### 1.3 Quick Install (Docker)

```bash
# 1. Pull the image
docker pull ghcr.io/org/ch-api:latest

# 2. Run with a named volume for data persistence
docker run -d \
  --name ch-api \
  --restart unless-stopped \
  -p 8080:8080 \
  -p 9090:9090 \
  -v ch-api-data:/app/data \
  -v /var/run/ch-api:/var/run/ch-api:rw \
  -e CH_API_CONFIG=/etc/ch-api/config.yaml \
  ghcr.io/org/ch-api:latest

# 3. Verify
docker inspect --format='{{.State.Health.Status}}' ch-api
curl -f http://localhost:8080/healthz
```

### 1.4 Directory Layout

| Path | Purpose | Owner | Permissions |
|------|---------|-------|-------------|
| `/usr/local/bin/ch-api` | Service binary | `root:root` | `755` |
| `/etc/systemd/system/ch-api.service` | systemd unit | `root:root` | `644` |
| `/etc/ch-api/config.yaml` | Configuration | `root:ch-api` | `640` |
| `/var/lib/ch-api/` | Working directory | `ch-api:ch-api` | `750` |
| `/var/lib/ch-api/data/events*` | Event log files | `ch-api:ch-api` | `644` |
| `/var/lib/ch-api/data/audit/` | Audit SQLite DB | `ch-api:ch-api` | `644` |
| `/var/lib/ch-api/data/vm-operations.log` | VM operation log | `ch-api:ch-api` | `644` |
| `/var/run/ch-api/ch.sock` | CH Unix socket | `ch-api:ch-api` | `700` |

---

## 2. Common Failure Scenarios & Remediation

### 2.1 Service fails to start

**Symptoms:**

```bash
sudo systemctl status ch-api
# Active: failed (Result: exit-code)
```

**Diagnosis:**

```bash
sudo journalctl -u ch-api --no-pager -l -n 50
```

**Common causes & fixes:**

| Cause | Journal Log Pattern | Fix |
|-------|---------------------|-----|
| Port already in use | `bind: address already in use` | Change `server.port` in config or kill the process using port 8080 |
| Config file unreadable | `failed to load config: read config file` | `sudo chmod 640 /etc/ch-api/config.yaml; sudo chown root:ch-api /etc/ch-api/config.yaml` |
| CH binary not found | `cloud-hypervisor not found in $PATH` | Install CH: `sudo apt install cloud-hypervisor` or download binary to `/usr/local/bin/` |
| CH socket permission denied | `VMM socket permission check failed` | `sudo chown ch-api:ch-api /var/run/ch-api/ch.sock; sudo chmod 700 /var/run/ch-api/ch.sock` |
| Missing data directory | `mkdir data: permission denied` | `sudo mkdir -p /var/lib/ch-api/data; sudo chown -R ch-api:ch-api /var/lib/ch-api` |
| Preflight strict mode failure | `preflight strict mode enabled, exiting` | Disable strict mode or fix the failing check |
| Auth secret empty | `auth enabled but secret is empty` | Set `auth.secret` in config or disable auth |

### 2.2 Service restarts in a loop

**Symptoms:**
- `systemctl status` shows repeated restarts
- `journalctl` shows the same error on every boot attempt

**Diagnosis:**

```bash
sudo systemctl status ch-api --no-pager
sudo journalctl -u ch-api --since "5 minutes ago"
```

**Remediation:**

1. Stop the restart loop:
   ```bash
   sudo systemctl stop ch-api
   ```
2. Check the cause (see 2.1)
3. Fix the root cause
4. Clear the start limit counter:
   ```bash
   sudo systemctl reset-failed ch-api
   sudo systemctl start ch-api
   ```

### 2.3 API returns 500 Internal Server Error

**Symptoms:**
- All or some endpoints return `500` with `application/problem+json`
- Logs show panics or VMM errors

**Diagnosis:**

```bash
# Check recent errors
sudo journalctl -u ch-api -p err --since "1 hour ago"

# Check panic recovery logs
sudo journalctl -u ch-api | grep "panic recovered"
```

**Remediation:**

| Scenario | Fix |
|----------|-----|
| VMM unreachable | Verify CH is running: `pgrep cloud-hypervisor`; check socket path |
| SQLite database locked | Restart service; if persists, move `/var/lib/ch-api/data/audit/` and recreate |
| Out of memory | Increase `MemoryMax` in unit file or add RAM; check `dmesg` for OOM kills |
| Panic in handler | Open an incident; attach stack trace from journal |

### 2.4 High memory usage

**Symptoms:**
- `MemoryMax` reached, service OOM-killed
- Metrics show steady RSS climb

**Diagnosis:**

```bash
# Check memory usage
curl -s http://localhost:9090/metrics | grep process_resident_memory_bytes

# Check OOM events
sudo dmesg | grep -i "killed process.*ch-api"
sudo journalctl -k | grep -i oom
```

**Remediation:**

1. Check for goroutine leaks:
   ```bash
   curl -s http://localhost:6060/debug/pprof/goroutine?debug=1
   ```
   (Requires `--profile` flag or pprof enabled)

2. Restart the service:
   ```bash
   sudo systemctl restart ch-api
   ```

3. If the leak persists, increase `MemoryMax` as a temporary measure:
   ```bash
   sudo systemctl edit ch-api
   # Add: [Service] MemoryMax=1G
   sudo systemctl daemon-reload
   sudo systemctl restart ch-api
   ```

### 2.5 Authentication failures

**Symptoms:**
- All API calls return `401 Unauthorized`
- `invalid token` or `missing authorization header`

**Diagnosis:**

```bash
# Verify auth is enabled
curl -i http://localhost:8080/api/v1/status
# Should return 401 if auth is enabled

# Test with a valid token
TOKEN=$(./scripts/generate-token.sh admin)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/status
```

**Remediation:**

| Cause | Fix |
|-------|-----|
| Wrong token secret | Regenerate token with the correct `CH_API_AUTH_SECRET` |
| Token expired | Generate a new token with a future `exp` claim |
| RBAC denied (403) | Check the user's roles in the JWT `roles` claim; ensure they match the permission table |
| Missing `Authorization` header | Add `Authorization: Bearer <token>` to requests |

### 2.6 Rate limiting

**Symptoms:**
- `429 Too Many Requests` responses
- `Retry-After` header present

**Remediation:**

1. Check if the limit is legitimate or misconfigured:
   ```bash
   curl -i -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/vms
   ```

2. Increase per-token limits in config (requires restart):
   ```yaml
   rate_limit:
     ip_limit: 200
     viewer_limit: 200
     operator_limit: 400
     admin_limit: 1000
   ```

3. For emergency bypass, restart the service (resets sliding windows):
   ```bash
   sudo systemctl restart ch-api
   ```

### 2.7 Disk space issues

**Symptoms:**
- `no space left on device` errors
- Event log rotation failing

**Diagnosis:**

```bash
df -h /var/lib/ch-api
du -sh /var/lib/ch-api/data/
ls -lhS /var/lib/ch-api/data/
```

**Remediation:**

1. Clean old event logs:
   ```bash
   sudo find /var/lib/ch-api/data/events -type f -mtime +30 -delete
   ```

2. Vacuum the audit database:
   ```bash
   sudo sqlite3 /var/lib/ch-api/data/audit/audit.db "VACUUM;"
   ```

3. If VM images are consuming space, move them to a dedicated volume:
   ```bash
   sudo mkdir -p /data/ch-images
   sudo mv /var/lib/ch-api/images /data/ch-images/
   sudo ln -s /data/ch-images /var/lib/ch-api/images
   ```

---

## 3. Log File Locations

### 3.1 systemd / journald (default)

When running under systemd, all logs go to the journal:

```bash
# Follow logs in real time
sudo journalctl -u ch-api -f

# Logs from the last hour
sudo journalctl -u ch-api --since "1 hour ago"

# Logs since last boot
sudo journalctl -u ch-api -b

# Export to file
sudo journalctl -u ch-api --since "2026-05-01" > /tmp/ch-api-export.log
```

### 3.2 Docker

```bash
# Follow logs
docker logs -f ch-api

# Last 100 lines
docker logs --tail 100 ch-api

# Export
docker logs ch-api > /tmp/ch-api-docker.log
```

### 3.3 Application log files (when file logging is configured)

| Log | Path | Format | Rotation |
|-----|------|--------|----------|
| Event log | `/var/lib/ch-api/data/events.YYYY-MM-DD.jsonl` | NDJSON | Daily rotation |
| Audit log | `/var/lib/ch-api/data/audit/audit.db` | SQLite | Vacuumed manually |
| VM operations | `/var/lib/ch-api/data/vm-operations.log` | NDJSON | Append-only |
| Storage ops | `/var/lib/ch-api/data/storage.log` | NDJSON | Append-only |
| Resource ops | `/var/lib/ch-api/data/resources.log` | NDJSON | Append-only |

### 3.4 Log format

Application logs are structured JSON:

```json
{"level":"info","time":"2026-05-16T12:00:00Z","message":"vm created","id":"vm-123","name":"web-01","trace_id":"abc-123"}
```

Fields:
- `level` — `debug`, `info`, `warn`, `error`
- `time` — ISO 8601 timestamp
- `message` — human-readable summary
- `trace_id` — propagated request ID (`X-Request-ID`)
- Additional key-value pairs vary by message

### 3.5 Metrics

Prometheus metrics are exposed at `http://localhost:9090/metrics`:

```bash
curl -s http://localhost:9090/metrics | grep chapi_
```

Key metrics:
- `chapi_http_requests_total` — request count by method, path, status
- `chapi_http_request_duration_seconds` — latency histogram
- `chapi_errors_total` — error count by type (`validation`, `internal`, `vm_not_found`)
- `chapi_vms_active` — current number of VMs in the store
- `process_resident_memory_bytes` — RSS memory usage

---

## 4. Service Management Commands

### 4.1 systemd

```bash
# Status
sudo systemctl status ch-api

# Start / Stop / Restart
sudo systemctl start ch-api
sudo systemctl stop ch-api
sudo systemctl restart ch-api

# Enable / Disable auto-start
sudo systemctl enable ch-api
sudo systemctl disable ch-api

# Reload configuration (graceful)
sudo systemctl reload ch-api

# View logs
sudo journalctl -u ch-api -f

# Check health endpoint directly
curl -f http://localhost:8080/healthz && echo "OK"
```

### 4.2 Docker

```bash
# Start / Stop / Restart
docker start ch-api
docker stop ch-api
docker restart ch-api

# Health status
docker inspect --format='{{.State.Health.Status}}' ch-api

# Logs
docker logs -f ch-api

# Shell-less exec (distroless has no shell)
docker exec ch-api /busybox/wget -qO- http://localhost:8080/healthz
```

### 4.3 Binary (development / debugging)

```bash
# Run with console logs
CH_API_LOG_FORMAT=console ./bin/ch-api

# Run with debug logging
CH_API_LOG_LEVEL=debug ./bin/ch-api

# Preflight check only
PREFLIGHT=1 ./bin/ch-api
```

### 4.4 Configuration hot-reload

The service watches the config file for changes when running under systemd.
To trigger a reload:

```bash
sudo systemctl reload ch-api
```

Or edit the file and the watcher goroutine will pick it up automatically.

---

## 5. Escalation Contacts

| Severity | Condition | Action | Contact |
|----------|-----------|--------|---------|
| **P0 — Critical** | Complete API outage, data loss, security breach | Page on-call immediately; engage SRE | **TODO: Add PagerDuty / OpsGenie integration** |
| **P1 — High** | Degraded API (high latency, partial failures), VM creation failing | Open incident ticket; notify platform team | **TODO: Add Slack channel / email alias** |
| **P2 — Medium** | Single endpoint failing, non-urgent bug, performance regression | Create GitHub issue; assign to next sprint | **TODO: Add GitHub issues URL** |
| **P3 — Low** | Documentation gap, minor UX issue, cosmetic bug | Add to backlog | **TODO: Add Jira / Linear project link** |

### On-call rotation

| Role | Name | Slack | Phone |
|------|------|-------|-------|
| Primary on-call | **TODO** | **TODO** | **TODO** |
| Secondary on-call | **TODO** | **TODO** | **TODO** |
| SRE lead | **TODO** | **TODO** | **TODO** |
| Engineering manager | **TODO** | **TODO** | **TODO** |

### External dependencies

| Service | Owner | Escalation | Notes |
|---------|-------|------------|-------|
| Cloud Hypervisor binary | Upstream / distro package | **TODO** | File CH bugs at github.com/cloud-hypervisor/cloud-hypervisor |
| KVM / kernel | OS vendor | **TODO** | Kernel panics → distro support |
| Host network | Network team | **TODO** | Interface down / routing issues |
| TLS certificate | Security team | **TODO** | ACME or manual cert expiry |

---

## 6. Quick Reference

### Health checks

```bash
# Liveness
curl -f http://localhost:8080/healthz

# Readiness (requires auth if enabled)
curl -f -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/status

# Metrics
curl -s http://localhost:9090/metrics | grep "chapi_"
```

### Common curl commands

```bash
# List VMs
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/vms

# Create a VM
curl -X POST -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"web-01","cpus":{"boot_vcpus":2,"max_vcpus":4},"memory":{"size":1024},"kernel":{"path":"/boot/vmlinux"},"disks":[{"path":"/images/ubuntu.raw"}]}' \
  http://localhost:8080/api/v1/vms

# Boot a VM
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/vms/<id>/boot
```

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CH_API_CONFIG` | `./config.yaml` | Config file path |
| `CH_API_LOG_LEVEL` | `info` | Log verbosity |
| `CH_API_LOG_FORMAT` | `json` | `json` or `console` |
| `CH_BINARY` | `cloud-hypervisor` | CH binary path (e2e tests) |
| `CH_KERNEL` | — | Kernel image path (e2e tests) |
| `PREFLIGHT` | `0` | Set to `1` to run preflight checks only |

---

## 7. Change Log

| Date | Author | Change |
|------|--------|--------|
| 2026-05-16 | OpenCode | Initial runbook created |
