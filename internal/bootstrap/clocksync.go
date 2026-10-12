package bootstrap

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// chronyPTP is the chrony drop-in ClockSync installs: read the Proxmox
// host's clock through the KVM PTP device, and step the clock whenever it
// is off by more than a second (after snapshot rollbacks, not just at boot).
const chronyPTP = `# Written by pmox (clock sync): read the Proxmox host's clock through the
# KVM PTP device, and step after snapshot rollbacks. Turn off with
# --no-clock-sync or clock_sync: false.
refclock PHC /dev/ptp0 poll 2 dpoll -2 offset 0 stratum 2 prefer
makestep 1 -1
`

// chronyPTPStaged is where the drop-in waits until the first boot knows
// whether /dev/ptp0 exists: chronyd won't start with a refclock on a
// missing device.
const chronyPTPStaged = "/usr/local/share/pmox/chrony-ptp.conf"

// ClockSyncCommand runs once at first boot (runcmd, after packages).
const ClockSyncCommand = "modprobe ptp_kvm 2>/dev/null || true; if [ -e /dev/ptp0 ]; then install -D -m 0644 " + chronyPTPStaged + " /etc/chrony/conf.d/pmox.conf && systemctl restart chrony; fi"

// ClockSync returns userData with chrony set up to follow the Proxmox
// host's clock: the chrony package, ptp_kvm loaded at boot, and a
// first-boot command that installs the drop-in when /dev/ptp0 exists.
// Without the device chrony keeps its default NTP pools.
func ClockSync(userData []byte) ([]byte, error) {
	return edit(userData, func(root *yaml.Node) error {
		pkgs, err := seq(root, "packages")
		if err != nil {
			return err
		}
		if !hasPackage(pkgs, "chrony") {
			pkgs.Content = append(pkgs.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "chrony"})
		}
		list, err := seq(root, "write_files")
		if err != nil {
			return err
		}
		if err := addFiles(list,
			File{Path: "/etc/modules-load.d/ptp_kvm.conf", Permissions: "0644", Content: []byte("ptp_kvm\n")},
			File{Path: chronyPTPStaged, Permissions: "0644", Content: []byte(chronyPTP)},
		); err != nil {
			return err
		}
		run, err := seq(root, "runcmd")
		if err != nil {
			return err
		}
		if !hasScalar(run, ClockSyncCommand) {
			run.Content = append(run.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: ClockSyncCommand})
		}
		return nil
	})
}

// hasPackage reports whether a cloud-init packages list installs name,
// as "name", "name=version" or [name, version].
func hasPackage(list *yaml.Node, name string) bool {
	for _, n := range list.Content {
		switch n.Kind {
		case yaml.ScalarNode:
			if n.Value == name || strings.HasPrefix(n.Value, name+"=") {
				return true
			}
		case yaml.SequenceNode:
			if len(n.Content) > 0 && n.Content[0].Value == name {
				return true
			}
		}
	}
	return false
}
