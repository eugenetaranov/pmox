# pmox examples

Runnable fixtures for the pmox feature set. Each file is small,
self-contained, and works against a default Ubuntu 24.04 template
built with `pmox create-template`.

## [cloud-init.yaml](./cloud-init.yaml)

A reference cloud-init user-data file. pmox does not take a
`--cloud-init` flag — it reads per-server cloud-init from
`~/.config/pmox/cloud-init/<slug>.yaml`, which `pmox configure`
writes on first run. Copy this file into that location (replacing
the placeholder SSH key) if you want to bootstrap a server by hand
instead of going through `configure`.

## [post-create.sh](./post-create.sh)

A minimal `--post-create` script. Reads `PMOX_IP` / `PMOX_USER` /
`PMOX_NAME` from the environment and SSHes into the just-launched VM
to run `cloud-init status --wait`, ensuring cloud-init has actually
finished before the launch command returns. Exit the script non-zero
and pmox prints a warning by default; pass `--strict-hooks` to
upgrade the failure to exit code 8.

```
pmox launch --post-create ./examples/post-create.sh web1
```

## [tack.yaml](./tack.yaml)

A minimal `tack` playbook that installs `htop` via the built-in `apt`
module. Invoked by pmox as `tack run ./tack.yaml -c ssh://<user>@<ip>
--ssh-key <identity> --auto-approve` after the VM is reachable.
Requires the `tack` binary on PATH — install it from
[tackhq/tack](https://github.com/tackhq/tack).

```
pmox launch --tack ./examples/tack.yaml web1
pmox apply web1   # re-run the same playbook later; see 'pmox apply --help'
                   # for the profile/--check/--tags flags this example
                   # doesn't need
```

## [ansible/playbook.yaml](./ansible/playbook.yaml)

A minimal Ansible playbook that installs `htop` on the launched VM.
pmox invokes `ansible-playbook` with an inline single-host inventory
(`-i <ip>,`), the configured SSH user, and the derived private key.
Requires `ansible-playbook` on PATH.

```
pmox launch --ansible ./examples/ansible/playbook.yaml web1
```

## Mix and match

The three hook flags (`--post-create`, `--tack`, `--ansible`) are
mutually exclusive — pick one per launch. If none of the hook flags
is set, pmox just prints the VM's IP and exits 0 once SSH is ready.
