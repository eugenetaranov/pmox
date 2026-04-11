## 1. client.go — requestForm helper

- [x] 1.1 Add unexported `func (c *Client) requestForm(ctx context.Context, method, path string, form url.Values) ([]byte, error)` in `client.go`
- [x] 1.2 Body construction: if `len(form) > 0`, wrap `strings.NewReader(form.Encode())` as the body; else pass `nil`
- [x] 1.3 Set headers: `Authorization`, `Accept: application/json`, and `Content-Type: application/x-www-form-urlencoded` **only when the body is non-empty**
- [x] 1.4 Reuse the existing status-code switch from `request()` — 401 → `ErrUnauthorized`, 404 → `ErrNotFound`, 5xx → `ErrAPIError`, 4xx → `ErrAPIError`, TLS errors → `ErrTLSVerificationFailed`
- [x] 1.5 Test: new `client_test.go` case that POSTs with a form body and asserts the server received `Content-Type: application/x-www-form-urlencoded` and the expected form-encoded payload
- [x] 1.6 Test: `requestForm` with an empty `url.Values` does **not** set `Content-Type` and sends no body

## 2. errors.go — ErrTimeout

- [x] 2.1 Add `var ErrTimeout = errors.New("operation timed out")` to `internal/pveclient/errors.go`
- [x] 2.2 No exitcode wiring — `WaitTask` callers will wrap with their own context. `ErrTimeout` maps to `ExitGeneric` via the default branch of `exitcode.From`, which is fine for this slice

## 3. nextid.go — NextID

- [x] 3.1 Create `internal/pveclient/nextid.go` with `func (c *Client) NextID(ctx context.Context) (int, error)`
- [x] 3.2 Call `request(ctx, "GET", "/cluster/nextid", nil)`
- [x] 3.3 Parse response: `{"data": "100"}` — the value is a **string**, not an int. Use `json.Unmarshal` into `struct { Data string }`, then `strconv.Atoi`
- [x] 3.4 On parse failure, return `fmt.Errorf("parse nextid response: %w", err)`
- [x] 3.5 Test: `nextid_test.go` with `httptest.Server` returning `{"data":"100"}` — assert `NextID` returns `100`
- [x] 3.6 Test: error path — server returns `{"data":"not-a-number"}`, assert error

## 4. vm.go — Clone, Resize, SetConfig, Start, GetStatus

- [x] 4.1 Create `internal/pveclient/vm.go`
- [x] 4.2 Declare `type VMStatus struct { Status, QMPStatus, Name string; VMID, CPUs int; Uptime, Mem, MaxMem int64 }` with json tags per design D4
- [x] 4.3 `func (c *Client) Clone(ctx context.Context, node string, sourceID, newID int, name string) (upid string, err error)` — POST `/nodes/{node}/qemu/{sourceID}/clone` with form `{"newid": newID, "name": name, "full": "1"}`. Parse `{"data": "UPID:..."}` and return the string
- [x] 4.4 `func (c *Client) Resize(ctx context.Context, node string, vmid int, disk, size string) error` — PUT `/nodes/{node}/qemu/{vmid}/resize` with form `{"disk": disk, "size": size}`. Response body ignored
- [x] 4.5 `func (c *Client) SetConfig(ctx context.Context, node string, vmid int, kv map[string]string) error` — POST `/nodes/{node}/qemu/{vmid}/config` with `kv` encoded as form values. Response body ignored
- [x] 4.6 `SetConfig` sshkeys quirk: if `kv` contains key `"sshkeys"`, replace its value with `url.QueryEscape(value)` **before** building the `url.Values`. Document with a comment above the method explaining PVE's double-encoding requirement
- [x] 4.7 `func (c *Client) Start(ctx context.Context, node string, vmid int) (upid string, err error)` — POST `/nodes/{node}/qemu/{vmid}/status/start` with no body. Parse UPID from `{"data": "UPID:..."}`
- [x] 4.8 `func (c *Client) GetStatus(ctx context.Context, node string, vmid int) (*VMStatus, error)` — GET `/nodes/{node}/qemu/{vmid}/status/current`. Parse `{"data": {...}}` into `*VMStatus`
- [x] 4.9 Shared helper in `vm.go`: `func parseDataString(body []byte) (string, error)` that parses `{"data": "<string>"}` envelopes returned by Clone/Start/Delete. Reused by all three UPID parsers
- [x] 4.10 Test fixture: create `internal/pveclient/testdata/status_running.json` with a realistic VMStatus payload

## 5. vm_test.go — VM endpoint tests

- [x] 5.1 Create `internal/pveclient/vm_test.go` with an `httptest.Server`-based helper `func newTestClient(t *testing.T, handler http.HandlerFunc) *Client`
- [x] 5.2 `TestClone_HappyPath`: mock server asserts method=POST, path=`/nodes/pve1/qemu/9000/clone`, form contains `newid=100&name=test&full=1`, returns `{"data":"UPID:pve1:..."}` — assert UPID returned
- [x] 5.3 `TestClone_Error`: server returns 500, assert `ErrAPIError`
- [x] 5.4 `TestResize`: mock server asserts PUT, form `disk=scsi0&size=%2B10G`, returns `{"data":null}`
- [x] 5.5 `TestSetConfig_HappyPath`: mock server asserts form contains all kv pairs
- [x] 5.6 `TestSetConfig_SSHKeysDoubleEncoded`: pass `kv = {"sshkeys": "ssh-ed25519 AAAA... user@host"}`, assert the received form body's `sshkeys` value is the URL-escaped form of the raw key (i.e. server sees the value *already once-escaped*, and http form decode gives that once-escaped string back)
- [x] 5.7 `TestStart`: mock server asserts POST to `/status/start`, returns `{"data":"UPID:..."}`, assert UPID
- [x] 5.8 `TestGetStatus`: load `testdata/status_running.json`, mock server returns it, assert parsed `VMStatus.Status == "running"` and other fields populated correctly

## 6. agent.go — AgentNetwork

- [x] 6.1 Create `internal/pveclient/agent.go`
- [x] 6.2 Declare `type AgentIface struct { Name, HardwareAddr string; IPAddresses []AgentIPAddr }` and `type AgentIPAddr struct { IPAddressType, IPAddress string; Prefix int }` with json tags
- [x] 6.3 `func (c *Client) AgentNetwork(ctx context.Context, node string, vmid int) ([]AgentIface, error)` — GET `/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces`
- [x] 6.4 Parse response: `{"data": {"result": [...]}}` — note the double wrapping (the PVE API wraps its `data` field, and the guest-agent's own response nests `result` inside)
- [x] 6.5 No built-in retry — a single call. Document the no-retry decision (D6) in a godoc comment
- [x] 6.6 Test fixture: `internal/pveclient/testdata/agent_network.json` with realistic multi-interface payload (lo + eth0 with both IPv4 and IPv6)
- [x] 6.7 `agent_test.go`: mock server returns the fixture, assert the parsed interfaces have the expected names and IP addresses
- [x] 6.8 Error test: 500 "QEMU guest agent is not running" → assert `ErrAPIError` is returned unchanged (no special-casing in the client)

## 7. delete.go — Delete

- [x] 7.1 Create `internal/pveclient/delete.go` (kept separate for symmetry; a trivial file, could go in vm.go — put it in vm.go actually to avoid file sprawl)
- [x] 7.2 Add `func (c *Client) Delete(ctx context.Context, node string, vmid int) (upid string, err error)` to `vm.go`. Call `request(ctx, "DELETE", ...)`. Parse UPID from `{"data": "UPID:..."}`
- [x] 7.3 Test in `vm_test.go`: mock server asserts DELETE method, returns UPID payload

## 8. tasks.go — WaitTask

- [x] 8.1 Create `internal/pveclient/tasks.go`
- [x] 8.2 Declare `type TaskStatus struct { Status, ExitStatus string }` for the `/tasks/{upid}/status` response
- [x] 8.3 `func (c *Client) GetTaskStatus(ctx context.Context, node, upid string) (*TaskStatus, error)` — GET `/nodes/{node}/tasks/{upid}/status`, parse
- [x] 8.4 `func (c *Client) WaitTask(ctx context.Context, node, upid string, timeout time.Duration) error` — poll loop per design D3
- [x] 8.5 Poll interval: `500 * time.Millisecond` constant at package level (not exported)
- [x] 8.6 Loop: call `GetTaskStatus`, check `.Status`. On `"stopped"` + `ExitStatus == "OK"` return nil. On `"stopped"` + other exit status, return `fmt.Errorf("%w: pve task %s: %s", ErrAPIError, upid, status.ExitStatus)`
- [x] 8.7 Respect `ctx.Done()` — return `ctx.Err()` immediately if cancelled
- [x] 8.8 Respect `timeout` — if elapsed, return `ErrTimeout` wrapped with UPID: `fmt.Errorf("%w: waiting for pve task %s", ErrTimeout, upid)`
- [x] 8.9 Test `tasks_test.go`: stateful mock server (closure-captured counter) returns `running` twice then `stopped + OK`, assert `WaitTask` returns nil
- [x] 8.10 Test: mock returns `stopped` with `ExitStatus = "clone failed: destination VMID 200 already exists"`, assert returned error wraps `ErrAPIError` and contains that text
- [x] 8.11 Test: mock always returns `running`, call `WaitTask` with 1.5s timeout, assert `ErrTimeout` returned
- [x] 8.12 Test: pre-cancelled context, assert `ctx.Err()` returned before any HTTP call (verify via a mock server that counts hits — should be zero)

## 9. Testdata fixtures

- [x] 9.1 Create `internal/pveclient/testdata/status_running.json` — hand-crafted from PVE API docs, realistic VMStatus payload
- [x] 9.2 Create `internal/pveclient/testdata/agent_network.json` — two interfaces (`lo`, `eth0`), eth0 has one IPv4 and one IPv6
- [x] 9.3 Create `internal/pveclient/testdata/clone_upid.json` — `{"data":"UPID:pve1:00001234:00005678:680ABCD0:qmclone:100:root@pam:"}` for reuse across Clone/Start/Delete tests
- [x] 9.4 Create `internal/pveclient/testdata/task_running.json` and `task_stopped_ok.json` for WaitTask tests

## 10. Verification

- [x] 10.1 `go build ./...` passes
- [x] 10.2 `go test ./internal/pveclient/... -race` passes
- [x] 10.3 `go test ./... -race` passes (full suite)
- [x] 10.4 `make lint` passes
- [x] 10.5 Token secret is never logged — grep test: `client_test.go` already has a transport-inspection test from slice 2 that captures request bodies; add one assertion that captured bodies never contain the test secret for any of the new endpoints
- [x] 10.6 Skim `internal/pveclient/` — every new exported method has a godoc comment naming the HTTP method and path it hits. No method is undocumented
