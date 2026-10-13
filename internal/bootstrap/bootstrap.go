// Package bootstrap prepares the VMs pmox launches: Inject and ClockSync
// add the small first-boot pieces to the VM's cloud-init, and the yardo
// commands install yardo (github.com/eugenetaranov/yardo, the on-VM
// setup tool) from its release and ship a team's own yardo items.
package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// yardoRepo is where yardo's releases are.
const yardoRepo = "https://github.com/eugenetaranov/yardo"

// Yardo is the yardo release to install: the latest, unless
// PMOX_YARDO_VERSION names one (a tag). pmox doesn't pin yardo: it is its
// own project, released on its own.
func Yardo() string {
	if v := strings.TrimSpace(os.Getenv("PMOX_YARDO_VERSION")); v != "" {
		return v
	}
	return "latest"
}

// YardoInstallCommand is the remote command that installs yardo's release
// version ("latest" for the newest) with its install.sh, which checks the
// download against the release's checksums. Saved choices are kept.
func YardoInstallCommand(version string) string {
	url := yardoRepo + "/releases/download/" + version + "/install.sh"
	args := " --version " + version
	if version == "latest" {
		url, args = yardoRepo+"/releases/latest/download/install.sh", ""
	}
	return "sudo -n sh -c '(command -v curl >/dev/null && curl -fsSL " + url + " || wget -qO- " + url + ") | sh -s --" + args + "'"
}

// TeamDir names the subdirectories of a team tarball: catalog.d/*.toml go
// to yardo's /etc/yardo/catalog.d, mcp-catalog/*.spec to the MCP catalog.
const (
	TeamCatalog = "catalog.d"
	TeamMCP     = "mcp-catalog"
)

// TeamSyncCommand is the remote command that syncs a TeamTarball (on
// stdin) into place: the files pmox copied before (listed in .team-files)
// but no longer ships go; other files there are left alone.
const TeamSyncCommand = `sudo -n sh -c 'set -e
t=$(mktemp -d); trap "rm -rf $t" EXIT
tar -xzf - -C "$t"
sync_team() {
  src=$1 dst=$2 ext=$3 man=$2/.team-files
  [ -d "$src" ] || [ -f "$man" ] || return 0
  mkdir -p "$dst"
  if [ -f "$man" ]; then
    while IFS= read -r f; do
      case "$f" in ""|.*|*/*) continue ;; esac
      [ -f "$src/$f" ] || rm -f "$dst/$f"
    done < "$man"
  fi
  : > "$man.new"
  for p in "$src"/*"$ext"; do
    [ -f "$p" ] || continue
    f=${p##*/}
    install -m 0644 "$p" "$dst/$f"
    echo "$f" >> "$man.new"
  done
  mv -f "$man.new" "$man"
}
sync_team "$t/catalog.d" /etc/yardo/catalog.d .toml
sync_team "$t/mcp-catalog" /usr/local/share/mcp-catalog .spec'`

// File is one file: a cloud-init write_files entry, or a team file.
type File struct {
	Path        string
	Permissions string
	Content     []byte
}

// TeamTarball is a gzip'd tar of team files (Path relative: catalog.d/x.toml,
// mcp-catalog/y.spec) for TeamSyncCommand.
func TeamTarball(files []File) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	now := time.Now()
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: f.Path, Mode: 0o644, Size: int64(len(f.Content)), ModTime: now}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// noAutoUpgrades turns off unattended-upgrades on the dev VM: on first boot
// it holds the dpkg lock for minutes and breaks installs (yardo's, and
// vendor scripts' own apt-get calls).
var noAutoUpgrades = File{
	Path:        "/etc/apt/apt.conf.d/20auto-upgrades",
	Permissions: "0644",
	Content: []byte(`// Written by pmox: no unattended upgrades on dev VMs.
APT::Periodic::Update-Package-Lists "0";
APT::Periodic::Unattended-Upgrade "0";
`),
}

// AutoUpgradesMarker is created by yardo's "autoupdates" item: the user
// opted into automatic security updates, so the bootcmd stands down.
const AutoUpgradesMarker = "/etc/yardo/auto-upgrades"

// stopAutoUpgrades runs as a bootcmd (every boot), before the apt timers
// can fire, unless the user opted in.
const stopAutoUpgrades = "[ -e " + AutoUpgradesMarker + " ] || systemctl disable --now apt-daily.timer apt-daily-upgrade.timer apt-daily-upgrade.service unattended-upgrades.service >/dev/null 2>&1 || true"

// oldStopAutoUpgrades are the forms older cloud-init files carry: unguarded,
// and guarded by the marker's path from before yardo was split out of
// pmox (devbox-setup).
// Inject replaces them so the opt-in survives reboots.
var oldStopAutoUpgrades = []string{
	"systemctl disable --now apt-daily.timer apt-daily-upgrade.timer apt-daily-upgrade.service unattended-upgrades.service >/dev/null 2>&1 || true",
	"[ -e /etc/" + "devbox-setup" + "/auto-upgrades ] || systemctl disable --now apt-daily.timer apt-daily-upgrade.timer apt-daily-upgrade.service unattended-upgrades.service >/dev/null 2>&1 || true",
}

// seq returns root's sequence under key, creating it (or converting an
// empty value) when needed.
func seq(root *yaml.Node, key string) (*yaml.Node, error) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != key {
			continue
		}
		v := root.Content[i+1]
		switch {
		case v.Kind == yaml.SequenceNode:
			return v, nil
		case v.Tag == "!!null":
			v.Kind, v.Tag, v.Value = yaml.SequenceNode, "", ""
			return v, nil
		default:
			return nil, fmt.Errorf("cloud-init %s is not a list", key)
		}
	}
	v := &yaml.Node{Kind: yaml.SequenceNode}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	return v, nil
}

func hasScalar(list *yaml.Node, v string) bool {
	for _, n := range list.Content {
		if n.Kind == yaml.ScalarNode && n.Value == v {
			return true
		}
	}
	return false
}

// Inject returns userData with unattended upgrades turned off (bootcmd +
// an apt config file), so they don't hold the dpkg lock while yardo
// installs. Everything else is kept as is. userData must be
// a cloud-config mapping document.
func Inject(userData []byte) ([]byte, error) {
	return edit(userData, func(root *yaml.Node) error {
		list, err := seq(root, "write_files")
		if err != nil {
			return err
		}
		boot, err := seq(root, "bootcmd")
		if err != nil {
			return err
		}
		for _, n := range boot.Content {
			if n.Kind == yaml.ScalarNode && slices.Contains(oldStopAutoUpgrades, n.Value) {
				n.Value = stopAutoUpgrades
			}
		}
		if !hasScalar(boot, stopAutoUpgrades) { // the starter template has it already
			boot.Content = append(boot.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: stopAutoUpgrades})
		}
		return addFiles(list, noAutoUpgrades)
	})
}

// addFiles appends write_files entries for fs.
func addFiles(list *yaml.Node, fs ...File) error {
	for _, f := range fs {
		enc, err := gzipB64(f.Content)
		if err != nil {
			return err
		}
		entry := &yaml.Node{Kind: yaml.MappingNode}
		for _, kv := range [][2]string{
			{"path", f.Path},
			{"permissions", f.Permissions},
			{"owner", "root:root"},
			{"encoding", "gz+b64"},
			{"content", enc},
		} {
			v := &yaml.Node{Kind: yaml.ScalarNode, Value: kv[1]}
			if kv[0] == "permissions" {
				v.Style = yaml.SingleQuotedStyle // keep the leading 0
			}
			entry.Content = append(entry.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: kv[0]}, v)
		}
		list.Content = append(list.Content, entry)
	}
	return nil
}

// edit parses userData, lets change edit its top-level mapping, and
// encodes the result as cloud-config.
func edit(userData []byte, change func(root *yaml.Node) error) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(userData, &doc); err != nil {
		return nil, fmt.Errorf("parse cloud-init: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("cloud-init is not a YAML mapping")
	}
	if err := change(doc.Content[0]); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode cloud-init: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	// cloud-init only treats the file as cloud-config with this first line.
	if !bytes.HasPrefix(out, []byte("#cloud-config")) {
		out = append([]byte("#cloud-config\n"), out...)
	}
	return out, nil
}

func gzipB64(b []byte) (string, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := zw.Write(b); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
