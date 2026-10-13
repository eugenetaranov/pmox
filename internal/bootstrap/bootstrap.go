// Package bootstrap ships devbox-setup — an interactive on-node installer
// (dev tools, Docker, mise, Claude Code, MCPJungle + MCP servers) — onto
// the VMs pmox launches: Tarball holds its files, which pmox copies over
// SSH once the VM is up. Inject and ClockSync add the small first-boot
// pieces to the VM's cloud-init.
//
// The script lives here. assets/ (mcp-sync and the MCP catalog) are copies
// from github.com/tackhq/tack-roles, refreshed with `task bootstrap:sync`,
// so devbox-setup and the tack roles install the same MCP servers. conf/
// holds devbox-setup's own files (zshrc and vimrc, the shell drop-in, git,
// tmux, docker, system drop-ins); the sync never touches it.
package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed devbox-setup picker.py mise-registry.tsv install.sh assets conf
var files embed.FS

//go:embed gobin
var gobins embed.FS

// Bins holds the Go devbox-setup builds (devbox-setup-linux-<arch>.gz);
// tests replace it. Without any, only the bash devbox-setup is shipped.
var Bins fs.FS = mustSub(gobins, "gobin")

func mustSub(f fs.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}

// LegacyScriptPath is where the bash devbox-setup lands when the Go one
// is installed: the Go binary runs it for steps not ported yet.
const LegacyScriptPath = ShareDir + "/devbox-setup.sh"

// goBuilds returns the embedded Go builds by file name.
func goBuilds() map[string][]byte {
	out := map[string][]byte{}
	names, _ := fs.Glob(Bins, "devbox-setup-linux-*.gz")
	for _, n := range names {
		if b, err := fs.ReadFile(Bins, n); err == nil && len(b) > 0 {
			out[n] = b
		}
	}
	return out
}

// HasGoBuilds reports whether this pmox ships the Go devbox-setup.
func HasGoBuilds() bool { return len(goBuilds()) > 0 }

// ShareDir is where the assets land on the VM; the script reads them there.
const ShareDir = "/usr/local/share/devbox-setup"

// ScriptPath is where the script lands on the VM.
const ScriptPath = "/usr/local/bin/devbox-setup"

// File is one write_files entry.
type File struct {
	Path        string
	Permissions string
	Content     []byte
}

// Files returns the script and its assets with their on-VM paths.
func Files() []File {
	script, err := files.ReadFile("devbox-setup")
	if err != nil {
		panic(err) // embedded; cannot fail
	}
	picker, err := files.ReadFile("picker.py")
	if err != nil {
		panic(err)
	}
	registry, err := files.ReadFile("mise-registry.tsv")
	if err != nil {
		panic(err)
	}
	install, err := files.ReadFile("install.sh")
	if err != nil {
		panic(err)
	}
	// With the Go builds, install.sh puts the right one at ScriptPath and
	// the bash script stays in the share directory for its legacy steps.
	scriptAt := ScriptPath
	builds := goBuilds()
	if len(builds) > 0 {
		scriptAt = LegacyScriptPath
	}
	out := []File{
		{Path: scriptAt, Permissions: "0755", Content: script},
		{Path: path.Join(ShareDir, "install.sh"), Permissions: "0755", Content: install},
		{Path: path.Join(ShareDir, "picker.py"), Permissions: "0755", Content: picker},
		{Path: path.Join(ShareDir, "mise-registry.tsv"), Permissions: "0644", Content: registry},
	}
	_ = fs.WalkDir(files, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := files.ReadFile(p)
		perm := "0644"
		if path.Base(p) == "mcp-sync" {
			perm = "0755"
		}
		out = append(out, File{Path: path.Join(ShareDir, strings.TrimPrefix(p, "assets/")), Permissions: perm, Content: b})
		return nil
	})
	_ = fs.WalkDir(files, "conf", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := files.ReadFile(p)
		out = append(out, File{Path: path.Join(ShareDir, p), Permissions: "0644", Content: b})
		return nil
	})
	names := make([]string, 0, len(builds))
	for n := range builds {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, File{Path: path.Join(ShareDir, "bin", n), Permissions: "0644", Content: builds[n]})
	}
	return out
}

// noAutoUpgrades turns off unattended-upgrades on the dev VM: on first boot
// it holds the dpkg lock for minutes and breaks installs (devbox-setup's,
// and vendor scripts' own apt-get calls).
var noAutoUpgrades = File{
	Path:        "/etc/apt/apt.conf.d/20auto-upgrades",
	Permissions: "0644",
	Content: []byte(`// Written by pmox: no unattended upgrades on dev VMs.
APT::Periodic::Update-Package-Lists "0";
APT::Periodic::Unattended-Upgrade "0";
`),
}

// AutoUpgradesMarker is created by devbox-setup's "autoupdates" item: the
// user opted into automatic security updates, so the bootcmd stands down.
const AutoUpgradesMarker = "/etc/devbox-setup/auto-upgrades"

// stopAutoUpgrades runs as a bootcmd (every boot), before the apt timers
// can fire, unless the user opted in.
const stopAutoUpgrades = "[ -e " + AutoUpgradesMarker + " ] || systemctl disable --now apt-daily.timer apt-daily-upgrade.timer apt-daily-upgrade.service unattended-upgrades.service >/dev/null 2>&1 || true"

// legacyStopAutoUpgrades is the unguarded form older cloud-init files carry;
// Inject replaces it so the opt-in survives reboots.
const legacyStopAutoUpgrades = "systemctl disable --now apt-daily.timer apt-daily-upgrade.timer apt-daily-upgrade.service unattended-upgrades.service >/dev/null 2>&1 || true"

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
// an apt config file), so they don't hold the dpkg lock while
// devbox-setup installs. Everything else is kept as is. userData must be
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
			if n.Kind == yaml.ScalarNode && n.Value == legacyStopAutoUpgrades {
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

// Tarball returns devbox-setup's files as a gzip'd tar to extract at /
// as root. It holds file entries only, so extracting never changes the
// modes of existing directories such as /usr/local.
func Tarball(extra ...File) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	now := time.Now()
	for _, f := range append(Files(), extra...) {
		mode, err := strconv.ParseInt(f.Permissions, 8, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: permissions %q: %w", f.Path, f.Permissions, err)
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     strings.TrimPrefix(f.Path, "/"),
			Mode:     mode,
			Size:     int64(len(f.Content)),
			ModTime:  now,
			Uname:    "root",
			Gname:    "root",
		}
		if err := tw.WriteHeader(hdr); err != nil {
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

// TeamDir is where a push's team files land in the share directory;
// install.sh syncs TeamDir/catalog.d/*.toml into /etc/devbox-setup/catalog.d
// and TeamDir/mcp-catalog/*.spec into the MCP catalog.
const TeamDir = ShareDir + "/team"

// InstallCommand is the remote command that installs Tarball (on stdin):
// it replaces the share directory, so files dropped from pmox don't
// linger, and leaves /var/lib/devbox-setup (saved answers) alone.
// install.sh then installs the Go build for the VM's architecture.
const InstallCommand = "sudo -n sh -c 'rm -rf " + ShareDir + " && tar -xzf - -C / --no-overwrite-dir && sh " + ShareDir + "/install.sh'"

// MiseRegistry is pmox's snapshot of mise's registry (name, aliases,
// description per line), for the picker's search.
func MiseRegistry() []byte {
	b, _ := files.ReadFile("mise-registry.tsv")
	return b
}

// Conf is devbox-setup's own config files (conf/), for file steps.
func Conf() fs.FS { return mustSub(files, "conf") }
