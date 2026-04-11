## D1. `request()` vs `requestForm()` — don't widen the existing signature

The current `request(ctx, method, path, query)` signature has four
callers (`GetVersion`, `ListNodes`, `ListTemplates`, `ListStorage`,
`ListBridges`). Widening it to `request(ctx, method, path, query, body)`
means touching all of them. Pointless churn.

**Decision:** add a sibling `requestForm(ctx, method, path, form url.Values) ([]byte, error)`
that encodes `form` as `application/x-www-form-urlencoded` into the
request body. GET-style callers keep using `request()`; POST/PUT/DELETE
callers with form data use `requestForm()`.

```go
func (c *Client) requestForm(ctx, method, path string, form url.Values) ([]byte, error) {
    var body io.Reader
    if len(form) > 0 {
        body = strings.NewReader(form.Encode())
    }
    req, _ := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
    req.Header.Set("Authorization", ...)
    req.Header.Set("Accept", "application/json")
    if len(form) > 0 {
        req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    }
    // ... same status-code switch as request()
}
```

The status-code handling duplicates a few lines from `request()`. We
could factor it out into a `do(req)` helper, but that's a 10-line
function with one caller growing to two. Not worth the indirection.

## D2. Return UPID strings for async operations

PVE's write endpoints return a **UPID** — a unique per-task identifier —
in the response `data` field, and the actual work happens asynchronously
on the node. `Clone`, `Start`, `Delete` all return a UPID. Callers that
want to wait pass the UPID back into `WaitTask`.

```go
func (c *Client) Clone(...) (upid string, err error)
func (c *Client) Start(...) (upid string, err error)
func (c *Client) Delete(...) (upid string, err error)
func (c *Client) WaitTask(ctx, node, upid, timeout) error
```

**Alternative considered:** make each write method block internally
(call WaitTask itself). Rejected — some callers want to fire-and-
forget (`pmox delete --async` maybe, or tests), and the non-blocking
shape lets callers run multiple tasks in parallel and wait for all of
them. Blocking can always be wrapped; unwrapping a blocking call is
harder.

**`Resize` and `SetConfig`** return no UPID — those endpoints complete
synchronously on the PVE API side, so their signatures are just
`error`.

## D3. `WaitTask` polling strategy

```
┌──────────────────────────────────────────────────────┐
│ GET /nodes/{node}/tasks/{upid}/status                │
│ loop:                                                │
│   parse response — field "status" is "running"|"stopped" │
│   if "stopped":                                      │
│     if "exitstatus" == "OK": return nil              │
│     else: return ErrAPIError with exitstatus text    │
│   sleep 500ms                                        │
│   if ctx.Done: return ctx.Err                        │
│   if time.Since(start) > timeout: return ErrTimeout  │
└──────────────────────────────────────────────────────┘
```

**Poll interval:** 500ms. Fast enough that a 2-second clone doesn't
feel sluggish, slow enough that a 60-second clone doesn't hammer the
API. Not tunable — launch will be the sole caller for a while and
can grow knobs later if needed.

**Timeout:** caller-supplied. Typical callers: Clone = 120s,
Start = 60s, Delete = 60s. Agent network-get (separate helper, see
D6) has its own longer timeout because waiting on the guest agent is
the slow part of `launch`.

**Add to `errors.go`:** `var ErrTimeout = errors.New("operation timed out")`.
Mapped to `ExitGeneric` for now — launch will print a specific message
before returning so the exit code is less important than the surface.

## D4. Status parsing — one shape per endpoint

Each endpoint has its own response shape and its own Go type. No
attempt to generalize — the shapes don't overlap meaningfully and a
single `Status` god-struct would bury the useful fields.

```go
type VMStatus struct {
    Status    string  `json:"status"`     // "running" | "stopped"
    QMPStatus string  `json:"qmpstatus"`  // finer-grained status
    VMID      int     `json:"vmid"`
    Name      string  `json:"name"`
    Uptime    int64   `json:"uptime"`
    CPUs      int     `json:"cpus"`
    Mem       int64   `json:"mem"`
    MaxMem    int64   `json:"maxmem"`
}

type AgentIface struct {
    Name        string           `json:"name"`            // "eth0", "lo", ...
    HardwareAddr string          `json:"hardware-address"`
    IPAddresses  []AgentIPAddr   `json:"ip-addresses"`
}

type AgentIPAddr struct {
    IPAddressType string `json:"ip-address-type"` // "ipv4" | "ipv6"
    IPAddress     string `json:"ip-address"`
    Prefix        int    `json:"prefix"`
}
```

Types live in the file of the endpoint that returns them
(`vm.go`, `agent.go`). Not in a central `types.go` — that's
premature centralization.

## D5. `SetConfig` — map input, not a struct

```go
func (c *Client) SetConfig(ctx, node, vmid, kv map[string]string) error
```

**Why a map, not a typed struct with `Memory`, `Cores`, `SSHKeys`, ...
fields:** the cloud-init key set is wide (`ciuser`, `cipassword`,
`sshkeys`, `ipconfig0..N`, `cicustom`, `searchdomain`, `nameserver`),
and the resource key set is wide too (`memory`, `cores`, `sockets`,
`cpu`, `agent`, `scsihw`, `net0`, `name`, `description`), and not
all of them are set every time. A map is one line of caller code and
one line of encoding. A struct would be 20 fields with `omitempty` on
each — not an improvement.

**Special handling:** `sshkeys` must be URL-encoded once *inside* the
value that `url.Values.Encode` then encodes again. PVE's API is
genuinely like this. The `SetConfig` method does the inner
`url.QueryEscape` for the `sshkeys` key specifically before building
the form. Documented in a comment above the method; tested with a
realistic ed25519 pubkey to pin the double-encoding.

## D6. `AgentNetwork` is not a task wait

The guest-agent endpoint returns a synchronous JSON payload — no
UPID, no task to wait on. If the guest agent isn't running yet, PVE
returns an error (typically 500 with "QEMU guest agent is not
running"). Callers that want to *wait* for the agent (launch does)
have to retry `AgentNetwork` themselves until they get a non-error
response or the context expires.

**Decision:** `AgentNetwork` is a single-shot call. No built-in
retry. The launch slice will wrap it in its own retry loop because
the retry policy is launch-specific (how long to wait, how to
distinguish "agent not ready" from "network broken"). Keeping
`AgentNetwork` simple here means the client package has no
time.Sleep in it and stays easy to test.

## D7. `nil` body and DELETE semantics

`DELETE /nodes/{node}/qemu/{vmid}` takes no body. Using `requestForm`
with an empty `url.Values` still sets the Content-Type header, which
PVE ignores but feels wrong. Two options:

- A) call `request()` (the existing no-body helper) with method DELETE
- B) call `requestForm()` with an empty map and accept the stray header

**Decision:** A. `request()` already handles body-less requests with
any HTTP verb — it's just named "request". Delete uses it. Same for
`NextID` (GET) and `GetStatus` (GET) and `AgentNetwork` (GET).
`requestForm()` is only for POST/PUT with an actual form body.

## D8. Testing with `httptest.Server`

Every new endpoint gets a test file that spins up an `httptest.Server`
and asserts:
- happy path: correct method, path, body, returns parsed result
- one error path per endpoint (401 → ErrUnauthorized, 500 →
  ErrAPIError, or a malformed payload)

`WaitTask` gets its own multi-step test: the mock server returns
`{"data":{"status":"running"}}` on the first two polls and
`{"data":{"status":"stopped","exitstatus":"OK"}}` on the third.
Assert WaitTask returns nil in under ~2s. A failure variant asserts
`exitstatus: "clone failed: destination VMID 200 already exists"`
bubbles up in the error text.

**Fixtures:** store realistic PVE response bodies under
`internal/pveclient/testdata/` — one JSON file per endpoint.
Hand-crafted from the PVE API docs, not captured from a real cluster
(so they're stable and reviewable).

## D9. No retries, no backoff, no circuit breaker

This client is consumed by a human-driven CLI. If the PVE API is
down, the user should see a clear error immediately, not a 30-second
hang while we retry. No retries in this slice. Launch may add its
own retry around `AgentNetwork` (D6) because that's a different
semantic — waiting for a guest-level service to come up, not
retrying a flaky network.

If this turns out wrong, it's a 20-line fix in `request()` later.
Don't build what we don't need.
