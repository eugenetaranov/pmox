package guestkeys

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

const launch = "ssh-ed25519 AAAAlaunch alice@ws1"

func TestHomeDir(t *testing.T) {
	passwd := []byte("root:x:0:0:root:/root:/bin/bash\r\nubuntu:x:1000:1000:Ubuntu:/home/ubuntu:/bin/bash\nbroken\n")
	if h, ok := HomeDir(passwd, "ubuntu"); !ok || h != "/home/ubuntu" {
		t.Errorf("ubuntu: %q %v", h, ok)
	}
	if h, ok := HomeDir(passwd, "root"); !ok || h != "/root" {
		t.Errorf("root: %q %v", h, ok)
	}
	if _, ok := HomeDir(passwd, "bob"); ok {
		t.Error("bob should be missing")
	}
}

func TestRender(t *testing.T) {
	bob, carol := "ssh-ed25519 AAAAbob pmox-access:bob", "ssh-ed25519 AAAAcarol pmox-access:carol"
	cases := []struct {
		name, in string
		keys     []string
		want     string
	}{
		{"insert after launch key", launch + "\n", []string{bob},
			launch + "\n" + BeginMarker + "\n" + bob + "\n" + EndMarker + "\n"},
		{"missing trailing newline", launch, []string{bob},
			launch + "\n" + BeginMarker + "\n" + bob + "\n" + EndMarker + "\n"},
		{"replace keeps lines after block",
			launch + "\n# pmox-access begin (old text)\n" + bob + "\n" + EndMarker + "\nssh-rsa AAAAhand added\n",
			[]string{carol},
			launch + "\n" + BeginMarker + "\n" + carol + "\n" + EndMarker + "\nssh-rsa AAAAhand added\n"},
		{"empty keys removes block",
			launch + "\n" + BeginMarker + "\n" + bob + "\n" + EndMarker + "\n", nil, launch + "\n"},
		{"crlf preserved", launch + "\r\n", []string{bob},
			launch + "\r\n" + BeginMarker + "\r\n" + bob + "\r\n" + EndMarker + "\r\n"},
		{"empty file", "", []string{bob}, BeginMarker + "\n" + bob + "\n" + EndMarker + "\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(Render([]byte(c.in), c.keys))
			if got != c.want {
				t.Errorf("got\n%q\nwant\n%q", got, c.want)
			}
			if !slices.Equal(ManagedKeys([]byte(got)), c.keys) && len(c.keys) > 0 {
				t.Errorf("ManagedKeys(Render) = %v, want %v", ManagedKeys([]byte(got)), c.keys)
			}
		})
	}
}

// fakeAgent is an in-memory guest filesystem.
type fakeAgent struct {
	files  map[string]string
	writes int
}

func (f *fakeAgent) AgentFileRead(_ context.Context, _ string, _ int, p string) ([]byte, bool, error) {
	c, ok := f.files[p]
	if !ok {
		return nil, false, errors.New("api error: 500: guest-file-open: No such file or directory")
	}
	return []byte(c), false, nil
}

func (f *fakeAgent) AgentFileWrite(_ context.Context, _ string, _ int, p string, content []byte) error {
	f.writes++
	f.files[p] = string(content)
	return nil
}

func newGuest() *fakeAgent {
	return &fakeAgent{files: map[string]string{
		"/etc/passwd":                       "ubuntu:x:1000:1000::/home/ubuntu:/bin/bash\n",
		"/home/ubuntu/.ssh/authorized_keys": launch + "\n",
	}}
}

func TestApplyIdempotentAndPreservesLaunchKey(t *testing.T) {
	g := newGuest()
	tgt := Target{Node: "p0", VMID: 101, User: "ubuntu"}
	keys := []string{"ssh-ed25519 AAAAbob pmox-access:bob"}

	res, err := Apply(context.Background(), g, tgt, keys)
	if err != nil || !res.Changed || res.Path != "/home/ubuntu/.ssh/authorized_keys" {
		t.Fatalf("first apply: %+v %v", res, err)
	}
	if !strings.HasPrefix(g.files[res.Path], launch+"\n") {
		t.Errorf("launch key not preserved: %q", g.files[res.Path])
	}
	res, err = Apply(context.Background(), g, tgt, keys)
	if err != nil || res.Changed || g.writes != 1 {
		t.Fatalf("second apply should be a no-op: %+v %v writes=%d", res, err, g.writes)
	}
	_, got, err := Read(context.Background(), g, tgt)
	if err != nil || !slices.Equal(got, keys) {
		t.Errorf("Read = %v %v", got, err)
	}
	if _, err := Apply(context.Background(), g, tgt, nil); err != nil || g.files[res.Path] != launch+"\n" {
		t.Errorf("revoke-all should leave only the launch key: %q %v", g.files[res.Path], err)
	}
}

func TestApplyErrors(t *testing.T) {
	g := newGuest()
	if _, err := Apply(context.Background(), g, Target{User: "bob"}, []string{"k"}); !errors.Is(err, ErrUnknownUser) {
		t.Errorf("unknown user: %v", err)
	}
	delete(g.files, "/home/ubuntu/.ssh/authorized_keys")
	if _, err := Apply(context.Background(), g, Target{User: "ubuntu"}, []string{"k"}); !errors.Is(err, ErrNoAuthorizedKeys) {
		t.Errorf("missing file: %v", err)
	}
	if g.writes != 0 {
		t.Error("must not create authorized_keys")
	}
}
