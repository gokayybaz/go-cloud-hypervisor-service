# Fuzz Testing

The project uses Go's native fuzzing (`go test -fuzz`) to exercise JSON-parsing
request DTOs.  Each handler that unmarshals JSON body data has a dedicated fuzz
target.

## Targets

| Fuzz Target | DTO | Validates |
|-------------|-----|-----------|
| `FuzzCreateVMRequest` | `CreateVMRequest` | `json.Unmarshal` + `Validate()` |
| `FuzzPatchCPURequest` | `PatchCPURequest` | `json.Unmarshal` |
| `FuzzPatchMemoryRequest` | `PatchMemoryRequest` | `json.Unmarshal` |
| `FuzzAddInterfaceRequest` | `AddInterfaceRequest` | `json.Unmarshal` + `Validate()` |
| `FuzzPatchInterfaceRequest` | `PatchInterfaceRequest` | `json.Unmarshal` + `Validate()` |
| `FuzzAddDiskRequest` | `AddDiskRequest` | `json.Unmarshal` + `Validate()` |

## Running Fuzz Tests

### Smoke test (seed corpus only)

Runs the fuzz target for a few seconds using the committed seed corpus:

```bash
go test ./internal/handler/ -fuzz=FuzzCreateVMRequest -fuzztime=5s
```

### Full fuzzing session

Let the fuzzer run indefinitely (or until you stop it with Ctrl-C):

```bash
go test ./internal/handler/ -fuzz=FuzzCreateVMRequest
```

### Run all targets briefly

```bash
for t in FuzzCreateVMRequest FuzzPatchCPURequest FuzzPatchMemoryRequest \
         FuzzAddInterfaceRequest FuzzPatchInterfaceRequest FuzzAddDiskRequest; do
    echo "=== $t ==="
    go test ./internal/handler/ -run='^$' -fuzz=$t -fuzztime=10s
done
```

## Seed Corpus

Initial seed inputs live under:

```
internal/handler/testdata/fuzz/<FuzzTargetName>/
```

These files are committed to the repository so that CI and new contributors
start from a known-good baseline.  Each file uses Go's `go test fuzz` wire
format:

```
go test fuzz v1
[]byte("{...}")
```

The corpus includes:
- **Valid inputs** — well-formed JSON that passes validation
- **Empty objects** — `{}` to test default zero-value handling
- **Invalid fields** — wrong types, missing required fields, out-of-range values
- **Non-JSON** — plain text to ensure `json.Unmarshal` errors are handled

## Triage Workflow

### 1. Discover a crash

When the fuzzer finds an input that panics or causes a hard error, it writes a
crash file to:

```
internal/handler/testdata/fuzz/<FuzzTargetName>/<sha256-hash>
```

### 2. Reproduce

Run the fuzz target against the crash file to confirm:

```bash
go test ./internal/handler/ -fuzz=FuzzCreateVMRequest \
    -test.fuzzminimizetime=10s \
    -test.fuzz=internal/handler/testdata/fuzz/FuzzCreateVMRequest/<hash>
```

### 3. Minimize

Go can minimize the crash input automatically:

```bash
go test ./internal/handler/ -fuzz=FuzzCreateVMRequest -fuzzminimizetime=30s \
    testdata/fuzz/FuzzCreateVMRequest/<hash>
```

The minimized input is written back to the same path.

### 4. Fix and regression-test

1. Fix the bug in the handler / validation code.
2. Add the minimized crash input to the committed corpus so CI catches
   regressions.
3. Run the fuzz target again to verify the crash no longer reproduces.

## CI Integration

Add a short fuzz smoke-test step to the GitHub Actions workflow:

```yaml
- name: Fuzz smoke test
  run: |
    go test ./internal/handler/ -fuzz=FuzzCreateVMRequest -fuzztime=30s
    go test ./internal/handler/ -fuzz=FuzzPatchCPURequest -fuzztime=30s
    go test ./internal/handler/ -fuzz=FuzzPatchMemoryRequest -fuzztime=30s
    go test ./internal/handler/ -fuzz=FuzzAddInterfaceRequest -fuzztime=30s
    go test ./internal/handler/ -fuzz=FuzzPatchInterfaceRequest -fuzztime=30s
    go test ./internal/handler/ -fuzz=FuzzAddDiskRequest -fuzztime=30s
```

This ensures the seed corpus still passes and the fuzzer can execute without
immediate crashes.

## Known Limitations

- Fuzz targets only exercise JSON parsing and validation.  They do not make
  HTTP requests or interact with the VMM / store layers.
- The `Validate()` methods on some DTOs call `net.ParseMAC` and `net.ParseIP`;
  these are exercised by the fuzzer but not mocked.
- Fuzzing does not cover WebSocket upgrade paths (console handler).

## Adding a New Fuzz Target

1. Create a new `FuzzXxx` function in a `*_test.go` file inside the handler
   package.
2. Call `f.Add([]byte(...))` with at least one seed input.
3. In the `f.Fuzz` callback, unmarshal into the target struct and call any
   validation methods.
4. Add seed corpus files to `testdata/fuzz/FuzzXxx/` using the wire format.
5. Run the target locally to verify it compiles and executes.

Example:

```go
func FuzzMyRequest(f *testing.F) {
    f.Add([]byte(`{"field":"value"}`))
    f.Fuzz(func(t *testing.T, data []byte) {
        var req MyRequest
        _ = json.Unmarshal(data, &req)
        _ = req.Validate()
    })
}
```
