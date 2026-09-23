package credstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileStoreURLs returns the canonical server URLs that have entries in
// the file secret store (secrets.yaml), sorted. It returns an empty slice
// when the file does not exist. The OS keychain cannot be enumerated, so
// this covers only the file backend.
func FileStoreURLs() ([]string, error) {
	m, err := loadSecrets()
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(m))
	for u := range m {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	return urls, nil
}

// fileBackend persists secrets in <config-dir>/pmox/secrets.yaml as a
// two-level map: canonical URL -> secret kind -> value. It is the
// fallback used when the OS keychain is unavailable. The file is 0600
// inside a 0700 dir and written atomically. Secrets never go into
// config.yaml.
type fileBackend struct{}

// secretsPath is a var so tests can point it at a temp file; the default
// respects XDG_CONFIG_HOME and falls back to ~/.config.
var secretsPath = func() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pmox", "secrets.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "pmox", "secrets.yaml"), nil
}

// splitAccount turns an account string into (url, kind). Accounts are a
// canonical URL, optionally with a "#<suffix>". Canonical URLs never
// contain "#", so the first "#" separates url from kind.
func splitAccount(account string) (url, kind string) {
	if i := strings.IndexByte(account, '#'); i >= 0 {
		return account[:i], account[i+1:]
	}
	return account, "token"
}

func loadSecrets() (map[string]map[string]string, error) {
	p, err := secretsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]map[string]string{}, nil
		}
		return nil, fmt.Errorf("read secrets file: %w", err)
	}
	var m map[string]map[string]string
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse secrets file %s: %w", p, err)
	}
	if m == nil {
		m = map[string]map[string]string{}
	}
	return m, nil
}

func saveSecrets(m map[string]map[string]string) error {
	p, err := secretsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create secrets dir %s: %w", dir, err)
	}
	_ = os.Chmod(dir, 0o700)
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal secrets: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".secrets-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

func (fileBackend) get(account string) (string, error) {
	url, kind := splitAccount(account)
	m, err := loadSecrets()
	if err != nil {
		return "", err
	}
	if km, ok := m[url]; ok {
		if v, ok := km[kind]; ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, account)
}

func (fileBackend) set(account, secret string) error {
	url, kind := splitAccount(account)
	m, err := loadSecrets()
	if err != nil {
		return err
	}
	if m[url] == nil {
		m[url] = map[string]string{}
	}
	m[url][kind] = secret
	return saveSecrets(m)
}

func (fileBackend) remove(account string) error {
	url, kind := splitAccount(account)
	m, err := loadSecrets()
	if err != nil {
		return err
	}
	km, ok := m[url]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, account)
	}
	if _, ok := km[kind]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, account)
	}
	delete(km, kind)
	if len(km) == 0 {
		delete(m, url)
	}
	return saveSecrets(m)
}
