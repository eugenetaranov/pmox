// Package paths resolves pmox's per-user base directories, honoring the
// XDG Base Directory environment variables.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// appName is the subdirectory pmox owns under each XDG base directory.
const appName = "pmox"

// ConfigDir returns pmox's config directory: $XDG_CONFIG_HOME/pmox when
// XDG_CONFIG_HOME is set, otherwise $HOME/.config/pmox.
func ConfigDir() (string, error) {
	return baseDir("XDG_CONFIG_HOME", ".config")
}

// StateDir returns pmox's state directory: $XDG_STATE_HOME/pmox when
// XDG_STATE_HOME is set, otherwise $HOME/.local/state/pmox.
func StateDir() (string, error) {
	return baseDir("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

// baseDir returns $env/pmox when env is non-empty, else $HOME/homeRel/pmox.
func baseDir(env, homeRel string) (string, error) {
	if xdg := os.Getenv(env); xdg != "" {
		return filepath.Join(xdg, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, homeRel, appName), nil
}
