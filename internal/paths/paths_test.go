package paths

import (
	"path/filepath"
	"testing"
)

func TestConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg/cfg")
	if got, err := ConfigDir(); err != nil || got != filepath.Join("/xdg/cfg", "pmox") {
		t.Errorf("ConfigDir with XDG = %q, %v", got, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got, err := ConfigDir(); err != nil || got != filepath.Join("/home/u", ".config", "pmox") {
		t.Errorf("ConfigDir without XDG = %q, %v", got, err)
	}
}

func TestStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	if got, err := StateDir(); err != nil || got != filepath.Join("/xdg/state", "pmox") {
		t.Errorf("StateDir with XDG = %q, %v", got, err)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got, err := StateDir(); err != nil || got != filepath.Join("/home/u", ".local", "state", "pmox") {
		t.Errorf("StateDir without XDG = %q, %v", got, err)
	}
}

func TestConfigDir_NoHomeIsError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if _, err := ConfigDir(); err == nil {
		t.Error("ConfigDir with no HOME: want error")
	}
}
