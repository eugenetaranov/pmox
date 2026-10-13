package answers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// SystemFile is the root-only file in the state directory with the system
// items' picks, the machine's settings, and the users set up.
const SystemFile = "system.json"

// UserFile is each user's answers, under their home. The user owns it, so
// it is untrusted: it is read and written without following symlinks,
// and can only pick user items.
var UserFile = []string{".config", "devbox", "answers.json"}

// FirstMCPPort is the first user's MCPJungle port; later users get the
// next free one.
const FirstMCPPort = 8080

// maxUserFile caps the home file's size.
const maxUserFile = 1 << 20

type systemFile struct {
	Version  int                 `json:"version"`
	Picks    []string            `json:"picks"`
	TZ       string              `json:"tz"`
	DevPorts []string            `json:"dev_ports,omitempty"`
	Layers   map[string][]string `json:"layers,omitempty"`
	Hand     []string            `json:"hand,omitempty"`
	// Users are the users set up, by name.
	Users map[string]*UserInfo `json:"users"`
}

// UserInfo is what root keeps about a user (never read from their home).
type UserInfo struct {
	MCPPort int `json:"mcp_port,omitempty"`
}

type userFile struct {
	Version    int                 `json:"version"`
	Picks      []string            `json:"picks"`
	MiseExtra  []string            `json:"mise_extra"`
	MCPListen  string              `json:"mcp_listen"`
	MCPServers []string            `json:"mcp_servers"`
	WireAgents *bool               `json:"wire_agents"`
	Git        Git                 `json:"git"`
	GitHub     GitHub              `json:"github"`
	NPM        NPM                 `json:"npm"`
	Registries []string            `json:"registries"`
	Layers     map[string][]string `json:"layers,omitempty"`
	Hand       []string            `json:"hand,omitempty"`
}

// Account is a user's home and ids.
type Account struct {
	Home     string
	UID, GID int
}

// LookupAccount finds a user's account in the password database.
func LookupAccount(name string) (Account, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return Account{}, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return Account{u.HomeDir, uid, gid}, nil
}

// Store is the saved answers: system.json in State, and each user's file
// in their home. The answers.json of Phase 1 is split into them on first
// use, and is still written (the combined answers of the user saved last)
// for the bash installer until the cut-over.
type Store struct {
	State string
	// IsSystem reports whether a pick is a system item.
	IsSystem func(id string) bool
	// Account looks a user up (tests replace it).
	Account func(name string) (Account, error)
	// Chown makes the home file the user's (false in tests run as non-root).
	Chown bool
}

func (s *Store) account(name string) (Account, error) {
	if s.Account != nil {
		return s.Account(name)
	}
	return LookupAccount(name)
}

func (s *Store) readSystem() (*systemFile, bool, error) {
	path := filepath.Join(s.State, SystemFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &systemFile{Version: Version, Users: map[string]*UserInfo{}}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var sys systemFile
	if err := json.Unmarshal(b, &sys); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	if sys.Version != Version {
		return nil, false, fmt.Errorf("%s: version %d; this devbox-setup reads %d", path, sys.Version, Version)
	}
	if sys.Users == nil {
		sys.Users = map[string]*UserInfo{}
	}
	return &sys, true, nil
}

// migrate splits a Phase 1 answers.json (or the older shell-format file)
// into system.json and the user's file, keeping it as answers.json.old.
// It does nothing once system.json exists, or when State isn't writable.
func (s *Store) migrate() (notes []string, err error) {
	if _, ok, err := s.readSystem(); err != nil || ok {
		return nil, err
	}
	_, jerr := os.Stat(filepath.Join(s.State, File))
	_, lerr := os.Stat(filepath.Join(s.State, Legacy))
	if (jerr != nil && lerr != nil) || !writable(s.State) {
		return nil, nil
	}
	a, notes, err := Read(s.State)
	if err != nil || a.User == "" {
		return notes, err
	}
	if b, err := os.ReadFile(filepath.Join(s.State, File)); err == nil {
		if err := writeAtomic(filepath.Join(s.State, File+".old"), b, 0o600); err != nil {
			return notes, err
		}
	}
	// The single gateway of Phase 1 ran on the first port: its user keeps it.
	if slices.Contains(a.Picks, "mcpjungle") {
		a.MCPPort = FirstMCPPort
	}
	return notes, s.Save(a)
}

// Users returns the users set up, sorted.
func (s *Store) Users() ([]string, []string, error) {
	notes, err := s.migrate()
	if err != nil {
		return nil, notes, err
	}
	sys, _, err := s.readSystem()
	if err != nil {
		return nil, notes, err
	}
	var out []string
	for u := range sys.Users {
		out = append(out, u)
	}
	sort.Strings(out)
	return out, notes, nil
}

// Load returns name's answers: system.json's with the user's file's. With
// name "", only the system's. notes are warnings (migrations, system
// items the user file can't pick).
func (s *Store) Load(name string) (*Answers, []string, error) {
	notes, err := s.migrate()
	if err != nil {
		return nil, notes, err
	}
	sys, _, err := s.readSystem()
	if err != nil {
		return nil, notes, err
	}
	a := &Answers{Version: Version, User: name, TZ: sys.TZ, DevPorts: sys.DevPorts, Layers: map[string][]string{}}
	a.Picks = slices.DeleteFunc(slices.Clone(sys.Picks), func(id string) bool { return !s.IsSystem(id) })
	a.Hand = slices.DeleteFunc(slices.Clone(sys.Hand), func(id string) bool { return !s.IsSystem(id) })
	for n, l := range sys.Layers {
		a.Layers[n] = slices.DeleteFunc(slices.Clone(l), func(id string) bool { return !s.IsSystem(id) })
	}
	if name != "" {
		if u := sys.Users[name]; u != nil {
			a.MCPPort = u.MCPPort
		}
		uf, err := s.readUser(name)
		if err != nil {
			return nil, notes, err
		}
		var refused []string
		userOnly := func(l []string) []string {
			return slices.DeleteFunc(slices.Clone(l), func(id string) bool {
				if s.IsSystem(id) {
					refused = append(refused, id)
					return true
				}
				return false
			})
		}
		a.Picks = Merge(a.Picks, userOnly(uf.Picks))
		a.Hand = Merge(a.Hand, userOnly(uf.Hand))
		for n, l := range uf.Layers {
			a.Layers[n] = Merge(a.Layers[n], userOnly(l))
		}
		if refused = Merge(nil, refused); len(refused) > 0 {
			notes = append(notes, fmt.Sprintf("%s's answers pick system items, which only root's system.json can: ignored %s",
				name, strings.Join(refused, " ")))
		}
		a.MiseExtra, a.MCPListen, a.MCPServers, a.WireAgents = uf.MiseExtra, uf.MCPListen, uf.MCPServers, uf.WireAgents
		a.Git, a.GitHub, a.NPM, a.Registries = uf.Git, uf.GitHub, uf.NPM, uf.Registries
	}
	if len(a.Layers) == 0 {
		a.Layers = nil
	}
	a.clean()
	var more []string
	a.Picks, more = MigratePicks(a.Picks)
	a.Hand, _ = MigratePicks(a.Hand)
	for n, l := range a.Layers {
		a.Layers[n], _ = MigratePicks(l)
	}
	return a, append(notes, more...), nil
}

// Save writes a: its system picks and settings, and a.User's entry, to
// system.json; the rest to a.User's file; and the combined answers to
// answers.json for the bash installer.
func (s *Store) Save(a *Answers) error {
	if a.User == "" {
		return errors.New("answers: no user to save for")
	}
	sys, _, err := s.readSystem()
	if err != nil {
		return err
	}
	split := func(l []string) (system, own []string) {
		for _, id := range l {
			if s.IsSystem(id) {
				system = append(system, id)
			} else {
				own = append(own, id)
			}
		}
		return Merge(nil, system), Merge(nil, own)
	}
	uf := &userFile{Version: Version, MiseExtra: a.MiseExtra, MCPListen: a.MCPListen, MCPServers: a.MCPServers,
		WireAgents: a.WireAgents, Git: a.Git, GitHub: a.GitHub, NPM: a.NPM, Registries: a.Registries}
	sys.Version, sys.TZ, sys.DevPorts = Version, a.TZ, a.DevPorts
	sys.Picks, uf.Picks = split(a.Picks)
	sys.Hand, uf.Hand = split(a.Hand)
	if len(sys.Hand) == 0 {
		sys.Hand = nil
	}
	if len(uf.Hand) == 0 {
		uf.Hand = nil
	}
	sysLayers := map[string][]string{}
	for n, l := range sys.Layers {
		sysLayers[n] = l // other users' named profiles may have set them
	}
	for n, l := range a.Layers {
		sl, ul := split(l)
		sysLayers[n] = sl
		if len(ul) > 0 {
			if uf.Layers == nil {
				uf.Layers = map[string][]string{}
			}
			uf.Layers[n] = ul
		}
	}
	sys.Layers = sysLayers
	if len(sys.Layers) == 0 {
		sys.Layers = nil
	}
	sys.Users[a.User] = &UserInfo{MCPPort: a.MCPPort}
	for _, l := range []*[]string{&sys.Picks, &uf.Picks, &uf.MiseExtra, &uf.MCPServers, &uf.Registries} {
		if *l == nil {
			*l = []string{}
		}
	}
	if err := s.writeUser(a.User, uf); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sys, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.State, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.State, 0o700); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(s.State, SystemFile), append(b, '\n'), 0o600); err != nil {
		return err
	}
	return Write(s.State, a)
}

// Port returns name's MCPJungle port: the recorded one, or the first one
// from FirstMCPPort that no other user has (not saved until Save).
func (s *Store) Port(name string) (int, error) {
	sys, _, err := s.readSystem()
	if err != nil {
		return 0, err
	}
	if u := sys.Users[name]; u != nil && u.MCPPort > 0 {
		return u.MCPPort, nil
	}
	used := map[int]bool{}
	for n, u := range sys.Users {
		if n != name && u != nil {
			used[u.MCPPort] = true
		}
	}
	p := FirstMCPPort
	for used[p] {
		p++
	}
	return p, nil
}

func (s *Store) readUser(name string) (*userFile, error) {
	acct, err := s.account(name)
	if err != nil {
		// A user devbox-setup will create has no answers yet.
		return &userFile{Version: Version}, nil //nolint:nilerr
	}
	shown := filepath.Join(append([]string{acct.Home}, UserFile...)...)
	b, err := readNoFollow(acct, UserFile)
	if errors.Is(err, fs.ErrNotExist) {
		return &userFile{Version: Version}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", shown, err)
	}
	var uf userFile
	if err := json.Unmarshal(b, &uf); err != nil {
		// Not the decoder's message: it can quote the file.
		return nil, fmt.Errorf("%s: not valid JSON; fix or remove it", shown)
	}
	if uf.Version != Version {
		return nil, fmt.Errorf("%s: version %d; this devbox-setup reads %d", shown, uf.Version, Version)
	}
	return &uf, nil
}

func (s *Store) writeUser(name string, uf *userFile) error {
	acct, err := s.account(name)
	if err != nil {
		return fmt.Errorf("user %q: %w", name, err)
	}
	b, err := json.MarshalIndent(uf, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNoFollow(acct, UserFile, append(b, '\n'), s.Chown); err != nil {
		return fmt.Errorf("%s: %w", filepath.Join(append([]string{acct.Home}, UserFile...)...), err)
	}
	return nil
}
