# Profiling

The API can optionally expose Go runtime profiling endpoints on a dedicated
`localhost`-only HTTP server.  This is useful for debugging memory leaks,
hot paths, and goroutine contention during development or load testing.

## Security Warning

**Profiling endpoints expose sensitive runtime data and must never be enabled
in production.**  The pprof server binds strictly to `localhost:6060` and is
not reachable from remote hosts, but the safest approach is to leave the
feature disabled (`profile: false`) in production configurations.

## Enabling Profiling

Profiling is controlled by the `profile` configuration key.

### Via configuration file

```yaml
profile: true
```

### Via environment variable

```bash
CH_API_PROFILE=1 ./ch-api
```

When enabled, the lifecycle manager starts a `pprof-server` component on
`localhost:6060` alongside the main HTTP API.

## Available Endpoints

| Endpoint | Description |
|----------|-------------|
| `http://localhost:6060/debug/pprof/` | Index page with links to all profiles |
| `http://localhost:6060/debug/pprof/cmdline` | Command-line invocation of the program |
| `http://localhost:6060/debug/pprof/profile` | **CPU profile** (see below) |
| `http://localhost:6060/debug/pprof/symbol` | Symbol resolution lookup |
| `http://localhost:6060/debug/pprof/trace` | Execution trace |
| `http://localhost:6060/debug/pprof/allocs` | Allocation profile |
| `http://localhost:6060/debug/pprof/block` | Blocking profile |
| `http://localhost:6060/debug/pprof/goroutine` | Goroutine stack traces |
| `http://localhost:6060/debug/pprof/heap` | Heap profile |
| `http://localhost:6060/debug/pprof/mutex` | Mutex contention profile |
| `http://localhost:6060/debug/pprof/threadcreate` | Thread creation profile |

## Collecting a CPU Profile

The most common use case is capturing a 30-second CPU profile while running
a load test or reproducing a specific workload.

### Step 1: Enable profiling

Start the server with profiling enabled:

```bash
CH_API_PROFILE=1 go run ./cmd/ch-api
```

You should see a log line:

```
{"level":"info","msg":"pprof profiling enabled","addr":"localhost:6060"}
```

### Step 2: Generate workload

While the profile is being collected, exercise the API.  For example:

```bash
# Create a few VMs
for i in {1..10}; do
  curl -s -X POST http://localhost:8080/api/v1/vms \
    -H "Content-Type: application/json" \
    -d '{"name":"vm-'$i'","cpus":{"boot_vcpus":1,"max_vcpus":2},"memory":{"size":256},"kernel":{"path":"/boot/vmlinuz"},"disks":[{"path":"/disk.raw"}]}' > /dev/null
done

# List them repeatedly
for i in {1..100}; do
  curl -s http://localhost:8080/api/v1/vms > /dev/null
done
```

### Step 3: Capture the profile

Run `go tool pprof` to fetch and analyse the profile:

```bash
# Collect a 30-second CPU profile and open the interactive viewer
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
```

Inside the `pprof` interactive shell, useful commands include:

| Command | Description |
|---------|-------------|
| `top10` | Show the 10 hottest functions |
| `top10 -cum` | Show the 10 functions with the highest cumulative time |
| `list <func>` | Show annotated source for a function |
| `web` | Generate an SVG flame graph and open it in a browser |
| `png` | Export the call graph as a PNG image |
| `quit` | Exit the interactive viewer |

### Step 4: Save the profile for later

You can also download the raw profile to disk for offline analysis or
comparison:

```bash
curl -s http://localhost:6060/debug/pprof/profile?seconds=30 > cpu.prof

# Analyse later
go tool pprof cpu.prof
```

## Heap and Allocation Profiles

Capture a snapshot of live heap objects:

```bash
curl -s http://localhost:6060/debug/pprof/heap > heap.prof
go tool pprof heap.prof
```

Show the top allocators by cumulative bytes:

```bash
go tool pprof -alloc_space http://localhost:6060/debug/pprof/allocs
```

## Goroutine Dump

If the API appears to hang, a goroutine dump can reveal deadlocks or
runaway goroutines:

```bash
curl -s http://localhost:6060/debug/pprof/goroutine?debug=2
```

With `debug=2` the output is plain-text stack traces similar to
`SIGQUIT`.  With `debug=1` you get a compact summary.

## Execution Trace

For sub-millisecond timing analysis (scheduler latency, GC pauses, channel
operations), capture an execution trace:

```bash
curl -s http://localhost:6060/debug/pprof/trace?seconds=5 > trace.out
go tool trace trace.out
```

This opens a web-based trace viewer in your browser.

## Disabling in Production

The recommended approach is to explicitly set `profile: false` in production
configuration files and to treat `CH_API_PROFILE=1` as a red flag in
production environment audits.

```yaml
# config.yaml (production)
profile: false
```

If you run the binary in a container, avoid forwarding port `6060` so that
even a misconfiguration cannot expose the endpoint externally.
