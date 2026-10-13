package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/bootstrap"
	"github.com/eugenetaranov/pmox/internal/paths"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/sshkey"
	"github.com/eugenetaranov/pmox/internal/vm"
	"github.com/eugenetaranov/pmox/internal/vmwait"
)

// fleetParallel is how many VMs apply and verify work on at once.
const fleetParallel = 4

// yardoTeamDir is where a team's yardo additions live on this
// machine: catalog.d/*.toml (items) and mcp-catalog/*.spec (MCP servers).
func yardoTeamDir() (string, error) {
	dir, err := paths.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "yardo"), nil
}

var teamName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// yardoTeamFiles are the team's files to ship with a push (paths relative
// to the team tarball), each catalog file checked to be TOML first.
func yardoTeamFiles() ([]bootstrap.File, error) {
	base, err := yardoTeamDir()
	if err != nil {
		return nil, err
	}
	var out []bootstrap.File
	for _, k := range []struct{ sub, ext string }{{"catalog.d", ".toml"}, {"mcp-catalog", ".spec"}} {
		dir := filepath.Join(base, k.sub)
		m, _ := filepath.Glob(filepath.Join(dir, "*"+k.ext))
		for _, p := range m {
			name := filepath.Base(p)
			if !teamName.MatchString(name) {
				return nil, fmt.Errorf("%s: use letters, digits, '.', '_' and '-' in the name", p)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			if k.ext == ".toml" {
				if err := checkTOML(p, b); err != nil {
					return nil, err
				}
			}
			out = append(out, bootstrap.File{Path: k.sub + "/" + name, Permissions: "0644", Content: b})
		}
	}
	return out, nil
}

type fleetFlags struct {
	ssh      sshFlags
	all      bool
	tag      string
	profiles []string
}

func newYardoApplyCmd() *cobra.Command {
	f := &fleetFlags{}
	cmd := &cobra.Command{
		Use:   "apply [name|vmid ...]",
		Short: "Install yardo on VMs and apply profiles (or their saved choices)",
		Long: `Install yardo's release on each running VM (with your team's
catalog.d and MCP specs from ~/.config/pmox/yardo/), then run
'sudo yardo --profile … --yes' there (with no --profile,
'sudo yardo --yes', which re-applies the saved choices).

VMs: the ones named, or --all pmox VMs, or those with --tag. Four at a
time; one result line per VM; exits non-zero if any VM failed.`,
		Example: `  pmox yardo apply --tag team --profile team.toml
  pmox yardo apply web1 web2 --profile team.toml --profile me.toml
  pmox yardo apply --all`,
		RunE: func(cmd *cobra.Command, args []string) error { return runYardoFleet(cmd, args, f, false) },
	}
	addFleetFlags(cmd, f)
	return cmd
}

func newYardoVerifyCmd() *cobra.Command {
	f := &fleetFlags{}
	cmd := &cobra.Command{
		Use:   "verify [name|vmid ...]",
		Short: "Check that VMs have what their profiles (or saved choices) pick",
		Long: `Push yardo to each running VM and run 'yardo --verify'
there, with the profiles when given: one line per VM, listing what is
missing. Changes nothing but yardo itself. Exits non-zero if any
VM is missing something or failed.`,
		Example: `  pmox yardo verify --tag team --profile team.toml`,
		RunE:    func(cmd *cobra.Command, args []string) error { return runYardoFleet(cmd, args, f, true) },
	}
	addFleetFlags(cmd, f)
	return cmd
}

func addFleetFlags(cmd *cobra.Command, f *fleetFlags) {
	addSSHFlags(cmd, &f.ssh)
	cmd.Flags().BoolVar(&f.all, "all", false, "every running pmox VM")
	cmd.Flags().StringVar(&f.tag, "tag", "", "the running pmox VMs with this Proxmox tag")
	cmd.Flags().StringArrayVar(&f.profiles, "profile", nil, "a yardo profile to apply (repeatable; later ones win)")
}

// selectFleet picks the VMs: the named ones (by name or vmid), --all
// running pmox VMs, or those with tag.
func selectFleet(resources []pveclient.Resource, args []string, all bool, tag string) ([]pveclient.Resource, error) {
	switch {
	case len(args) > 0 && (all || tag != ""):
		return nil, errors.New("name VMs, or use --all or --tag, not both")
	case all && tag != "":
		return nil, errors.New("use --all or --tag, not both")
	case len(args) == 0 && !all && tag == "":
		return nil, errors.New("name the VMs, or use --all or --tag")
	}
	var out []pveclient.Resource
	if len(args) > 0 {
		for _, a := range args {
			var match *pveclient.Resource
			for i, r := range resources {
				if !r.IsTemplate() && (strconv.Itoa(r.VMID) == a || r.Name == a) && vm.HasPMOXTag(r.Tags) {
					match = &resources[i]
					break
				}
			}
			if match == nil {
				return nil, fmt.Errorf("no pmox VM %q", a)
			}
			out = append(out, *match)
		}
		return out, nil
	}
	for _, r := range resources {
		if r.IsTemplate() || !vm.HasPMOXTag(r.Tags) || !r.IsRunning() || (tag != "" && !r.HasTag(tag)) {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		if tag != "" {
			return nil, fmt.Errorf("no running pmox VM has the tag %q", tag)
		}
		return nil, errors.New("no running pmox VMs")
	}
	return out, nil
}

// fleetCommand is the remote command, and the tar of profiles it reads
// on stdin (none: nil).
func fleetCommand(profiles [][]byte, verify bool) (string, []byte, error) {
	mode := "--yes"
	if verify {
		mode = "--verify"
	}
	if len(profiles) == 0 {
		return "sudo -n yardo " + mode, nil, nil
	}
	const dir = "/var/lib/yardo/pmox-profiles"
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	var flags []string
	for i, p := range profiles {
		name := fmt.Sprintf("%d.toml", i+1)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(p)), Typeflag: tar.TypeReg}); err != nil {
			return "", nil, err
		}
		if _, err := tw.Write(p); err != nil {
			return "", nil, err
		}
		flags = append(flags, "--profile "+dir+"/"+name)
	}
	if err := tw.Close(); err != nil {
		return "", nil, err
	}
	cmd := "sudo -n sh -c 'umask 077 && rm -rf " + dir + " && mkdir -p " + dir + " && tar -xf - -C " + dir +
		" && yardo " + strings.Join(flags, " ") + " " + mode + "'"
	return cmd, buf.Bytes(), nil
}

// yardoFleetRunFn runs command on the target with stdin and returns its
// combined output. A seam for tests.
var yardoFleetRunFn = func(ctx context.Context, target *sshTarget, command string, stdin []byte) (string, error) {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return "", errors.New("ssh binary not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, yardoProfileTimeout)
	defer cancel()
	args := yardoSSHArgs(target, guestHostKeyOpts(), command)
	c := exec.CommandContext(ctx, sshPath, args...)
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	out, err := c.CombinedOutput()
	return string(out), err
}

// fleetIPFn finds a running VM's address. A seam for tests.
var fleetIPFn = func(ctx context.Context, client *pveclient.Client, r pveclient.Resource) (string, error) {
	if !r.IsRunning() {
		return "", errors.New("not running (start it with: pmox start " + r.Name + ")")
	}
	ifaces, err := client.AgentNetwork(ctx, r.Node, r.VMID)
	if err != nil {
		return "", errors.New("guest agent not responding")
	}
	ip := vmwait.PickIPv4(ifaces)
	if ip == "" {
		return "", errors.New("no IPv4 address from the guest agent")
	}
	return ip, nil
}

func runYardoFleet(cmd *cobra.Command, args []string, f *fleetFlags, verify bool) error {
	ctx := cmd.Context()
	var profiles [][]byte
	for _, p := range f.profiles {
		b, err := os.ReadFile(sshkey.ExpandHome(p))
		if err != nil {
			return fmt.Errorf("profile: %w", err)
		}
		if err := checkTOML(p, b); err != nil {
			return fmt.Errorf("profile: %w", err)
		}
		profiles = append(profiles, b)
	}
	if _, err := yardoTeamFiles(); err != nil {
		return err
	}
	command, stdin, err := fleetCommand(profiles, verify)
	if err != nil {
		return err
	}
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	resources, err := client.ClusterResources(ctx, "vm")
	if err != nil {
		return fmt.Errorf("list VMs: %w", err)
	}
	vms, err := selectFleet(resources, args, f.all, f.tag)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	var mu sync.Mutex
	failed := 0
	sem := make(chan struct{}, fleetParallel)
	var wg sync.WaitGroup
	for _, r := range vms {
		wg.Add(1)
		sem <- struct{}{}
		go func(r pveclient.Resource) {
			defer wg.Done()
			defer func() { <-sem }()
			line, ok := fleetOne(ctx, client, r, f, resolved, command, stdin, verify)
			mu.Lock()
			defer mu.Unlock()
			if !ok {
				failed++
			}
			fmt.Fprintf(out, "%s%s: %s\n", doneMark(ok), r.Name, line)
		}(r)
	}
	wg.Wait()
	if failed > 0 {
		return fmt.Errorf("%d of %d VMs failed", failed, len(vms))
	}
	return nil
}

// fleetOne pushes to one VM and runs the command: its result line.
func fleetOne(ctx context.Context, client *pveclient.Client, r pveclient.Resource, f *fleetFlags, resolved *server.Resolved, command string, stdin []byte, verify bool) (string, bool) {
	ip, err := fleetIPFn(ctx, client, r)
	if err != nil {
		return err.Error(), false
	}
	user, key, _, err := resolveGuestIdentity(resolved.URL, r.VMID, f.ssh.user, f.ssh.identity, resolved.Server)
	if err != nil {
		return err.Error(), false
	}
	target := &sshTarget{IP: ip, User: user, Key: key, VMID: r.VMID, Name: r.Name}
	if err := yardoPushFn(ctx, target); err != nil {
		return "push: " + err.Error(), false
	}
	out, err := yardoFleetRunFn(ctx, target, command, stdin)
	switch {
	case err == nil && verify:
		return "everything installed", true
	case err == nil:
		return "applied", true
	case verify && (strings.Contains(out, "\nmissing    ") || strings.HasPrefix(out, "missing    ")):
		return verifyLine(out), false
	}
	return fmt.Sprintf("%v: %s", err, lastLines(out, 2)), false
}

// verifyLine sums up yardo --verify's output.
func verifyLine(out string) string {
	var missing []string
	for _, l := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(l, "missing    "); ok {
			label, _, _ := strings.Cut(rest, ":")
			missing = append(missing, label)
		}
	}
	if len(missing) == 0 {
		return "missing items (see 'sudo yardo --verify' on the VM)"
	}
	return fmt.Sprintf("%d missing: %s", len(missing), strings.Join(missing, ", "))
}

// lastLines is the end of a command's output, for a result line.
func lastLines(out string, n int) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " · ")
}
