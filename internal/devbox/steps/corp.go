package steps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

func init() {
	RegisterGo("proxy", func(catalog.Step) Step { return proxyStep{} })
	RegisterGo("corpca", func(catalog.Step) Step { return corpCAStep{} })
}

// ProxyEnv is the proxy variables for a proxy, both spellings (tools read
// one or the other); none without a proxy.
func ProxyEnv(p answers.Proxy) []string {
	if p.URL == "" {
		return nil
	}
	var out []string
	for _, k := range []string{"http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY"} {
		out = append(out, k+"="+p.URL)
	}
	if p.NoProxy != "" {
		out = append(out, "no_proxy="+p.NoProxy, "NO_PROXY="+p.NoProxy)
	}
	return out
}

// --- go:proxy ----------------------------------------------------------------

// proxyStep sends apt, Docker (daemon and client) and login shells through
// the proxy. It runs with the apt repositories, before the run's packages
// are installed.
type proxyStep struct{}

const (
	aptProxy    = "/etc/apt/apt.conf.d/95devbox-proxy"
	etcEnv      = "/etc/environment"
	dockerProxy = "/etc/systemd/system/docker.service.d/proxy.conf"
)

func (proxyStep) BeforeApt() bool { return true }

func proxyOf(e *Env) answers.Proxy {
	if e.Answers == nil {
		return answers.Proxy{}
	}
	return e.Answers.Proxy
}

func (proxyStep) Plan(e *Env) []Action {
	p := proxyOf(e)
	if p.URL == "" {
		return []Action{{"note", "no proxy set"}}
	}
	return []Action{{"file", aptProxy}, {"line", etcEnv + " (proxy variables)"}, {"file", dockerProxy}, {"file", "~/.docker/config.json (proxies)"}}
}

func (proxyStep) files(e *Env) []wantFile {
	p := proxyOf(e)
	apt := fmt.Sprintf("// devbox-setup: the proxy (the proxy item).\nAcquire::http::Proxy %q;\nAcquire::https::Proxy %q;\n", p.URL, p.URL)
	var env bytes.Buffer
	env.WriteString("# devbox-setup: the proxy (the proxy item).\n[Service]\n")
	for _, kv := range ProxyEnv(p) {
		fmt.Fprintf(&env, "Environment=%q\n", kv)
	}
	return []wantFile{{aptProxy, []byte(apt), 0o644}, {dockerProxy, env.Bytes(), 0o644}}
}

// environment returns /etc/environment with the proxy variables set.
func (proxyStep) environment(e *Env) ([]byte, error) {
	b, err := os.ReadFile(e.Path(etcEnv))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, kv := range ProxyEnv(proxyOf(e)) {
		k, v, _ := strings.Cut(kv, "=")
		b = WithLine(b, fmt.Sprintf("%s=%q", k, v), "^"+k+"=")
	}
	return b, nil
}

// dockerClient returns the user's ~/.docker/config.json with the proxy in
// "proxies" (everything else kept), and whether that changes it.
func (proxyStep) dockerClient(e *Env) (b []byte, changed bool, err error) {
	cur, err := os.ReadFile(e.Path("~/.docker/config.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	doc := map[string]any{}
	if len(bytes.TrimSpace(cur)) > 0 {
		if err := json.Unmarshal(cur, &doc); err != nil {
			return nil, false, fmt.Errorf("~/.docker/config.json: %w", err)
		}
	}
	p := proxyOf(e)
	def := map[string]any{"httpProxy": p.URL, "httpsProxy": p.URL}
	if p.NoProxy != "" {
		def["noProxy"] = p.NoProxy
	}
	proxies, _ := doc["proxies"].(map[string]any)
	if proxies == nil {
		proxies = map[string]any{}
	}
	old, _ := json.Marshal(proxies["default"])
	proxies["default"] = def
	doc["proxies"] = proxies
	now, _ := json.Marshal(def)
	b, err = json.MarshalIndent(doc, "", "\t")
	return append(b, '\n'), !bytes.Equal(old, now), err
}

func (s proxyStep) Check(_ context.Context, e *Env) (bool, error) {
	if proxyOf(e).URL == "" {
		return true, nil
	}
	for _, f := range s.files(e) {
		if !same(e.Path(f.to), f) {
			return false, nil
		}
	}
	env, err := s.environment(e)
	if err != nil {
		return false, err
	}
	if cur, _ := os.ReadFile(e.Path(etcEnv)); !bytes.Equal(cur, env) {
		return false, nil
	}
	_, changed, err := s.dockerClient(e)
	return err == nil && !changed, nil
}

func (s proxyStep) Apply(ctx context.Context, e *Env) error {
	if proxyOf(e).URL == "" {
		return nil
	}
	dockerChanged := false
	for _, f := range s.files(e) {
		p := e.Path(f.to)
		if same(p, f) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := writeFile(p, f.b, f.mode); err != nil {
			return err
		}
		dockerChanged = dockerChanged || f.to == dockerProxy
	}
	env, err := s.environment(e)
	if err != nil {
		return err
	}
	if err := writeFile(e.Path(etcEnv), env, 0o644); err != nil {
		return err
	}
	client, changed, err := s.dockerClient(e)
	if err != nil {
		return err
	}
	if changed {
		if err := (mcpWireStep{}).userDir(e, "~/.docker"); err != nil {
			return err
		}
		if err := writeFile(e.Path("~/.docker/config.json"), client, 0o600); err != nil {
			return err
		}
		if err := e.chown(e.Path("~/.docker/config.json"), e.UID, e.GID); err != nil {
			return err
		}
	}
	if dockerChanged {
		// A Docker installed later in this run starts with the drop-in.
		if _, err := e.run(ctx, Cmd{Name: "systemctl", Args: []string{"daemon-reload"}}); err != nil {
			return err
		}
		if root(ctx, e, "dockerd") {
			if _, err := e.run(ctx, Cmd{Name: "systemctl", Args: []string{"try-restart", "docker"}}); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- go:corpca ---------------------------------------------------------------

// corpCAStep installs an internal CA bundle into the system store (one
// file per certificate), so curl, apt, Docker and git trust it; shell.sh
// points Node, Python requests and the AWS CLI at the store. It runs with
// the apt repositories, so apt can reach an internal mirror in the same run.
type corpCAStep struct{}

const (
	caDir    = "/usr/local/share/ca-certificates"
	caPrefix = "devbox-corp-"
)

func (corpCAStep) BeforeApt() bool { return true }

func caOf(e *Env) answers.CA {
	if e.Answers == nil {
		return answers.CA{}
	}
	return e.Answers.CA
}

func (corpCAStep) Plan(e *Env) []Action {
	if caOf(e).Source == "" {
		return []Action{{"note", "no CA bundle set"}}
	}
	return []Action{{"file", caDir + "/" + caPrefix + "*.crt (from " + caOf(e).Source + ")"}, {"run", "update-ca-certificates"}}
}

// certs fetches the bundle, checks it, and splits it into PEM certificates.
func (corpCAStep) certs(ctx context.Context, e *Env) ([][]byte, error) {
	c := caOf(e)
	var b []byte
	var err error
	if strings.HasPrefix(c.Source, "https://") {
		if e.Download == nil {
			return nil, errors.New("no downloader")
		}
		if b, err = e.Download(ctx, c.Source); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != c.SHA256 {
			return nil, fmt.Errorf("%s: sha256 is %s, not the %s given", c.Source, got, c.SHA256)
		}
	} else if b, err = os.ReadFile(e.Path(c.Source)); err != nil {
		return nil, err
	}
	var out [][]byte
	for rest := b; ; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if blk.Type == "CERTIFICATE" {
			out = append(out, pem.EncodeToMemory(blk))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no PEM certificates in it", c.Source)
	}
	return out, nil
}

func (s corpCAStep) want(ctx context.Context, e *Env) ([]wantFile, error) {
	certs, err := s.certs(ctx, e)
	if err != nil {
		return nil, err
	}
	var out []wantFile
	for i, c := range certs {
		out = append(out, wantFile{filepath.Join(caDir, fmt.Sprintf("%s%d.crt", caPrefix, i+1)), c, 0o644})
	}
	return out, nil
}

// stale lists the devbox certificates no longer in the bundle.
func stale(e *Env, keep []wantFile) []string {
	have, _ := filepath.Glob(e.Path(filepath.Join(caDir, caPrefix+"*.crt")))
	var out []string
	for _, p := range have {
		found := false
		for _, w := range keep {
			if e.Path(w.to) == p {
				found = true
			}
		}
		if !found {
			out = append(out, p)
		}
	}
	return out
}

func (s corpCAStep) Check(ctx context.Context, e *Env) (bool, error) {
	if caOf(e).Source == "" {
		return true, nil
	}
	want, err := s.want(ctx, e)
	if err != nil {
		return false, err
	}
	for _, w := range want {
		if !same(e.Path(w.to), w) {
			return false, nil
		}
	}
	return len(stale(e, want)) == 0, nil
}

func (s corpCAStep) Apply(ctx context.Context, e *Env) error {
	if caOf(e).Source == "" {
		return nil
	}
	want, err := s.want(ctx, e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.Path(caDir), 0o755); err != nil {
		return err
	}
	for _, p := range stale(e, want) {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	for _, w := range want {
		if err := writeFile(e.Path(w.to), w.b, w.mode); err != nil {
			return err
		}
	}
	if _, err := e.run(ctx, Cmd{Name: "update-ca-certificates", Args: []string{"--fresh"}}); err != nil {
		return err
	}
	if root(ctx, e, "dockerd") {
		_, err := e.run(ctx, Cmd{Name: "systemctl", Args: []string{"try-restart", "docker"}})
		return err
	}
	return nil
}
