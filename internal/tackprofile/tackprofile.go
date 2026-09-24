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
	"strings"

	"github.com/eugenetaranov/pmox/internal/atomicfile"
)

// store is the on-disk shape: key -> profile name.
type store map[string]string

func key(serverURL string, vmid int) string {
	return serverURL + "#" + strconv.Itoa(vmid)
}

// Get returns the remembered profile for (serverURL, vmid) and whether one
// was found. A missing state file yields ("", false, nil); a corrupt one is
// moved aside (see load) and also yields ("", false, nil). Any other read
// failure is returned as an error.
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

// Entry is one remembered profile record.
type Entry struct {
	ServerURL string
	VMID      int
	Profile   string
}

// All returns every remembered profile entry.
func All(stateDir string) ([]Entry, error) {
	s, err := load(stateDir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(s))
	for k, profile := range s {
		i := strings.LastIndex(k, "#")
		if i < 0 {
			continue
		}
		vmid, err := strconv.Atoi(k[i+1:])
		if err != nil {
			continue
		}
		out = append(out, Entry{ServerURL: k[:i], VMID: vmid, Profile: profile})
	}
	return out, nil
}

// Delete removes the remembered profile for (serverURL, vmid), if present.
func Delete(stateDir, serverURL string, vmid int) error {
	s, err := load(stateDir)
	if err != nil {
		return err
	}
	if _, ok := s[key(serverURL, vmid)]; !ok {
		return nil
	}
	delete(s, key(serverURL, vmid))
	return save(stateDir, s)
}

func statePath(stateDir string) string {
	return filepath.Join(stateDir, "profiles.json")
}

// corruptPath is where an unparseable state file is moved aside to.
func corruptPath(stateDir string) string {
	return statePath(stateDir) + ".corrupt"
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
		// fresh rather than blocking apply, but move the file aside first
		// so the next save doesn't silently destroy it. Best effort.
		_ = os.Rename(statePath(stateDir), corruptPath(stateDir))
		return store{}, nil
	}
	if s == nil {
		s = store{}
	}
	return s, nil
}

func save(stateDir string, s store) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(statePath(stateDir), data, 0o600); err != nil {
		return fmt.Errorf("write tack profile state: %w", err)
	}
	return nil
}
