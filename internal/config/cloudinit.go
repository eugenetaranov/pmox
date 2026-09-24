package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/eugenetaranov/pmox/internal/atomicfile"
	"github.com/eugenetaranov/pmox/internal/paths"
)

// pubKeyPrefixes are the OpenSSH public-key type tokens pmox recognizes
// when scanning cloud-init ssh_authorized_keys entries.
var pubKeyPrefixes = []string{"ssh-", "ecdsa-", "sk-ssh-", "sk-ecdsa-"}

// pubKeyBody returns the base64 body (the unique middle field) of an
// OpenSSH public-key line, ignoring the type token and the comment.
// Returns "" if the line isn't a recognizable public key.
func pubKeyBody(line string) string {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	isKey := false
	for _, p := range pubKeyPrefixes {
		if strings.HasPrefix(fields[0], p) {
			isKey = true
			break
		}
	}
	if !isKey {
		return ""
	}
	return fields[1]
}

// CloudInitKeyBodies scans a cloud-init file for ssh_authorized_keys
// entries and returns their key bodies (base64 middles). A missing file
// returns an empty slice and no error.
func CloudInitKeyBodies(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if b := pubKeyBody(line); b != "" {
			out = append(out, b)
		}
	}
	return out, nil
}

// CloudInitAuthorizesKey reports whether the cloud-init file at path
// authorizes the given public key (compared by key body, ignoring the
// comment). ok is false when the file has no recognizable keys.
func CloudInitAuthorizesKey(path, pubKeyLine string) (authorized, hasAnyKey bool, err error) {
	want := pubKeyBody(pubKeyLine)
	bodies, err := CloudInitKeyBodies(path)
	if err != nil {
		return false, false, err
	}
	if len(bodies) == 0 {
		return false, false, nil
	}
	for _, b := range bodies {
		if want != "" && b == want {
			return true, true, nil
		}
	}
	return false, true, nil
}

//go:embed cloud-init.template.yaml
var cloudInitTemplate []byte

// ErrCloudInitExists is returned by WriteStarterCloudInit when a file
// already exists at the target path. Callers distinguish this from
// other write errors to print an idempotent "not overwriting" message
// without treating it as a failure.
var ErrCloudInitExists = errors.New("cloud-init template already exists")

// Slug turns a canonical server URL into a stable filesystem-safe
// identifier in the form `<host>-<port>`. The canonical URL produced
// by CanonicalizeURL always has a host and port, so an error means the
// caller passed something else.
func Slug(canonicalURL string) (string, error) {
	u, err := url.Parse(canonicalURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("url has no host: %s", canonicalURL)
	}
	port := u.Port()
	if port == "" {
		port = "8006"
	}
	return fmt.Sprintf("%s-%s", host, port), nil
}

// CloudInitDir returns the directory under which per-server cloud-init
// files live. It respects $XDG_CONFIG_HOME, falling back to
// $HOME/.config, matching Path().
func CloudInitDir() (string, error) {
	dir, err := paths.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cloud-init"), nil
}

// CloudInitPath returns the absolute path to the cloud-init file for
// the given canonical server URL.
func CloudInitPath(canonicalURL string) (string, error) {
	slug, err := Slug(canonicalURL)
	if err != nil {
		return "", err
	}
	dir, err := CloudInitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, slug+".yaml"), nil
}

// RenderTemplate substitutes the user and SSH public key into the
// embedded starter template and returns the rendered bytes. The output
// is always a valid UTF-8 text file under 64 KiB for any reasonable
// input, which means it will pass snippet.ValidateContent.
func RenderTemplate(user, sshPubkey string) ([]byte, error) {
	tmpl, err := template.New("cloud-init").Parse(string(cloudInitTemplate))
	if err != nil {
		return nil, fmt.Errorf("parse cloud-init template: %w", err)
	}
	var buf bytes.Buffer
	data := struct {
		User      string
		SSHPubkey string
	}{User: user, SSHPubkey: sshPubkey}
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render cloud-init template: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteStarterCloudInit writes the rendered template to path with
// mode 0600, creating the parent directory with mode 0700 if needed.
// The write is atomic (temp file + rename). If a file already exists
// at path, the function returns ErrCloudInitExists without touching
// it, so configure can be safely re-run without clobbering user edits.
func WriteStarterCloudInit(path, user, sshPubkey string) error {
	if _, err := os.Stat(path); err == nil {
		return ErrCloudInitExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	return WriteCloudInit(path, user, sshPubkey)
}

// ErrCloudInitKeyDrift is returned by EnsureStarterCloudInit when the
// existing file authorizes SSH keys, but not the selected one. It wraps
// ErrCloudInitExists.
var ErrCloudInitKeyDrift = fmt.Errorf("%w and authorizes a different SSH key", ErrCloudInitExists)

// EnsureStarterCloudInit writes the starter template like
// WriteStarterCloudInit. When the file already exists it is never
// touched: the result is ErrCloudInitKeyDrift if the file authorizes
// some SSH key but not sshPubkey (so re-running init with a new key can
// offer to regenerate it), else ErrCloudInitExists.
func EnsureStarterCloudInit(path, user, sshPubkey string) error {
	err := WriteStarterCloudInit(path, user, sshPubkey)
	if !errors.Is(err, ErrCloudInitExists) {
		return err
	}
	authorized, hasAny, aerr := CloudInitAuthorizesKey(path, sshPubkey)
	if aerr == nil && hasAny && !authorized {
		return ErrCloudInitKeyDrift
	}
	return ErrCloudInitExists
}

// WriteCloudInit unconditionally renders and writes the template at
// path. Unlike WriteStarterCloudInit it overwrites any existing file.
// Used by `pmox init --regen-cloud-init` after the caller has
// obtained overwrite confirmation.
func WriteCloudInit(path, user, sshPubkey string) error {
	content, err := RenderTemplate(user, sshPubkey)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(path, content, 0o600); err != nil {
		return fmt.Errorf("write cloud-init %s: %w", path, err)
	}
	// Tighten a pre-existing cloud-init dir.
	_ = os.Chmod(filepath.Dir(path), 0o700)
	return nil
}
