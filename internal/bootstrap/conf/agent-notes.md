# This machine

- A disposable Ubuntu VM on Proxmox, created by pmox. Breaking it is cheap;
  it can be recreated.
- The user has passwordless sudo.
- Developer tools are installed per user with mise (`mise ls`, `mise use -g
  <tool>`); system packages with apt.
- `~/devbox-setup.txt` lists what devbox-setup installed and how;
  `sudo devbox-setup` adds more.
- Kubernetes config, if any, is in `~/.kube/config`.
