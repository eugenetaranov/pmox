// Package tackprofile persists, per VM, the tack profile last applied to
// it, so a bare `pmox apply <vm>` can reuse the previous choice. State is
// a single JSON file under the pmox state dir, keyed by server URL + VMID
// (VMIDs are stable across renames; entries can be pruned when a VM is
// deleted).
package tackprofile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// store is the on-disk shape: key -> profile name.
type store map[string]string

func key(serverURL string, vmid int) string {
	return serverURL + "#" + strconv.Itoa(vmid)
}

// Get returns the remembered profile for (serverURL, vmid) and whether one
// was found. A missing or unreadable file yields ("", false, nil).
func Get(stateDir, serverURL string, vmid int) (string, bool, error) {
	s, err := load(stateDir)
	if err != nil {
		return "", false, err
	}
	v, ok := s[key(serverURL, vmid)]
	return v, ok, nil
}

// Set records the profile for (serverURL, vmid), creating the state file
// (0600 in a 0700 dir) if needed.
func Set(stateDir, serverURL string, vmid int, profile string) error {
	s, err := load(stateDir)
	if err != nil {
		return err
	}
	s[key(serverURL, vmid)] = profile
	return save(stateDir, s)
}

func statePath(stateDir string) string {
	return filepath.Join(stateDir, "profiles.json")
}

func load(stateDir string) (store, error) {
	data, err := os.ReadFile(statePath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return store{}, nil
		}
		return nil, fmt.Errorf("read tack profile state: %w", err)
	}
	var s store
	if err := json.Unmarshal(data, &s); err != nil {
		// A corrupt state file is non-fatal for a convenience cache: start
		// fresh rather than blocking apply.
		return store{}, nil
	}
	if s == nil {
		s = store{}
	}
	return s, nil
}

func save(stateDir string, s store) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tack profile state: %w", err)
	}
	if err := os.Rename(tmp, statePath(stateDir)); err != nil {
		return fmt.Errorf("replace tack profile state: %w", err)
	}
	return nil
}
