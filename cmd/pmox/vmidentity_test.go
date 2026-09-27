package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/vmidentity"
)

const testServerURL = "https://pve1.test:8006/api2/json"

func TestResolveGuestIdentity_PrecedenceLadder(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, err := guestIdentityStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := vmidentity.Set(dir, testServerURL, 100, vmidentity.Identity{User: "ubuntu"}); err != nil {
		t.Fatal(err)
	}
	srv := &config.Server{User: "e"}

	cases := []struct {
		name     string
		flagUser string
		wantUser string
	}{
		{"flag wins over everything", "root", "root"},
		{"recorded identity wins over config", "", "ubuntu"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user, _, _, err := resolveGuestIdentity(testServerURL, 100, tc.flagUser, "", srv)
			if err != nil {
				t.Fatalf("resolveGuestIdentity: %v", err)
			}
			if user != tc.wantUser {
				t.Errorf("user = %q, want %q", user, tc.wantUser)
			}
		})
	}
}

func TestResolveGuestIdentity_ConfigWhenNoRecord(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	srv := &config.Server{User: "e"}
	user, _, note, err := resolveGuestIdentity(testServerURL, 101, "", "", srv)
	if err != nil {
		t.Fatalf("resolveGuestIdentity: %v", err)
	}
	if user != "e" {
		t.Errorf("user = %q, want e (config, no record)", user)
	}
	if note != "" {
		t.Errorf("note = %q, want empty (nothing recorded to compare against)", note)
	}
}

func TestResolveGuestIdentity_DefaultWhenNothingSet(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	user, _, _, err := resolveGuestIdentity(testServerURL, 102, "", "", &config.Server{})
	if err != nil {
		t.Fatalf("resolveGuestIdentity: %v", err)
	}
	if user != defaultUser {
		t.Errorf("user = %q, want %q", user, defaultUser)
	}
}

func TestResolveGuestIdentity_EmptyServerURLSkipsLookup(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, err := guestIdentityStateDir()
	if err != nil {
		t.Fatal(err)
	}
	// Seed a record keyed by empty server URL to prove it's never consulted.
	if err := vmidentity.Set(dir, "", 100, vmidentity.Identity{User: "should-not-be-used"}); err != nil {
		t.Fatal(err)
	}
	user, _, _, err := resolveGuestIdentity("", 100, "", "", &config.Server{User: "e"})
	if err != nil {
		t.Fatalf("resolveGuestIdentity: %v", err)
	}
	if user != "e" {
		t.Errorf("user = %q, want e (empty serverURL must skip the vmidentity lookup)", user)
	}
}

func TestRecordVMIdentity(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	ciPath := filepath.Join(dir, "cloud-init.yaml")
	content := "#cloud-config\nusers:\n  - name: ubuntu\n    ssh_authorized_keys:\n      - ssh-ed25519 AAAA test\n"
	if err := os.WriteFile(ciPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	recordVMIdentity(nil, testServerURL, 100, ciPath)

	stateDir, err := guestIdentityStateDir()
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := vmidentity.Get(stateDir, testServerURL, 100)
	if err != nil {
		t.Fatalf("vmidentity.Get: %v", err)
	}
	if !ok {
		t.Fatal("expected an identity to be recorded")
	}
	want := vmidentity.Identity{User: "ubuntu", SSHPubkeyLine: "ssh-ed25519 AAAA test"}
	if got != want {
		t.Errorf("recorded = %+v, want %+v", got, want)
	}
}

func TestRecordVMIdentity_MissingCloudInitFileIsSilent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// A missing file is not an error for CloudInitIdentityFromFile, so
	// recordVMIdentity should just skip recording rather than panic.
	recordVMIdentity(nil, testServerURL, 100, filepath.Join(t.TempDir(), "nope.yaml"))

	stateDir, err := guestIdentityStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := vmidentity.Get(stateDir, testServerURL, 100); ok {
		t.Error("expected no identity recorded for a missing cloud-init file")
	}
}

func TestGuestIdentityNote(t *testing.T) {
	srv := &config.Server{User: "e"}
	rec := vmidentity.Identity{User: "ubuntu"}

	if note := guestIdentityNote(100, rec, srv, false, false); note != "" {
		t.Errorf("no drift: note = %q, want empty", note)
	}
	if note := guestIdentityNote(100, rec, srv, true, false); note == "" {
		t.Error("user-only drift: want a non-empty note")
	}
	if note := guestIdentityNote(100, rec, srv, false, true); note == "" {
		t.Error("key-only drift: want a non-empty note")
	}
	if note := guestIdentityNote(100, rec, srv, true, true); note == "" {
		t.Error("user+key drift: want a non-empty note")
	}
}
