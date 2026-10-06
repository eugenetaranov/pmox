// Package guestkeys maintains pmox's managed block of SSH public keys in
// a guest user's ~/.ssh/authorized_keys, using only the QEMU guest
// agent's file-read / file-write API (no command execution).
//
// The block is delimited by marker lines; everything outside it (the
// cloud-init launch key, anything the user added by hand) is never
// changed:
//
//	ssh-ed25519 AAAA… alice@ws1
//	# pmox-access begin (managed by pmox - edits inside this block are overwritten)
//	ssh-ed25519 AAAA… pmox-access:bob
//	# pmox-access end
package guestkeys

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	// BeginMarker opens the managed block; any line starting with
	// beginPrefix is recognized, so the explanatory suffix can change.
	BeginMarker = "# pmox-access begin (managed by pmox - edits inside this block are overwritten)"
	EndMarker   = "# pmox-access end"
	beginPrefix = "# pmox-access begin"

	// maxWrite is PVE's limit on agent file-write content.
	maxWrite = 60 * 1024
)

var (
	// ErrUnknownUser means the login user isn't in the guest's /etc/passwd.
	ErrUnknownUser = errors.New("user not found in the guest's /etc/passwd")
	// ErrNoAuthorizedKeys means the user has no ~/.ssh/authorized_keys.
	// pmox refuses to create one: the agent would create it as root, and
	// sshd's StrictModes rejects a root-owned file in a user's home.
	ErrNoAuthorizedKeys = errors.New("the user has no ~/.ssh/authorized_keys")
	// ErrTooLarge means the file exceeds what the guest agent can move.
	ErrTooLarge = errors.New("authorized_keys is too large to edit through the guest agent")
)

// Agent is the subset of pveclient.Client guestkeys needs.
type Agent interface {
	AgentFileRead(ctx context.Context, node string, vmid int, path string) ([]byte, bool, error)
	AgentFileWrite(ctx context.Context, node string, vmid int, path string, content []byte) error
}

// Target identifies a guest user on a VM.
type Target struct {
	Node string
	VMID int
	User string
}

// Result describes what Apply did.
type Result struct {
	Path    string   // authorized_keys path inside the guest
	Before  []string // managed key lines before
	After   []string // managed key lines after
	Changed bool     // false when the block already matched
}

// HomeDir finds user's home directory in /etc/passwd content.
func HomeDir(passwd []byte, user string) (string, bool) {
	for _, line := range strings.Split(string(passwd), "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), ":")
		if len(f) >= 6 && f[0] == user && f[5] != "" {
			return f[5], true
		}
	}
	return "", false
}

// ManagedKeys returns the key lines inside the managed block (nil when
// there is no block).
func ManagedKeys(content []byte) []string {
	var keys []string
	in := false
	for _, line := range splitLines(string(content)) {
		switch {
		case strings.HasPrefix(line, beginPrefix):
			in = true
		case line == EndMarker:
			in = false
		case in && strings.TrimSpace(line) != "":
			keys = append(keys, line)
		}
	}
	return keys
}

// Render returns content with the managed block set to keys: replaced
// in place when present, appended when absent, removed entirely when
// keys is empty. Lines outside the block, the file's line ending (\n or
// \r\n) and a trailing newline are preserved.
func Render(content []byte, keys []string) []byte {
	text := string(content)
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}

	var before, after []string
	state := 0 // 0 before block, 1 inside, 2 after
	for _, line := range splitLines(text) {
		switch {
		case state == 0 && strings.HasPrefix(line, beginPrefix):
			state = 1
		case state == 1 && line == EndMarker:
			state = 2
		case state == 0:
			before = append(before, line)
		case state == 2:
			after = append(after, line)
		}
	}
	// An unterminated block swallows the rest of the file; that's the
	// conservative reading — those lines were pmox-managed.

	out := append([]string{}, before...)
	if len(keys) > 0 {
		out = append(out, BeginMarker)
		out = append(out, keys...)
		out = append(out, EndMarker)
	}
	out = append(out, after...)
	if len(out) == 0 {
		return []byte{}
	}
	return []byte(strings.Join(out, eol) + eol)
}

// splitLines splits on \n, trimming a trailing \r per line and dropping
// the empty element after a final newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
	return lines
}

// locate resolves the authorized_keys path for t.User and reads it.
func locate(ctx context.Context, a Agent, t Target) (string, []byte, error) {
	passwd, _, err := a.AgentFileRead(ctx, t.Node, t.VMID, "/etc/passwd")
	if err != nil {
		return "", nil, fmt.Errorf("read /etc/passwd: %w", err)
	}
	home, ok := HomeDir(passwd, t.User)
	if !ok {
		return "", nil, fmt.Errorf("%w: %s", ErrUnknownUser, t.User)
	}
	p := path.Join(home, ".ssh", "authorized_keys")
	content, truncated, err := a.AgentFileRead(ctx, t.Node, t.VMID, p)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such file") {
			return p, nil, fmt.Errorf("%w (%s)", ErrNoAuthorizedKeys, p)
		}
		return p, nil, fmt.Errorf("read %s: %w", p, err)
	}
	if truncated {
		return p, nil, fmt.Errorf("%w (%s)", ErrTooLarge, p)
	}
	return p, content, nil
}

// Read returns the authorized_keys path and the managed key lines for t.
func Read(ctx context.Context, a Agent, t Target) (string, []string, error) {
	p, content, err := locate(ctx, a, t)
	if err != nil {
		return p, nil, err
	}
	return p, ManagedKeys(content), nil
}

// Apply sets t's managed block to keys, writing only when it changes.
func Apply(ctx context.Context, a Agent, t Target, keys []string) (Result, error) {
	p, content, err := locate(ctx, a, t)
	if err != nil {
		return Result{Path: p}, err
	}
	res := Result{Path: p, Before: ManagedKeys(content), After: keys}
	if equal(res.Before, keys) {
		return res, nil
	}
	next := Render(content, keys)
	if len(next) > maxWrite {
		return res, fmt.Errorf("%w (%s, %d bytes)", ErrTooLarge, p, len(next))
	}
	if err := a.AgentFileWrite(ctx, t.Node, t.VMID, p, next); err != nil {
		return res, fmt.Errorf("write %s: %w", p, err)
	}
	res.Changed = true
	return res, nil
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
