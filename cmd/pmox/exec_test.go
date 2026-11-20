package main

import (
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
)

func TestRunRemote_PropagatesExitStatus(t *testing.T) {
	orig := sshRunFn
	t.Cleanup(func() { sshRunFn = orig })
	sshRunFn = func(string, []string) error {
		return exec.Command("sh", "-c", "exit 7").Run()
	}

	err := runRemote("ssh", []string{"ssh", "pmox@10.0.0.5", "false"})
	if err == nil {
		t.Fatal("expected an error for a non-zero remote exit")
	}
	if got := exitcode.From(err); got != 7 {
		t.Errorf("exitcode.From = %d, want 7 (the remote status)", got)
	}
	var self selfReporter
	if !errors.As(err, &self) {
		t.Error("remote exit must be self-reported (no extra 'Error:' line)")
	}
}

func TestRunRemote_SuccessAndNonExitErrors(t *testing.T) {
	orig := sshRunFn
	t.Cleanup(func() { sshRunFn = orig })

	sshRunFn = func(string, []string) error { return nil }
	if err := runRemote("ssh", nil); err != nil {
		t.Errorf("success: err = %v", err)
	}

	boom := errors.New("fork failed")
	sshRunFn = func(string, []string) error { return boom }
	err := runRemote("ssh", nil)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want passthrough", err)
	}
	var self selfReporter
	if errors.As(err, &self) {
		t.Error("a local failure must still print an Error: line")
	}
}

// execUser and execAgent are the flag values parseExecArgs's last run saw.
var (
	execUser  string
	execAgent bool
)

// parseExecArgs runs cobra's parser over argv for an exec-shaped command
// and returns what splitExecArgs makes of it.
func parseExecArgs(t *testing.T, argv ...string) (vmArgs, remote []string, err error) {
	t.Helper()
	cmd := &cobra.Command{
		Use:  "exec",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			vmArgs, remote, err = splitExecArgs(cmd, args)
			return nil
		},
	}
	cmd.Flags().StringP("user", "u", "", "")
	cmd.Flags().BoolP("forward-agent", "A", false, "")
	cmd.Flags().SetInterspersed(false)
	execUser, execAgent = "", false
	cmd.PostRun = func(cmd *cobra.Command, _ []string) {
		execUser, _ = cmd.Flags().GetString("user")
		execAgent, _ = cmd.Flags().GetBool("forward-agent")
	}
	cmd.SetArgs(argv)
	if xerr := cmd.Execute(); xerr != nil {
		t.Fatalf("execute: %v", xerr)
	}
	return vmArgs, remote, err
}

func TestSplitExecArgs(t *testing.T) {
	vmArgs, remote, err := parseExecArgs(t, "web1", "-u", "root", "--", "ls", "-la", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vmArgs, []string{"web1"}) || !reflect.DeepEqual(remote, []string{"ls", "-la", "/tmp"}) {
		t.Errorf("vm=%q remote=%q", vmArgs, remote)
	}

	vmArgs, remote, err = parseExecArgs(t, "--", "uptime")
	if err != nil || len(vmArgs) != 0 || !reflect.DeepEqual(remote, []string{"uptime"}) {
		t.Errorf("no VM arg: vm=%q remote=%q err=%v", vmArgs, remote, err)
	}

	if execUser != "" {
		t.Errorf("user = %q, want unset", execUser)
	}

	if _, _, err = parseExecArgs(t, "web1"); err == nil {
		t.Error("a VM without a command should be an error")
	}
	if _, _, err = parseExecArgs(t, "web1", "--"); err == nil {
		t.Error("empty remote command should be an error")
	}
	if _, _, err = parseExecArgs(t); err == nil {
		t.Error("bare exec should be an error without a terminal")
	}
}

// Without "--" the first argument is the VM and the rest, dashes and
// all, is the command; pmox's flags go before the VM, or between the VM
// and a "--".
func TestSplitExecArgs_NoSeparator(t *testing.T) {
	cases := []struct {
		name       string
		argv       []string
		vm, remote []string
		user       string
		agent      bool
	}{
		{"command with a dash flag", []string{"web1", "uname", "-a"}, []string{"web1"}, []string{"uname", "-a"}, "", false},
		{"pmox flags before the VM", []string{"-u", "root", "web1", "ls", "-l", "/root"}, []string{"web1"}, []string{"ls", "-l", "/root"}, "root", false},
		{"pmox flags after the VM, with --", []string{"web1", "-A", "--", "git", "pull"}, []string{"web1"}, []string{"git", "pull"}, "", true},
		{"valued flag after the VM, with --", []string{"web1", "-u", "root", "--", "ls", "-la"}, []string{"web1"}, []string{"ls", "-la"}, "root", false},
		{"flags before, then VM, then --", []string{"-A", "web1", "--", "id"}, []string{"web1"}, []string{"id"}, "", true},
		{"a -- inside the command", []string{"web1", "git", "log", "--", "file"}, []string{"web1"}, []string{"git", "log", "--", "file"}, "", false},
		{"no VM", []string{"-u", "root", "--", "uptime"}, []string{}, []string{"uptime"}, "root", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm, remote, err := parseExecArgs(t, tc.argv...)
			if err != nil {
				t.Fatal(err)
			}
			if len(vm) != len(tc.vm) || (len(vm) > 0 && vm[0] != tc.vm[0]) || !reflect.DeepEqual(remote, tc.remote) {
				t.Errorf("vm=%q remote=%q, want vm=%q remote=%q", vm, remote, tc.vm, tc.remote)
			}
			if execUser != tc.user || execAgent != tc.agent {
				t.Errorf("user=%q agent=%v, want %q %v", execUser, execAgent, tc.user, tc.agent)
			}
		})
	}

	if _, _, err := parseExecArgs(t, "web1", "-A", "extra", "--", "ls"); err == nil {
		t.Error("a positional between the VM's flags and -- should be an error")
	}
}

func TestExtraArgsAfterDash(t *testing.T) {
	parse := func(argv ...string) []string {
		var got []string
		cmd := &cobra.Command{
			Use: "cp",
			RunE: func(cmd *cobra.Command, _ []string) error {
				got = extraArgsAfterDash(cmd)
				return nil
			},
		}
		cmd.SetArgs(argv)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := parse("./a", "web1:/tmp", "--", "-l", "1000"); !reflect.DeepEqual(got, []string{"-l", "1000"}) {
		t.Errorf("got %q", got)
	}
	if got := parse("./a", "web1:/tmp"); got != nil {
		t.Errorf("no dash: got %q, want nil", got)
	}
}
