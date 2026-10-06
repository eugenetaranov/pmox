// Package accessreg is pmox's cluster-side access registry, kept on the
// Proxmox cluster filesystem so every node and every workstation sees
// the same state:
//
//	/etc/pve/pmox/keys/<name>.pub   published public keys, one per person
//	/etc/pve/pmox/access.yaml       desired grants: person → VMs
//
// It holds public keys only. Whoever can write /etc/pve is already a
// cluster admin, so the registry adds no trust boundary of its own.
package accessreg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

const (
	Dir        = "/etc/pve/pmox"
	KeysDir    = Dir + "/keys"
	AccessFile = Dir + "/access.yaml"

	// LabelPrefix tags a published key line's comment, so keys in a
	// guest's managed block say whose they are.
	LabelPrefix = "pmox-access:"
)

var (
	// ErrConflict means access.yaml kept changing underneath an update.
	ErrConflict = errors.New("access.yaml was changed by someone else at the same time; try again")
	// ErrNotPublished means no key is published under a name.
	ErrNotPublished = errors.New("no key published under that name")
	// ErrInvalidName rejects names that aren't safe file names.
	ErrInvalidName = errors.New("invalid name (use letters, digits, '.', '_' or '-')")

	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// FS is the remote file access the registry needs (pvessh.Client).
type FS interface {
	ReadFile(ctx context.Context, p string) ([]byte, error)
	WriteFile(ctx context.Context, p string, data []byte) error
	Remove(ctx context.Context, p string) error
	ReadDir(ctx context.Context, dir string) ([]string, error)
}

// Access is the desired state in access.yaml.
type Access struct {
	Version int               `yaml:"version"`
	People  map[string]*Grant `yaml:"people"`
}

// Grant is what one person may reach.
type Grant struct {
	AllVMs bool  `yaml:"all_vms,omitempty"`
	VMs    []int `yaml:"vms,omitempty,flow"`
}

// PublishedKey is one keys/<name>.pub file.
type PublishedKey struct {
	Name        string
	Line        string // "type base64 pmox-access:<name>"
	Fingerprint string // SHA256:…
	Host        string // workstation it was published from
	UID         string // local uid on that workstation
	TokenUser   string // PVE token that published it
	PublishedAt time.Time
}

// ValidName reports whether name can be used as a registry name.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

// NewPublishedKey normalizes an authorized_keys line for name: the
// comment becomes "pmox-access:<name>" and the fingerprint is computed.
func NewPublishedKey(name, authorizedKeyLine string) (PublishedKey, error) {
	if err := ValidName(name); err != nil {
		return PublishedKey{}, err
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedKeyLine))
	if err != nil {
		return PublishedKey{}, fmt.Errorf("parse public key: %w", err)
	}
	body := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	return PublishedKey{
		Name:        name,
		Line:        body + " " + LabelPrefix + name,
		Fingerprint: ssh.FingerprintSHA256(pub),
	}, nil
}

// SameKey reports whether a and b hold the same public key.
func SameKey(a, b PublishedKey) bool { return a.Fingerprint == b.Fingerprint }

func keyPath(name string) string { return path.Join(KeysDir, name+".pub") }

// --- published keys ---

func encodeKey(k PublishedKey) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# pmox published key — public key only\n")
	fmt.Fprintf(&b, "# name: %s\n", k.Name)
	fmt.Fprintf(&b, "# host: %s\n", k.Host)
	fmt.Fprintf(&b, "# uid: %s\n", k.UID)
	fmt.Fprintf(&b, "# token: %s\n", k.TokenUser)
	fmt.Fprintf(&b, "# published: %s\n", k.PublishedAt.UTC().Format(time.RFC3339))
	b.WriteString(k.Line + "\n")
	return b.Bytes()
}

func decodeKey(name string, data []byte) (PublishedKey, error) {
	k := PublishedKey{Name: name}
	var line string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "":
		case strings.HasPrefix(l, "#"):
			kv := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(l, "#")), ":", 2)
			if len(kv) != 2 {
				continue
			}
			v := strings.TrimSpace(kv[1])
			switch strings.TrimSpace(kv[0]) {
			case "host":
				k.Host = v
			case "uid":
				k.UID = v
			case "token":
				k.TokenUser = v
			case "published":
				k.PublishedAt, _ = time.Parse(time.RFC3339, v)
			}
		case line == "":
			line = l
		}
	}
	if line == "" {
		return k, fmt.Errorf("%s: no public key line", keyPath(name))
	}
	parsed, err := NewPublishedKey(name, line)
	if err != nil {
		return k, fmt.Errorf("%s: %w", keyPath(name), err)
	}
	k.Line, k.Fingerprint = parsed.Line, parsed.Fingerprint
	return k, nil
}

// GetKey returns the key published under name (ErrNotPublished if none).
func GetKey(ctx context.Context, fs FS, name string) (PublishedKey, error) {
	if err := ValidName(name); err != nil {
		return PublishedKey{}, err
	}
	data, err := fs.ReadFile(ctx, keyPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return PublishedKey{}, fmt.Errorf("%w: %s", ErrNotPublished, name)
	}
	if err != nil {
		return PublishedKey{}, err
	}
	return decodeKey(name, data)
}

// ListKeys returns every published key, sorted by name. Unreadable or
// malformed files are skipped and reported in errs.
func ListKeys(ctx context.Context, fs FS) (keys []PublishedKey, errs []error, err error) {
	names, err := fs.ReadDir(ctx, KeysDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, n := range names {
		name, ok := strings.CutSuffix(n, ".pub")
		if !ok || ValidName(name) != nil {
			continue
		}
		k, kerr := GetKey(ctx, fs, name)
		if kerr != nil {
			errs = append(errs, kerr)
			continue
		}
		keys = append(keys, k)
	}
	return keys, errs, nil
}

// PublishKey writes k, replacing any existing key under the same name.
func PublishKey(ctx context.Context, fs FS, k PublishedKey) error {
	if err := ValidName(k.Name); err != nil {
		return err
	}
	return fs.WriteFile(ctx, keyPath(k.Name), encodeKey(k))
}

// UnpublishKey removes name's key (ErrNotPublished if none).
func UnpublishKey(ctx context.Context, fs FS, name string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	err := fs.Remove(ctx, keyPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotPublished, name)
	}
	return err
}

// --- access.yaml ---

func parseAccess(data []byte) (*Access, error) {
	a := &Access{Version: 1, People: map[string]*Grant{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return a, nil
	}
	if err := yaml.Unmarshal(data, a); err != nil {
		return nil, fmt.Errorf("parse %s: %w", AccessFile, err)
	}
	if a.Version != 1 {
		return nil, fmt.Errorf("%s: unsupported version %d (this pmox understands 1)", AccessFile, a.Version)
	}
	if a.People == nil {
		a.People = map[string]*Grant{}
	}
	return a, nil
}

func encodeAccess(a *Access) ([]byte, error) {
	for name, g := range a.People {
		if g == nil || (!g.AllVMs && len(g.VMs) == 0) {
			delete(a.People, name)
			continue
		}
		sort.Ints(g.VMs)
		g.VMs = slices.Compact(g.VMs)
	}
	body, err := yaml.Marshal(a)
	if err != nil {
		return nil, err
	}
	return append([]byte("# pmox access registry — managed by 'pmox access'; who may reach which VMs\n"), body...), nil
}

func readRaw(ctx context.Context, fs FS) ([]byte, error) {
	data, err := fs.ReadFile(ctx, AccessFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// ReadAccess returns the desired state (empty when access.yaml is absent).
func ReadAccess(ctx context.Context, fs FS) (*Access, error) {
	data, err := readRaw(ctx, fs)
	if err != nil {
		return nil, err
	}
	return parseAccess(data)
}

// UpdateAccess reads access.yaml, applies mutate and writes it back. If
// the file changed between the read and the write (another workstation
// editing at the same time), it re-reads and re-applies mutate once,
// then gives up with ErrConflict.
func UpdateAccess(ctx context.Context, fs FS, mutate func(*Access) error) (*Access, error) {
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := readRaw(ctx, fs)
		if err != nil {
			return nil, err
		}
		a, err := parseAccess(raw)
		if err != nil {
			return nil, err
		}
		if err := mutate(a); err != nil {
			return nil, err
		}
		out, err := encodeAccess(a)
		if err != nil {
			return nil, err
		}
		check, err := readRaw(ctx, fs)
		if err != nil {
			return nil, err
		}
		if sha256.Sum256(check) != sha256.Sum256(raw) {
			continue // changed underneath us: re-read and re-apply
		}
		if err := fs.WriteFile(ctx, AccessFile, out); err != nil {
			return nil, err
		}
		return a, nil
	}
	return nil, ErrConflict
}

// GrantVMs adds vmids to name's grant.
func (a *Access) GrantVMs(name string, vmids ...int) {
	g := a.grant(name)
	g.VMs = append(g.VMs, vmids...)
}

// GrantAll gives name access to all pmox VMs, including future ones.
func (a *Access) GrantAll(name string) { a.grant(name).AllVMs = true }

// RevokeVMs removes vmids from name's grant.
func (a *Access) RevokeVMs(name string, vmids ...int) {
	g, ok := a.People[name]
	if !ok {
		return
	}
	g.VMs = slices.DeleteFunc(g.VMs, func(v int) bool { return slices.Contains(vmids, v) })
}

// RevokeAll removes all of name's access.
func (a *Access) RevokeAll(name string) { delete(a.People, name) }

func (a *Access) grant(name string) *Grant {
	g, ok := a.People[name]
	if !ok || g == nil {
		g = &Grant{}
		a.People[name] = g
	}
	return g
}

// Allowed reports whether name may reach vmid.
func (a *Access) Allowed(name string, vmid int) bool {
	g, ok := a.People[name]
	return ok && g != nil && (g.AllVMs || slices.Contains(g.VMs, vmid))
}

// KeysFor returns the managed key lines vmid should carry: everyone
// allowed on it whose key is published, sorted by name. missing lists
// allowed people with no published key.
func (a *Access) KeysFor(vmid int, keys []PublishedKey) (lines, missing []string) {
	byName := map[string]PublishedKey{}
	for _, k := range keys {
		byName[k.Name] = k
	}
	names := make([]string, 0, len(a.People))
	for n := range a.People {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !a.Allowed(n, vmid) {
			continue
		}
		if k, ok := byName[n]; ok {
			lines = append(lines, k.Line)
		} else {
			missing = append(missing, n)
		}
	}
	return lines, missing
}
