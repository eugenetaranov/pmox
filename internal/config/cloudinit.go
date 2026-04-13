package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"text/template"
)

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
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pmox", "cloud-init"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "pmox", "cloud-init"), nil
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
		User     string
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

// WriteCloudInit unconditionally renders and writes the template at
// path. Unlike WriteStarterCloudInit it overwrites any existing file.
// Used by `pmox configure --regen-cloud-init` after the caller has
// obtained overwrite confirmation.
func WriteCloudInit(path, user, sshPubkey string) error {
	content, err := RenderTemplate(user, sshPubkey)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create cloud-init dir %s: %w", dir, err)
	}
	_ = os.Chmod(dir, 0o700)
	tmp, err := os.CreateTemp(dir, ".cloud-init-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
