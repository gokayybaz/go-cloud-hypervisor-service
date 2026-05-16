# systemd Service Installation

This document describes how to install and operate the Cloud Hypervisor API as a
systemd service on Linux.

## Prerequisites

- Linux distribution with systemd (Ubuntu 22.04+, RHEL 9+, Debian 12+, etc.)
- Root or `sudo` access
- The `ch-api` binary built for the target architecture (see [CI build](../.github/workflows/ci.yml))
- A configuration file (optional; the binary uses built-in defaults when absent)

## Quick Start

```bash
# 1. Create the dedicated user and directories
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ch-api
sudo mkdir -p /etc/ch-api /var/lib/ch-api/data
sudo chown -R ch-api:ch-api /var/lib/ch-api

# 2. Install the binary
sudo cp ch-api-linux-amd64 /usr/local/bin/ch-api
sudo chmod +x /usr/local/bin/ch-api

# 3. Install the systemd unit file
sudo cp systemd/ch-api.service /etc/systemd/system/
sudo systemctl daemon-reload

# 4. Enable and start the service
sudo systemctl enable --now ch-api

# 5. Verify
sudo systemctl status ch-api
sudo journalctl -u ch-api -f
```

## Files and Directories

| Path | Purpose | Permissions |
|------|---------|-------------|
| `/usr/local/bin/ch-api` | Service binary | `755 root:root` |
| `/etc/systemd/system/ch-api.service` | systemd unit file | `644 root:root` |
| `/etc/ch-api/config.yaml` | Configuration file (optional) | `640 root:ch-api` |
| `/var/lib/ch-api/` | Working directory (data, logs, audit DB) | `750 ch-api:ch-api` |
| `/var/lib/ch-api/data/` | Runtime data (events, audit, VM ops) | `750 ch-api:ch-api` |

## Step-by-Step Installation

### 1. Create the `ch-api` user

The service runs as an unprivileged, dedicated user:

```bash
sudo useradd --system \
  --no-create-home \
  --shell /usr/sbin/nologin \
  --comment "Cloud Hypervisor API service" \
  ch-api
```

### 2. Prepare data directories

```bash
sudo mkdir -p /var/lib/ch-api/data
sudo mkdir -p /etc/ch-api
sudo chown -R ch-api:ch-api /var/lib/ch-api
sudo chmod 750 /var/lib/ch-api
```

### 3. Install the binary

```bash
sudo cp ./ch-api-linux-amd64 /usr/local/bin/ch-api
sudo chmod +x /usr/local/bin/ch-api
```

### 4. Install the unit file

```bash
sudo cp systemd/ch-api.service /etc/systemd/system/ch-api.service
sudo systemctl daemon-reload
```

### 5. Configure the service (optional)

Create `/etc/ch-api/config.yaml`:

```yaml
server:
  port: "8080"
  host: ""
  shutdown_timeout: "15s"

log:
  level: "info"
  format: "json"

auth:
  enabled: true
  secret: "change-me-in-production"
  rbac_enabled: true

cloud_hypervisor:
  binary_path: "cloud-hypervisor"
  socket_path: "/var/run/ch-api/ch.sock"
```

Set restrictive permissions:

```bash
sudo chmod 640 /etc/ch-api/config.yaml
sudo chown root:ch-api /etc/ch-api/config.yaml
```

### 6. Start and enable the service

```bash
sudo systemctl enable ch-api      # start on boot
sudo systemctl start ch-api       # start now
```

## Service Management

### Check status

```bash
sudo systemctl status ch-api
```

### View logs

```bash
# Follow real-time logs
sudo journalctl -u ch-api -f

# Logs since last boot
sudo journalctl -u ch-api -b

# Logs from the last hour
sudo journalctl -u ch-api --since "1 hour ago"

# Export logs to file
sudo journalctl -u ch-api --since "2026-01-01" > ch-api.log
```

### Restart, stop, reload

```bash
sudo systemctl restart ch-api   # restart the service
sudo systemctl stop ch-api      # stop the service
sudo systemctl reload ch-api    # graceful reload (if supported)
```

## Unit File Reference

### `[Unit]` section

| Directive | Value | Purpose |
|-----------|-------|---------|
| `After=network.target` | — | Wait for network interfaces to be up before starting |
| `Wants=network-online.target` | — | Ensure network is actually online, not just service-started |

### `[Service]` section

| Directive | Value | Purpose |
|-----------|-------|---------|
| `Type=exec` | — | systemd considers the service started once `ExecStart` is executed |
| `Restart=on-failure` | — | Restart only when the process exits with a non-zero status |
| `RestartSec=5` | 5 seconds | Wait between restart attempts |
| `StartLimitIntervalSec=60` | 60 seconds | Window for `StartLimitBurst` |
| `StartLimitBurst=3` | 3 starts | If the service fails 3 times in 60 seconds, stop trying |
| `LimitNOFILE=65536` | 65536 | Max open file descriptors (sockets, disk images, logs) |
| `LimitNPROC=4096` | 4096 | Max processes/threads for the service user |
| `MemoryMax=512M` | 512 MiB | Hard memory limit (OOM-kill if exceeded) |
| `TasksMax=512` | 512 | Max concurrent tasks/threads |
| `StandardOutput=journal` | — | Forward stdout to journald |
| `StandardError=journal` | — | Forward stderr to journald |
| `SyslogIdentifier=ch-api` | — | Tag used in journald logs |
| `TimeoutStopSec=30` | 30 seconds | Graceful shutdown timeout before SIGKILL |

### Security hardening directives

| Directive | Purpose |
|-----------|---------|
| `NoNewPrivileges=true` | Prevent privilege escalation via setuid binaries |
| `ProtectSystem=strict` | Mount `/usr`, `/boot`, `/etc` read-only |
| `ProtectHome=true` | Make `/home` and `/root` inaccessible |
| `ReadWritePaths=/var/lib/ch-api` | Only writable path (data directory) |
| `ProtectKernelTunables=true` | Protect `/proc/sys`, `/sys` |
| `ProtectKernelModules=true` | Prevent kernel module loading |
| `ProtectControlGroups=true` | Make cgroup hierarchies read-only |
| `RestrictSUIDSGID=true` | Block creation of setuid/setgid files |
| `RestrictRealtime=true` | Prevent real-time scheduling |
| `RestrictNamespaces=true` | Restrict namespace creation |
| `LockPersonality=true` | Prevent personality syscall changes |
| `MemoryDenyWriteExecute=true` | Prevent writable-executable memory mappings |

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CH_API_CONFIG` | `/etc/ch-api/config.yaml` | Path to YAML configuration file |
| `CH_API_LOG_LEVEL` | `info` | Log verbosity (`debug`, `info`, `warn`, `error`) |
| `CH_API_LOG_FORMAT` | `json` | Log format (`json` or `console`) |

Override via a drop-in file:

```bash
sudo systemctl edit ch-api
```

Add:

```ini
[Service]
Environment="CH_API_LOG_LEVEL=debug"
Environment="CH_API_CONFIG=/opt/ch-api/custom.yaml"
```

Then reload:

```bash
sudo systemctl daemon-reload
sudo systemctl restart ch-api
```

## Troubleshooting

### Service fails to start

```bash
# Check the status and recent log entries
sudo systemctl status ch-api --no-pager -l
sudo journalctl -u ch-api --no-pager -l -n 50
```

### Permission denied on data directory

Ensure the `ch-api` user owns the working directory:

```bash
sudo chown -R ch-api:ch-api /var/lib/ch-api
sudo chmod 750 /var/lib/ch-api
```

### Port already in use

If port 8080 is taken, change the port in `/etc/ch-api/config.yaml`:

```yaml
server:
  port: "9090"
```

Then restart:

```bash
sudo systemctl restart ch-api
```

### Socket permission issues (Cloud Hypervisor)

If the CH Unix socket is at a path the service user cannot access, either:

1. Move the socket to `/var/run/ch-api/` and ensure `ch-api` owns it, or
2. Add the `ch-api` user to the group that owns the socket.

### OOM killed (out of memory)

If the service is killed by the OOM killer, check:

```bash
sudo journalctl -k | grep -i "killed process" | grep ch-api
```

Increase `MemoryMax` via a drop-in if the limit is too low:

```bash
sudo systemctl edit ch-api
# Add: [Service] MemoryMax=1G
sudo systemctl daemon-reload
sudo systemctl restart ch-api
```

### Too many open files

If you see `too many open files` errors, increase `LimitNOFILE` in the unit
file and reload:

```bash
sudo systemctl edit ch-api
# Add: [Service] LimitNOFILE=131072
sudo systemctl daemon-reload
sudo systemctl restart ch-api
```

## Uninstallation

```bash
sudo systemctl stop ch-api
sudo systemctl disable ch-api
sudo rm /etc/systemd/system/ch-api.service
sudo systemctl daemon-reload
sudo rm -rf /usr/local/bin/ch-api /var/lib/ch-api /etc/ch-api
sudo userdel ch-api
```

## Further Reading

- [systemd.exec(5)](https://www.freedesktop.org/software/systemd/man/systemd.exec.html) — Service execution environment
- [systemd.resource-control(5)](https://www.freedesktop.org/software/systemd/man/systemd.resource-control.html) — Resource limits
- [journalctl(1)](https://www.freedesktop.org/software/systemd/man/journalctl.html) — Query the journal
- [Configuration](../docs/configuration.md) — Application configuration reference
