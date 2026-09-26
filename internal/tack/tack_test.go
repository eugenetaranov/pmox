package tack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
)

func TestArgsMinimal(t *testing.T) {
	got := Args(Options{Playbook: "pb.yaml", User: "ubuntu", IP: "10.0.0.5", KeyPath: "/k/id"})
	want := []string{"run", "pb.yaml", "-c", "ssh://ubuntu@10.0.0.5", "--ssh-key", "/k/id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args = %v\nwant  %v", got, want)
	}
}

func TestArgsFull(t *testing.T) {
	got := Args(Options{
		Playbook:    "pb.yaml",
		User:        "root",
		IP:          "192.168.0.9",
		Port:        2222,
		KeyPath:     "/k/id",
		Insecure:    true,
		Check:       true,
		AutoApprove: true,
		Tags:        []string{"docker", "net"},
		SkipTags:    []string{"slow"},
		OutputJSON:  true,
	})
	want := []string{
		"run", "pb.yaml",
		"-c", "ssh://root@192.168.0.9:2222",
		"--ssh-key", "/k/id",
		"--ssh-insecure",
		"--check",
		"--auto-approve",
		"--tags", "docker,net",
		"--skip-tags", "slow",
		"--output", "json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args = %v\nwant  %v", got, want)
	}
}

// stubTack puts an executable named tack on PATH (only PATH entry).
func stubTack(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub needs a POSIX shell")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tack"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestCommand(t *testing.T) {
	stubTack(t)
	cmd, err := Command(context.Background(), Options{Playbook: "pb.yaml", IP: "10.0.0.5", User: "u"})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if want := []string{"run", "pb.yaml", "-c", "ssh://u@10.0.0.5"}; !reflect.DeepEqual(cmd.Args[1:], want) {
		t.Errorf("Args = %v, want %v", cmd.Args[1:], want)
	}
	if cmd.WaitDelay != waitDelay {
		t.Errorf("WaitDelay = %v, want %v", cmd.WaitDelay, waitDelay)
	}
	if cmd.Env == nil {
		t.Error("Env not inherited")
	}
	// Every pmox-managed VM has passwordless sudo and key-based SSH, so
	// tack must never block waiting on a password prompt — several
	// callers (the --tack hook, --output json) don't even wire a
	// terminal for it to prompt on. Set via env (not a flag/argv), the
	// right shape for anything with a value too, so a real secret would
	// never show up in `ps`.
	if !slices.Contains(cmd.Env, "TACK_SUDO_NO_PROMPT=1") {
		t.Errorf("Env = %v, missing TACK_SUDO_NO_PROMPT=1", cmd.Env)
	}
	if !slices.Contains(cmd.Env, "TACK_SSH_NO_PROMPT=1") {
		t.Errorf("Env = %v, missing TACK_SSH_NO_PROMPT=1", cmd.Env)
	}
}

func TestCommandValidates(t *testing.T) {
	stubTack(t)
	if _, err := Command(context.Background(), Options{IP: "10.0.0.5"}); err == nil {
		t.Error("want error for missing playbook")
	}
	if _, err := Command(context.Background(), Options{Playbook: "pb.yaml"}); err == nil {
		t.Error("want error for missing host")
	}
	t.Setenv("PATH", "")
	if _, err := Command(context.Background(), Options{Playbook: "pb.yaml", IP: "x"}); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("want ErrNotInstalled, got %v", err)
	}
}

func TestArgsNoUserNoKey(t *testing.T) {
	got := Args(Options{Playbook: "pb.yaml", IP: "10.0.0.5"})
	want := []string{"run", "pb.yaml", "-c", "ssh://10.0.0.5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args = %v\nwant  %v", got, want)
	}
}
