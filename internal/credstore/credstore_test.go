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
