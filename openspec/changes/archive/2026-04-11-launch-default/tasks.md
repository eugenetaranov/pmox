## 1. Package scaffolding

- [x] 1.1 Create `internal/launch/` with `launch.go`, `cloudinit.go`, `ip.go`, `ssh.go`
- [x] 1.2 Declare `type Options struct { Client *pveclient.Client; Node, Name, User, SSHPubKey, TemplateName string; TemplateID, CPU, MemMB int; DiskSize, Storage, Bridge string; Wait time.Duration; NoWaitSSH bool; Stderr io.Writer; Verbose bool }` in `launch.go`
- [x] 1.3 Declare `type Result struct { VMID int; IP string }` for the state-machine return
- [x] 1.4 Add `func Run(ctx context.Context, opts Options) (*Result, error)` — signature only, `panic("TODO")` body

## 2. State machine — phases 1–3 (nextid, clone, tag)

- [x] 2.1 Phase 1: call `opts.Client.NextID(ctx)`; wrap error as `fmt.Errorf("allocate vmid: %w", err)`
- [x] 2.2 Phase 2: call `opts.Client.Clone(ctx, opts.Node, opts.TemplateID, vmid, opts.Name)`; wrap error; call `WaitTask(ctx, opts.Node, upid, 120*time.Second)`
- [x] 2.3 Phase 3: call `opts.Client.SetConfig(ctx, opts.Node, vmid, map[string]string{"tags": "pmox"})`. On error, return `fmt.Errorf("tag vm %d: %w (vm exists on cluster, run pmox delete %d)", vmid, err, vmid)`
- [x] 2.4 Test `TestRun_TagBeforeResize`: stateful `httptest.Server` asserts that the first `SetConfig` body contains only `tags=pmox` and is received before any `Resize` call

## 3. State machine — phases 4–6 (resize, config, start)

- [x] 3.1 Phase 4: call `Resize(ctx, node, vmid, "scsi0", opts.DiskSize)`; wrap error as `"resize disk: %w"`
- [x] 3.2 Phase 5: build the config kv map via `cloudinit.BuildBuiltinKV(opts, vmid)` and call `SetConfig`; wrap error as `"push cloud-init config: %w"`
- [x] 3.3 Phase 6: call `Start`, then `WaitTask(ctx, node, upid, 60*time.Second)`
- [x] 3.4 Test `TestRun_ConfigKVContainsRequiredKeys`: assert the second SetConfig body contains `ciuser`, `sshkeys`, `ipconfig0=ip=dhcp`, `agent=1`, `memory`, `cores`, `name`

## 4. cloudinit.go — built-in kv builder

- [x] 4.1 Create `internal/launch/cloudinit.go` with `func BuildBuiltinKV(opts Options, vmid int) map[string]string`
- [x] 4.2 The function returns a map with exactly these keys: `name`, `memory`, `cores`, `agent`, `ciuser`, `sshkeys`, `ipconfig0`
- [x] 4.3 `agent` is always `"1"`, `ipconfig0` is always `"ip=dhcp"`, `sshkeys` is the raw pubkey string (SetConfig handles double-encoding)
- [x] 4.4 Table-driven test `TestBuildBuiltinKV` covering: default user, custom user via `opts.User`, multi-line pubkey file content gets trimmed
- [x] 4.5 Test `TestBuildBuiltinKV_NoCicustom`: assert the returned map does not contain a `cicustom` key — that's slice 7's territory

## 5. ip.go — AgentNetwork poll + IP picker

- [x] 5.1 Create `internal/launch/ip.go`
- [x] 5.2 Package-level constant `pollInterval = 1 * time.Second`
- [x] 5.3 `func WaitForIP(ctx context.Context, c *pveclient.Client, node string, vmid int, timeout time.Duration) (string, error)` — loop per design D5
- [x] 5.4 Timeout message: `"qemu-guest-agent not responding on VM %d; install qemu-guest-agent in your template and re-run launch"`
- [x] 5.5 Context cancellation returns `ctx.Err()` unchanged
- [x] 5.6 `func pickIPv4(ifaces []pveclient.AgentIface) string` — implements D-T3 heuristic
- [x] 5.7 Skip prefixes list: `lo`, `docker`, `br-`, `veth`, `cni`, `virbr`, `tun` (exact prefix match via `strings.HasPrefix`)
- [x] 5.8 Exclude addresses in `127.0.0.0/8` and `169.254.0.0/16` via `net.ParseIP` + hard-coded checks
- [x] 5.9 Fallback: if nothing survives, scan all interfaces for any non-loopback non-link-local IPv4
- [x] 5.10 Return `""` when no IPv4 is usable — caller keeps polling

## 6. ip_test.go — picker table tests + poll loop

- [x] 6.1 Create `internal/launch/ip_test.go`
- [x] 6.2 `TestPickIPv4` table cases:
  - single `eth0` with one IPv4 → returns it
  - `eth0` + `docker0` → returns eth0's IPv4
  - `eth0` with IPv6 only → returns ""
  - only `169.254.x.y` → returns ""
  - fallback: all interfaces filtered out by prefix, single `ens3` with valid IPv4 → returns it
  - empty `ifaces` slice → returns ""
- [x] 6.3 `TestWaitForIP_HappyPath`: stateful mock returns empty result twice then a valid interface — assert returned IP matches fixture
- [x] 6.4 `TestWaitForIP_Timeout`: mock always returns agent-not-running error, call with 2s timeout, assert error contains `qemu-guest-agent not responding on VM`
- [x] 6.5 `TestWaitForIP_ContextCancel`: pre-cancelled context, assert `ctx.Err()` returned

## 7. ssh.go — handshake wait

- [x] 7.1 Create `internal/launch/ssh.go`
- [x] 7.2 Add `golang.org/x/crypto/ssh` to `go.mod` via `go get`
- [x] 7.3 `func WaitForSSH(ctx context.Context, ip string, timeout time.Duration) error`
- [x] 7.4 Loop: dial `net.Dial("tcp", ip+":22")` with 2s per-attempt timeout, exponential backoff starting at 500ms capped at 5s
- [x] 7.5 On successful dial, run `ssh.NewClientConn` with `ClientConfig{User: "pmox", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5*time.Second}` and no auth
- [x] 7.6 Treat any error mentioning "unable to authenticate" / "no supported methods" / "ssh:" as success (sshd answered the banner, which is what we care about)
- [x] 7.7 Treat `io.EOF` and "connection reset" during handshake as retry-worthy
- [x] 7.8 On ctx done or timeout, return wrapped error `"wait for ssh on %s: %w"`

## 8. ssh_test.go — fake ssh listener

- [x] 8.1 Create `internal/launch/ssh_test.go`
- [x] 8.2 `TestWaitForSSH_HandshakeSuccess`: start a goroutine that listens on `127.0.0.1:0`, runs `ssh.NewServerConn` with a generated host key, and closes the connection after the banner exchange. Assert `WaitForSSH` returns nil within 5s
- [x] 8.3 `TestWaitForSSH_Timeout`: no listener, call `WaitForSSH` with 1s timeout, assert error contains `wait for ssh on`
- [x] 8.4 `TestWaitForSSH_TCPOnlyNotEnough`: start a raw TCP listener that accepts then immediately closes — assert `WaitForSSH` keeps retrying and eventually times out (no banner exchange counts as failure)

## 9. cmd/pmox/launch.go — Cobra wiring

- [x] 9.1 Create `cmd/pmox/launch.go` with a `newLaunchCmd()` returning `*cobra.Command`
- [x] 9.2 Register in `cmd/pmox/main.go` via `rootCmd.AddCommand(newLaunchCmd())`
- [x] 9.3 Flags (all persistent on this command): `--cpu`, `--mem`, `--disk`, `--template`, `--storage`, `--node`, `--bridge`, `--user`, `--ssh-key`, `--wait`, `--no-wait-ssh`
- [x] 9.4 Use `flagOrEnv(cmd, "template", "PMOX_TEMPLATE")` pattern for flags that map to env vars
- [x] 9.5 `RunE`: resolve server via `server.Resolve`, load config, build `launch.Options`, call `launch.Run`, map error → exit code via `exitcode.From`
- [x] 9.6 Pre-run hook: emit the D-T4 server log line to stderr when `-v` is set
- [x] 9.7 Print result on stdout: `fmt.Printf("launched %s (vmid=%d, ip=%s)\n", name, r.VMID, r.IP)`
- [x] 9.8 `--template` resolution: if the value parses as int, use as VMID; else look up via `ListTemplates` against the resolved node

## 10. Flag-default resolution

- [x] 10.1 In `cmd/pmox/launch.go`, after flag parsing and server resolution, layer defaults: CLI flag > configured server default > built-in literal
- [x] 10.2 Built-in literals: `CPU=2`, `MemMB=2048`, `DiskSize="20G"`, `Wait=3*time.Minute`, `User="pmox"`
- [x] 10.3 Missing required with no default surfaces `ExitConfig` with a message naming the missing setting and suggesting `pmox configure`
- [x] 10.4 Test `TestResolveDefaults` with table cases covering each fallback tier

## 11. Integration test — full state-machine walk

- [x] 11.1 Create `internal/launch/launch_test.go`
- [x] 11.2 Build a shared `fakePVE` helper: `httptest.Server` dispatching by path, stateful closure to advance the agent-network response
- [x] 11.3 `TestRun_HappyPath`: assert sequence of endpoint hits matches: `GET /cluster/nextid`, `POST /nodes/{node}/qemu/{tmpl}/clone`, `GET /nodes/{node}/tasks/{upid}/status`, `POST /nodes/{node}/qemu/{vmid}/config` (tag), `PUT /resize`, `POST /config` (full kv), `POST /status/start`, `GET /tasks/{upid}/status`, `GET /agent/network-get-interfaces` (1+)
- [x] 11.4 Assert `Run` returns `*Result` with non-zero VMID and the mocked IP
- [x] 11.5 `TestRun_TagErrorMentionsCleanup`: mock returns 500 on the tag SetConfig; assert error message contains `run pmox delete`
- [x] 11.6 `TestRun_StartFailsNoRollback`: mock returns 500 on Start; assert `Run` returns an error AND the test server records zero `DELETE` calls
- [x] 11.7 `TestRun_WaitIPTimeout`: mock always returns agent-not-running; assert error message contains `qemu-guest-agent not responding on VM`

## 12. Verbose log line

- [x] 12.1 In `cmd/pmox/launch.go`, before the first API call, when `cmd.Flags().GetBool("verbose")` is true, write one line to stderr: `using server <url> (<reason>)`
- [x] 12.2 `reason` comes from `server.Resolved.Source` — one of `--server flag`, `PMOX_SERVER env var`, `single configured`, `interactive picker`
- [x] 12.3 Test in `cmd/pmox/launch_test.go`: capture stderr, assert the log line appears exactly once on a dry-run that short-circuits before real API calls

## 13. Exit code mapping sanity

- [x] 13.1 Verify `internal/exitcode.From` already maps `pveclient.ErrUnauthorized → ExitAuth`, `ErrNotFound → ExitConfig`, `ErrAPIError → ExitRemote`, `ErrTimeout → ExitTimeout`. If any are missing, add them — they should have shipped in slice 1
- [x] 13.2 Test `TestExitCodeMapping` in `internal/exitcode/exitcode_test.go` (or extend existing) covering each of those classes

## 14. Verification

- [x] 14.1 `go build ./...` passes
- [x] 14.2 `go test ./internal/launch/... -race` passes
- [x] 14.3 `go test ./... -race` passes (full suite)
- [x] 14.4 `make lint` passes
- [x] 14.5 `pmox launch --help` shows every flag from task 9.3 with a one-line description
- [x] 14.6 `pmox --help` lists `launch` in the command table
- [x] 14.7 README gets no updates in this slice — that's slice 9's scope
- [x] 14.8 Manual smoke test against a real PVE cluster: `pmox launch smoke-test` completes in <3 minutes, `ssh pmox@<ip>` succeeds, `pmox delete smoke-test` (stubbed until slice 6; for this slice, manual PVE UI cleanup is fine)
