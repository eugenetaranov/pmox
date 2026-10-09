## ADDED Requirements

### Requirement: Opt-in system items
Every item in this capability SHALL be an unticked System-tab item. Each SHALL write its settings as drop-in files (`*.d/60-devbox.conf` or a similar name) rather than edit the distribution's main config file, and SHALL be idempotent.

#### Scenario: Nothing preselected
- **WHEN** the picker opens on a fresh VM
- **THEN** none of the system tuning items is ticked

### Requirement: Dev limits
A `devlimits` item SHALL:
- set `fs.inotify.max_user_watches=524288`, `fs.inotify.max_user_instances=1024`, `vm.max_map_count=262144` and `kernel.task_delayacct=1` in `/etc/sysctl.d/60-devbox.conf`, and apply them with `sysctl --system`;
- set nofile limits of 65536 soft and 1048576 hard in `/etc/security/limits.d/60-devbox.conf` and in `/etc/systemd/system.conf.d/60-devbox.conf`.

#### Scenario: Applied immediately
- **WHEN** `devlimits` is installed
- **THEN** `sysctl fs.inotify.max_user_instances` prints 1024 without a reboot

### Requirement: zram swap
A `zram` item SHALL install `systemd-zram-generator`, configure `zram0` with size `min(ram / 2, 8192)` and zstd compression, set `vm.swappiness=100`, and start the device.

#### Scenario: Swap active
- **WHEN** `zram` is installed on a VM without swap
- **THEN** `swapon --show` lists `/dev/zram0`

### Requirement: Journal size cap
A `journald` item SHALL set `SystemMaxUse=200M`, `SystemKeepFree=1G` and `MaxRetentionSec=2week` in a journald drop-in, and restart systemd-journald.

#### Scenario: Cap in effect
- **WHEN** `journald` is installed
- **THEN** `systemd-analyze cat-config systemd/journald.conf` shows `SystemMaxUse=200M`

### Requirement: Timezone and locale
A `tzlocale` item SHALL ask for a timezone, defaulting to the saved answer or else `Etc/UTC`, and validated against `timedatectl list-timezones`. It SHALL then set it, generate `en_US.UTF-8`, set it as `LANG`, and install `locales-all`. The timezone SHALL be saved as `A_TZ`.

#### Scenario: Mac SSH locale
- **WHEN** `tzlocale` is installed and a macOS client connects sending `LC_CTYPE=UTF-8`
- **THEN** perl and bash print no setlocale warnings

### Requirement: Docker daemon defaults
The `docker` item SHALL write `/etc/docker/daemon.json` from `conf/daemon.json` only when that file is absent, before docker is first started. The defaults SHALL include:
- the `local` log driver with max-size 10m and max-file 3
- `default-address-pools` of `10.200.0.0/16` with size 24
- `live-restore: true`

#### Scenario: Fresh docker
- **WHEN** `docker` is installed on a VM without `daemon.json`
- **THEN** `docker info` shows the `local` logging driver, and new networks are allocated from 10.200.0.0/16

#### Scenario: User daemon.json kept
- **WHEN** `/etc/docker/daemon.json` already exists
- **THEN** devbox-setup leaves it unchanged and does not restart docker

### Requirement: sysstat collects data
When the `debug` item is installed, sysstat collection SHALL be enabled (`ENABLED="true"`) and its service started.

#### Scenario: sar has data
- **WHEN** `debug` has been installed for more than 10 minutes
- **THEN** `sar` shows samples for today

### Requirement: etckeeper
An `etckeeper` item SHALL install etckeeper and SHALL be ordered first among the system items in the catalog, so that later changes to /etc are committed.

#### Scenario: Later changes tracked
- **WHEN** `etckeeper` and `journald` are installed in the same run
- **THEN** `git -C /etc log` contains a commit that adds the journald drop-in

### Requirement: Host-synced clock
A `chrony` item SHALL install chrony, load `ptp_kvm` at boot, and configure `refclock PHC /dev/ptp0 … prefer` and `makestep 1 -1` in a chrony drop-in, so the host clock is the selected source. When `/dev/ptp0` is absent, it SHALL fall back to the default NTP pools and print a note.

#### Scenario: After snapshot rollback
- **WHEN** a VM with `chrony` installed resumes with a clock several minutes off
- **THEN** chrony steps the clock to the host time within one poll interval

### Requirement: Admin package list hygiene
The `debug` item SHALL install `bind9-dnsutils` instead of `dnsutils` and `iotop-c` instead of `iotop`, and SHALL add `iperf3 ethtool traceroute nmap iftop pv ltrace lnav duf`. `htop` SHALL be removed from `cli`, since `btop` in `debug` covers it, and `build-essential` SHALL be listed only in `essentials`.

#### Scenario: Package list
- **WHEN** the picker shows the `debug` item
- **THEN** its source column lists `bind9-dnsutils` and `iotop-c`, and lists neither `dnsutils` nor `iotop`
