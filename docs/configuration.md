# Configuration Guide

`ch-api` uses a layered configuration system powered by [Viper](https://github.com/spf13/viper). Values can be provided via:

1. **Built-in defaults** (hard-coded)
2. **YAML config file** (`config.yaml`)
3. **Environment variables** (highest precedence)

The three layers are merged in the order above — environment variables always win.

## Configuration File

The application searches for `config.yaml` in the following order:

1. Current working directory (`./config.yaml`)
2. System directory (`/etc/ch-api/config.yaml`)
3. User home directory (`$HOME/.ch-api/config.yaml`)

A sample file is provided at `config.yaml.example`.

## Environment Variables

All settings can be overridden via environment variables using the `CH_API_` prefix. Nested keys use underscores as separators:

| YAML Key                     | Environment Variable                  |
|------------------------------|---------------------------------------|
| `server.port`                | `CH_API_SERVER_PORT`                  |
| `server.host`                | `CH_API_SERVER_HOST`                  |
| `server.read_timeout`        | `CH_API_SERVER_READ_TIMEOUT`          |
| `server.write_timeout`       | `CH_API_SERVER_WRITE_TIMEOUT`         |
| `server.idle_timeout`        | `CH_API_SERVER_IDLE_TIMEOUT`          |
| `server.shutdown_timeout`    | `CH_API_SERVER_SHUTDOWN_TIMEOUT`      |
| `log.level`                  | `CH_API_LOG_LEVEL`                    |
| `log.format`                 | `CH_API_LOG_FORMAT`                   |
| `preflight.enabled`          | `CH_API_PREFLIGHT_ENABLED`            |
| `preflight.strict_mode`      | `CH_API_PREFLIGHT_STRICT_MODE`        |
| `cloud_hypervisor.binary_path`| `CH_API_CLOUD_HYPERVISOR_BINARY_PATH` |
| `cloud_hypervisor.socket_path`| `CH_API_CLOUD_HYPERVISOR_SOCKET_PATH` |

### Example

```bash
CH_API_SERVER_PORT=9090 CH_API_LOG_LEVEL=debug ./ch-api
```

## Available Keys

### `server`

| Key               | Default   | Description                              |
|-------------------|-----------|------------------------------------------|
| `port`            | `8080`    | HTTP listen port                         |
| `host`            | `""`      | HTTP listen host (empty = all interfaces)|
| `read_timeout`    | `15s`     | HTTP read timeout                        |
| `write_timeout`   | `15s`     | HTTP write timeout                       |
| `idle_timeout`    | `60s`     | HTTP idle timeout                        |
| `shutdown_timeout`| `15s`     | Graceful shutdown timeout                |

### `log`

| Key      | Default  | Description                        |
|----------|----------|------------------------------------|
| `level`  | `info`   | `debug`, `info`, `warn`, `error`, `fatal`, `panic` |
| `format` | `json`   | `json` or `console` (pretty-print) |

### `preflight`

| Key           | Default | Description                                           |
|---------------|---------|-------------------------------------------------------|
| `enabled`     | `true`  | Run preflight checks on startup                       |
| `strict_mode` | `false` | Exit immediately if any preflight check fails         |

### `cloud_hypervisor`

| Key           | Default                         | Description                        |
|---------------|---------------------------------|------------------------------------|
| `binary_path` | `cloud-hypervisor`              | Path or name of the CH binary      |
| `socket_path` | `/var/run/ch-api/ch.sock`       | UNIX socket for CH API             |

## Hot Reload

The configuration file is watched for changes at runtime. When `config.yaml` is modified on disk, the application reloads it automatically and invokes a callback with the new configuration.

```go
loader := config.NewLoader()
cfg, _ := loader.Load()

watcher := loader.Watch(func(newCfg *config.Config) {
    // React to config change, e.g. adjust log level
    logger.Info("config reloaded", "level", newCfg.Log.Level)
})
defer watcher.Stop()
```

**Limitations:**
- Environment variables are read once at startup and are not re-evaluated on reload.
- The HTTP server address (`host`, `port`) changes require a process restart.
- The watcher uses `fsnotify` and may not work correctly on all network filesystems.

## Example `config.yaml`

```yaml
server:
  port: "8080"
  host: ""
  read_timeout: "15s"
  write_timeout: "15s"
  idle_timeout: "60s"
  shutdown_timeout: "15s"

log:
  level: "info"
  format: "json"

preflight:
  enabled: true
  strict_mode: false

cloud_hypervisor:
  binary_path: "cloud-hypervisor"
  socket_path: "/var/run/ch-api/ch.sock"
```