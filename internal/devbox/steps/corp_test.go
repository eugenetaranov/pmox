package steps

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

func TestProxy(t *testing.T) {
	x := &fakeExec{}
	e := testEnv(t, x)
	e.Answers = &answers.Answers{Proxy: answers.Proxy{URL: "http://proxy.acme:3128", NoProxy: "localhost,.acme"}}
	put(t, e.Path("/etc/environment"), "PATH=\"/usr/local/sbin:/usr/bin\"\nhttp_proxy=\"http://old:1\"\n")
	put(t, e.Path("~/.docker/config.json"), `{"auths": {"ghcr.io": {}}}`)
	st := step(t, catalog.Step{Type: "go:proxy"})
	if _, ok := st.(BeforeApt); !ok {
		t.Fatal("proxy must run before the apt transaction")
	}
	if check(t, e, st) {
		t.Fatal("Check = true before Apply")
	}
	apply(t, e, st)
	if got := read(t, e.Path(aptProxy)); !strings.Contains(got, `Acquire::https::Proxy "http://proxy.acme:3128";`) {
		t.Errorf("apt: %s", got)
	}
	env := read(t, e.Path("/etc/environment"))
	for _, want := range []string{`PATH="/usr/local/sbin:/usr/bin"`, `http_proxy="http://proxy.acme:3128"`, `HTTPS_PROXY="http://proxy.acme:3128"`, `NO_PROXY="localhost,.acme"`} {
		if !strings.Contains(env, want+"\n") {
			t.Errorf("/etc/environment lacks %s:\n%s", want, env)
		}
	}
	if strings.Contains(env, "old:1") {
		t.Errorf("old proxy kept:\n%s", env)
	}
	if got := read(t, e.Path(dockerProxy)); !strings.Contains(got, `Environment="HTTPS_PROXY=http://proxy.acme:3128"`) {
		t.Errorf("docker drop-in: %s", got)
	}
	client := read(t, e.Path("~/.docker/config.json"))
	if !strings.Contains(client, `"ghcr.io"`) || !strings.Contains(client, `"httpsProxy": "http://proxy.acme:3128"`) || !strings.Contains(client, `"noProxy": "localhost,.acme"`) {
		t.Errorf("docker client config:\n%s", client)
	}
	if !check(t, e, st) {
		t.Error("Check = false after Apply")
	}
	if got := x.commands(); len(got) == 0 || got[0] != "systemctl daemon-reload" {
		t.Errorf("ran %q", got)
	}
}

func testCert(t *testing.T, cn string) []byte {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestCorpCA(t *testing.T) {
	x := &fakeExec{}
	e := testEnv(t, x)
	bundle := append(append([]byte("# Acme roots\n"), testCert(t, "Acme Root")...), testCert(t, "Acme Issuing")...)
	put(t, e.Path("/root/acme.pem"), string(bundle))
	e.Answers = &answers.Answers{CA: answers.CA{Source: "/root/acme.pem"}}
	st := step(t, catalog.Step{Type: "go:corpca"})
	apply(t, e, st)
	for _, n := range []string{"1", "2"} {
		if got := read(t, e.Path(caDir+"/devbox-corp-"+n+".crt")); !strings.HasPrefix(got, "-----BEGIN CERTIFICATE-----") {
			t.Errorf("cert %s: %q", n, got)
		}
	}
	if got := x.commands(); got[0] != "update-ca-certificates --fresh" {
		t.Errorf("ran %q", got)
	}
	if !check(t, e, st) {
		t.Error("Check = false after Apply")
	}
	// A smaller bundle later: the extra file goes.
	put(t, e.Path("/root/acme.pem"), string(testCert(t, "Acme Root 2")))
	apply(t, e, st)
	if _, err := os.Stat(e.Path(caDir + "/devbox-corp-2.crt")); err == nil {
		t.Error("devbox-corp-2.crt left behind")
	}
	// From a URL: the sha256 must match.
	e.Answers.CA = answers.CA{Source: "https://pki.acme/roots.pem", SHA256: strings.Repeat("0", 64)}
	e.Download = func(context.Context, string) ([]byte, error) { return bundle, nil }
	if err := st.Apply(context.Background(), e); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("wrong sha256: err = %v", err)
	}
	sum := sha256.Sum256(bundle)
	e.Answers.CA.SHA256 = hex.EncodeToString(sum[:])
	apply(t, e, st)
	if _, err := os.Stat(e.Path(caDir + "/devbox-corp-2.crt")); err != nil {
		t.Errorf("from the URL: %v", err)
	}
	put(t, e.Path("/root/bad.pem"), "not a cert")
	e.Answers.CA = answers.CA{Source: "/root/bad.pem"}
	if err := st.Apply(context.Background(), e); err == nil || !strings.Contains(err.Error(), "no PEM certificates") {
		t.Errorf("not PEM: err = %v", err)
	}
}
