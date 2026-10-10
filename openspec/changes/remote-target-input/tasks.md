## 1. Target and local path fields (`internal/tui/target`)

- [x] 1.1 Model over bubbles `textinput`: one value split at the first `:`, VM list under the input (name, status, IP; filtered by VM-part prefix; capped rows + "… N more")
- [x] 1.2 Ghost text and Tab: unique VM → `<name>:` + default path; several → longest common prefix, then cycle; → / End accept at end of value
- [x] 1.3 ↑/↓ replace the VM part with the highlighted VM and keep the path; free editing otherwise; Esc back/cancel; Ctrl-C exit 130
- [x] 1.4 Single-VM mode: open as `<vm>:` with cursor in the path, VM part dimmed, no list; list returns when the user edits the VM part
- [x] 1.5 Validation on Enter: exactly one VM by name or VMID, empty path → default; inline errors keep the value
- [x] 1.5a Remote paths: neither `/` nor `~/` → relative to the login home (field, explicit args, completion, mkdir check and prompt showing `~/…`)
- [x] 1.6 Remote path completion hook: an injected lister (one SSH `ls -1pA` per directory, 2s timeout, cached); hint when the VM is stopped or the listing fails
- [x] 1.7 Local path field: greyed default, Tab completion of local segments, directories-only option
- [x] 1.8 teatest coverage: narrowing, Tab unique/common-prefix/cycle, ↑/↓ keeps path, backspace into the VM part, single-VM open, unknown VM error, empty path default, local completion

## 2. Missing destination directory

- [x] 2.1 Shared helper: decide which directory to check (destination when the source is a directory or the destination ends in `/`, else its parent); remote `test -d` over SSH, local `os.Stat`
- [x] 2.2 Confirm "Create <path> on <vm>?" (default Yes) on a terminal; `--mkdir` creates without asking; without a terminal and without `--mkdir`, fail naming the path and the flag
- [x] 2.3 Remote create: `mkdir -p` falling back to `sudo -n install -d -o <user> -g <group>`; local `os.MkdirAll`; errors name the path and suggest a writable location
- [x] 2.4 Unit tests: directory/parent choice, prompt yes/no, `--mkdir`, non-TTY error, sudo fallback command line

## 3. Commands

- [x] 3.1 mount: replace `resolveMountArgs` prompts with the local field + target field; one-argument form opens only the target field; default path `/mnt/<base name>`; `--mkdir` and the missing-directory check before the first sync
- [x] 3.2 cp and sync: replace the VM picker + path prompts in `resolveCpSyncArgs` with the fields in direction order; `--mkdir` and the check before scp/rsync
- [x] 3.3 sync: `-a` by default ahead of `--` flags; `--no-archive`
- [x] 3.4 umount with no arguments: read running mounts locally; none → "✓ No active mounts"; one → stop; several → multi-select of `<local> → <vm>:<path>`; non-TTY with several → usage error
- [x] 3.5 Update command tests (mount, cp, sync, umount) for the new prompts, flags and rsync arguments

## 4. Docs and verification

- [x] 4.1 README mount/cp/sync sections and llms.txt: interactive fields and keys, `/mnt/<dir>` default, `--mkdir`, sync `-a` / `--no-archive`, bare umount
- [x] 4.2 `go vet`, `task lint`, `go test -race ./...`, `openspec validate remote-target-input`
- [x] 4.3 Real cluster through a pty: bare mount with one and with several VMs, Tab and ↑/↓, mount into `/mnt/<dir>` (sudo create), `pmox sync . web1:~/project/pmox` into a missing directory, cp download into a missing local directory, bare umount with several mounts
  - Done on PVE 9.1.1 with testvm plus a scratch VM: single-VM field opened as `testvm:` with `/mnt/<dir>` suggested; `/mnt/rtitest` created with sudo, owned by ubuntu, and synced; two-VM list narrowed by typing, Tab completed the VM, ↓ swapped the VM and kept `/srv/data`; remote Tab completed `/et` → `/etc/` → `/etc/hostname`; `sync . testvm:project/pmoxtest` asked "Create ~/project/pmoxtest on testvm?", created it and copied the files (`-a`); without a terminal it stopped naming `--mkdir`; `cp --mkdir` download created a nested local directory; bare umount stopped the only mount, then reported "✓ No active mounts", and with two running offered both and stopped just the chosen one.
  - Seen, not caused by this change: when two pmox processes read the macOS keychain at the same moment (a mount's background child starting while another command runs), one can fail with "secret … not found in keychain".
