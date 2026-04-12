package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSSHInsecure_EnvVarParse(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"1":     true,
		"true":  true,
		"TRUE":  true,
		"yes":   true,
		"no":    false,
		"0":     false,
		"false": false,
	}
	for in, want := range cases {
		t.Setenv("PMOX_SSH_INSECURE", in)
		if got := envBool("PMOX_SSH_INSECURE"); got != want {
			t.Errorf("envBool(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSSHInsecure_WarningOncePerProcess(t *testing.T) {
	origFlag := sshInsecure
	origWarned := sshInsecureWarned
	t.Cleanup(func() {
		sshInsecure = origFlag
		sshInsecureWarned = origWarned
	})
	sshInsecure = true
	sshInsecureWarned = false

	// Capture stderr by redirecting.
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	if !SSHInsecure() {
		t.Fatal("SSHInsecure() = false, want true")
	}
	if !SSHInsecure() {
		t.Fatal("SSHInsecure() second call = false")
	}
	if !SSHInsecure() {
		t.Fatal("SSHInsecure() third call = false")
	}
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	got := buf.String()
	count := strings.Count(got, "WARNING: --ssh-insecure")
	if count != 1 {
		t.Errorf("warning count = %d, want 1 (got: %q)", count, got)
	}
}

func TestSSHInsecure_Disabled(t *testing.T) {
	orig := sshInsecure
	t.Cleanup(func() { sshInsecure = orig })
	sshInsecure = false
	if SSHInsecure() {
		t.Error("SSHInsecure() = true when flag unset")
	}
}

func TestSSHInsecureFlag_Registered(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("ssh-insecure")
	if f == nil {
		t.Fatal("--ssh-insecure flag missing")
	}
}
