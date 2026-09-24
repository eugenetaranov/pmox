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

	if _, _, err = parseExecArgs(t, "web1"); err == nil {
		t.Error("missing -- should be an error")
	}
	if _, _, err = parseExecArgs(t, "web1", "--"); err == nil {
		t.Error("empty remote command should be an error")
	}
	if _, _, err = parseExecArgs(t, "a", "b", "--", "ls"); err == nil {
		t.Error("two VM args should be an error")
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
