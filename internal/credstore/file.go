package credstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/eugenetaranov/pmox/internal/atomicfile"
	"github.com/eugenetaranov/pmox/internal/paths"
)

// FileStoreURLs returns the canonical server URLs that have entries in
// the file secret store (secrets.yaml), sorted. It returns an empty slice
// when the file does not exist. The OS keychain cannot be enumerated, so
// this covers only the file backend.
func FileStoreURLs() ([]string, error) {
	m, err := defaultStore.file.load()
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
type fileBackend struct {
	// path resolves the secrets file location.
	path func() (string, error)
}

// defaultSecretsPath returns <config-dir>/secrets.yaml, respecting
// XDG_CONFIG_HOME and falling back to ~/.config/pmox.
func defaultSecretsPath() (string, error) {
	dir, err := paths.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "secrets.yaml"), nil
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

func (f *fileBackend) load() (map[string]map[string]string, error) {
	p, err := f.path()
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

func (f *fileBackend) save(m map[string]map[string]string) error {
	p, err := f.path()
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal secrets: %w", err)
	}
	if err := atomicfile.Write(p, data, 0o600); err != nil {
		return fmt.Errorf("write secrets file %s: %w", p, err)
	}
	// Tighten a pre-existing secrets dir.
	_ = os.Chmod(filepath.Dir(p), 0o700)
	return nil
}

func (f *fileBackend) get(account string) (string, error) {
	url, kind := splitAccount(account)
	m, err := f.load()
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

func (f *fileBackend) set(account, secret string) error {
	url, kind := splitAccount(account)
	m, err := f.load()
	if err != nil {
		return err
	}
	if m[url] == nil {
		m[url] = map[string]string{}
	}
	m[url][kind] = secret
	return f.save(m)
}

func (f *fileBackend) remove(account string) error {
	url, kind := splitAccount(account)
	m, err := f.load()
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
	return f.save(m)
}
