// Package mount manages the on-disk registry and process lifecycle of
// pmox's background mount daemons. Each active `pmox mount` daemon is
// recorded as a small JSON sidecar so `pmox umount` can find the exact
// mount to stop — by VM and, when given, by remote path — instead of
// guessing from a filename hash it can't fully reconstruct.
package mount

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Record describes one background mount. It is persisted as a JSON
// sidecar in the state dir. Path is the sidecar's own location (set by
// List/Save) and is not serialized.
type Record struct {
	VMName     string `json:"vm_name"`
	LocalPath  string `json:"local_path"`
	RemotePath string `json:"remote_path"`
	PID        int    `json:"pid"`
	LogPath    string `json:"log_path"`

	Path string `json:"-"`
}

// ID derives a stable identifier from the local+remote path pair, so
// re-mounting the same directories maps to the same record.
func ID(localPath, remotePath string) string {
	h := sha256.Sum256([]byte(localPath + "\x00" + remotePath))
	return fmt.Sprintf("%x", h[:8])
}

// sidecarName is the sidecar filename for a record: the VM name (for
// human-readable listing and prefix matching) plus the path-pair ID.
func sidecarName(vmName, localPath, remotePath string) string {
	return fmt.Sprintf("%s-%s.json", sanitize(vmName), ID(localPath, remotePath))
}

// sanitize keeps filenames safe: only the VM name goes into the sidecar
// filename and it may in principle contain path separators.
func sanitize(name string) string {
	return strings.NewReplacer("/", "_", "\\", "_", string(os.PathSeparator), "_").Replace(name)
}

// Save writes (or overwrites) the record's sidecar under stateDir and
// returns the sidecar path. It sets r.Path on the returned copy.
func Save(stateDir string, r Record) (Record, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return r, fmt.Errorf("create state dir: %w", err)
	}
	r.Path = filepath.Join(stateDir, sidecarName(r.VMName, r.LocalPath, r.RemotePath))
	data, err := json.Marshal(r)
	if err != nil {
		return r, fmt.Errorf("marshal mount record: %w", err)
	}
	if err := os.WriteFile(r.Path, data, 0o600); err != nil {
		return r, fmt.Errorf("write mount record: %w", err)
	}
	return r, nil
}

// Find returns the record for a given path pair, if one exists.
func Find(stateDir, localPath, remotePath string) (Record, bool, error) {
	all, err := List(stateDir)
	if err != nil {
		return Record{}, false, err
	}
	for _, r := range all {
		if r.LocalPath == localPath && r.RemotePath == remotePath {
			return r, true, nil
		}
	}
	return Record{}, false, nil
}

// List reads every mount record under stateDir. A missing state dir is
// not an error — it just means no mounts. Unparseable sidecars are
// skipped rather than failing the whole listing.
func List(stateDir string) ([]Record, error) {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(stateDir, e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r Record
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		r.Path = p
		out = append(out, r)
	}
	return out, nil
}

// ForVM returns the records belonging to a VM name.
func ForVM(stateDir, vmName string) ([]Record, error) {
	all, err := List(stateDir)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, r := range all {
		if r.VMName == vmName {
			out = append(out, r)
		}
	}
	return out, nil
}

// Remove deletes a record's sidecar. A missing file is not an error.
func Remove(r Record) error {
	if r.Path == "" {
		return nil
	}
	if err := os.Remove(r.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
