package answers

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testStore keeps homes under a temp dir; system items are the ones named
// "sys-*" and ufw.
func testStore(t *testing.T) (*Store, func(string) string) {
	t.Helper()
	root := t.TempDir()
	home := func(u string) string { return filepath.Join(root, "home", u) }
	s := &Store{
		State:    filepath.Join(root, "state"),
		IsSystem: func(id string) bool { return id == "ufw" || id == "docker" },
		Account: func(name string) (Account, error) {
			if err := os.MkdirAll(home(name), 0o755); err != nil {
				return Account{}, err
			}
			return Account{Home: home(name), UID: os.Getuid(), GID: os.Getgid()}, nil
		},
	}
	return s, home
}

func userPath(home string) string { return filepath.Join(append([]string{home}, UserFile...)...) }

func TestStoreTwoUsers(t *testing.T) {
	s, home := testStore(t)
	ada := &Answers{User: "ada", Picks: []string{"docker", "zsh"}, Git: Git{Name: "Ada"}, TZ: "UTC", MCPPort: 8080}
	if err := s.Save(ada); err != nil {
		t.Fatal(err)
	}
	bob, _, err := s.Load("bob")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bob.Picks, []string{"docker"}) || bob.Git.Name != "" || bob.TZ != "UTC" {
		t.Errorf("bob before his run = %+v", bob)
	}
	if p, _ := s.Port("bob"); p != 8081 {
		t.Errorf("bob's port = %d, want 8081", p)
	}
	bob.Picks = append(bob.Picks, "go")
	bob.Git.Name, bob.MCPPort = "Bob", 8081
	if err := s.Save(bob); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.Load("ada")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Picks, []string{"docker", "zsh"}) || a.Git.Name != "Ada" || a.MCPPort != 8080 {
		t.Errorf("ada after bob's run = %+v", a)
	}
	if users, _, _ := s.Users(); !reflect.DeepEqual(users, []string{"ada", "bob"}) {
		t.Errorf("users = %v", users)
	}
	b, err := os.ReadFile(userPath(home("bob")))
	if err != nil || !strings.Contains(string(b), `"go"`) || strings.Contains(string(b), "docker") || strings.Contains(string(b), "8081") {
		t.Errorf("bob's file (%v):\n%s", err, b)
	}
	if fi, _ := os.Stat(userPath(home("bob"))); fi.Mode().Perm() != 0o600 {
		t.Errorf("bob's file mode = %v", fi.Mode())
	}
	// The bash installer's answers.json: the last user's combined answers.
	if c, _, _ := Read(s.State); c.User != "bob" || !reflect.DeepEqual(c.Picks, []string{"docker", "go"}) {
		t.Errorf("answers.json = %+v", c)
	}
}

func TestStoreUserFileCantPickSystemItems(t *testing.T) {
	s, home := testStore(t)
	if err := s.Save(&Answers{User: "ada", Picks: []string{"zsh"}}); err != nil {
		t.Fatal(err)
	}
	p := userPath(home("ada"))
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), `"zsh"`, `"zsh", "ufw"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	a, notes, err := s.Load("ada")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Picks, []string{"zsh"}) {
		t.Errorf("picks = %v", a.Picks)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "ignored ufw") {
		t.Errorf("notes = %v", notes)
	}
}

func TestStoreRefusesSymlinks(t *testing.T) {
	s, home := testStore(t)
	secret := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(secret, []byte("root:$6$secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Account("ada"); err != nil {
		t.Fatal(err)
	}
	p := userPath(home("ada"))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, p); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Load("ada")
	if err == nil || !strings.Contains(err.Error(), "symlink") || strings.Contains(err.Error(), "secret") {
		t.Errorf("symlinked file: err = %v", err)
	}
	// A symlinked directory on the way is refused too, for reads and writes.
	if err := os.RemoveAll(filepath.Join(home("ada"), ".config")); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home("ada"), ".config")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load("ada"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlinked dir, load: err = %v", err)
	}
	if err := s.Save(&Answers{User: "ada"}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlinked dir, save: err = %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote through the symlink: %v", entries)
	}
}

func TestStoreBadJSONNotQuoted(t *testing.T) {
	s, home := testStore(t)
	if _, err := s.Account("ada"); err != nil {
		t.Fatal(err)
	}
	p := userPath(home("ada"))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("hunter2-not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load("ada"); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("err = %v", err)
	}
}

// Phase 1's answers.json is split on first use; its gateway keeps 8080.
func TestStoreMigrates(t *testing.T) {
	s, home := testStore(t)
	old := &Answers{User: "ada", Picks: []string{"ufw", "mcpjungle"}, Git: Git{Name: "Ada"}, TZ: "UTC"}
	if err := Write(s.State, old); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.Load("ada")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Picks, []string{"ufw", "mcpjungle"}) || a.Git.Name != "Ada" || a.TZ != "UTC" || a.MCPPort != 8080 {
		t.Errorf("migrated = %+v", a)
	}
	for _, f := range []string{filepath.Join(s.State, SystemFile), filepath.Join(s.State, File+".old"), userPath(home("ada"))} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if p, _ := s.Port("bob"); p != 8081 {
		t.Errorf("bob's port = %d", p)
	}
}
