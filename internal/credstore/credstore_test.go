package credstore

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func init() {
	keyring.MockInit()
}

func TestSetGetRoundtrip(t *testing.T) {
	url := "https://pve.home.lan:8006/api2/json"
	secret := "s3cr3t-v4lue"
	if err := Set(url, secret); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := Get(url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != secret {
		t.Errorf("got %q, want %q", got, secret)
	}
}

func TestGetMissingReturnsErrNotFound(t *testing.T) {
	_, err := Get("https://nonexistent.example:8006/api2/json")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestNodeSSHPasswordRoundtrip(t *testing.T) {
	url := "https://ssh.example:8006/api2/json"
	if err := SetNodeSSHPassword(url, "hunter2"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := GetNodeSSHPassword(url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("got %q", got)
	}
	// Missing is ErrNotFound.
	_, err = GetNodeSSHPassword("https://nope.example:8006/api2/json")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	// Remove clears it.
	if err := RemoveNodeSSHPassword(url); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := GetNodeSSHPassword(url); !errors.Is(err, ErrNotFound) {
		t.Errorf("after remove: want ErrNotFound, got %v", err)
	}
}

func TestNodeSSHKeyPassphraseRoundtrip(t *testing.T) {
	url := "https://keyssh.example:8006/api2/json"
	if err := SetNodeSSHKeyPassphrase(url, "pp"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := GetNodeSSHKeyPassphrase(url)
	if err != nil || got != "pp" {
		t.Fatalf("Get: got %q err %v", got, err)
	}
	if err := RemoveNodeSSHKeyPassphrase(url); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := GetNodeSSHKeyPassphrase(url); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestRemove(t *testing.T) {
	url := "https://removeme.example:8006/api2/json"
	if err := Set(url, "x"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := Remove(url); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_, err := Get(url)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound after remove, got %v", err)
	}
}

func TestProbeKeychainIsReadOnly(t *testing.T) {
	keyring.MockInit()
	if !probeKeychain() {
		t.Fatal("a working keychain must probe as available")
	}
	// Nothing may be left behind (or ever written): concurrent processes
	// raced on a shared probe entry when the probe wrote one.
	if _, err := keyring.Get(service, probeAccount); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("probe wrote an entry: %v", err)
	}
	// A leftover entry from an older pmox still means "available".
	_ = keyring.Set(service, probeAccount, "1")
	if !probeKeychain() {
		t.Error("a leftover probe entry must still probe as available")
	}
}

func TestProbeKeychainUnavailable(t *testing.T) {
	keyring.MockInitWithError(errors.New("org.freedesktop.secrets was not provided"))
	t.Cleanup(keyring.MockInit)
	if probeKeychain() {
		t.Error("a keychain that errors must probe as unavailable")
	}
}
