package pvessh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// testServer is an in-process SSH server with an SFTP subsystem backed
// by the real filesystem under a configurable rootDir. Host key is a
// fresh ed25519 keypair minted per test.
type testServer struct {
	addr       string
	rootDir    string
	hostKey    ssh.Signer
	hostPubB64 string // for known_hosts
	cfg        *ssh.ServerConfig
	listener   net.Listener
	wg         sync.WaitGroup
	disableSFTP bool

	validPassword string
	authorizedKey ssh.PublicKey
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	hostKey := genED25519(t)
	return &testServer{
		rootDir:       t.TempDir(),
		hostKey:       hostKey,
		validPassword: "hunter2",
	}
}

func (s *testServer) start(t *testing.T) {
	t.Helper()
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if s.validPassword != "" && string(pass) == s.validPassword {
				return nil, nil
			}
			return nil, errors.New("bad password")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if s.authorizedKey != nil && ssh.FingerprintSHA256(key) == ssh.FingerprintSHA256(s.authorizedKey) {
				return nil, nil
			}
			return nil, errors.New("unauthorized key")
		},
	}
	cfg.AddHostKey(s.hostKey)
	s.cfg = cfg

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.listener = ln
	s.addr = ln.Addr().String()

	s.wg.Add(1)
	go s.serve(t)
	t.Cleanup(func() {
		_ = ln.Close()
		s.wg.Wait()
	})
}

func (s *testServer) serve(t *testing.T) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(t, conn)
	}
}

func (s *testServer) handleConn(t *testing.T, nConn net.Conn) {
	_, chans, reqs, err := ssh.NewServerConn(nConn, s.cfg)
	if err != nil {
		_ = nConn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(t, ch, chReqs)
	}
}

func (s *testServer) handleSession(t *testing.T, ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			if len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
				if s.disableSFTP {
					_ = req.Reply(false, nil)
					_ = ch.Close()
					return
				}
				_ = req.Reply(true, nil)
				srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(s.rootDir))
				if err != nil {
					_ = ch.Close()
					return
				}
				_ = srv.Serve()
				_ = srv.Close()
				_ = ch.Close()
				return
			}
			_ = req.Reply(false, nil)
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *testServer) writeKnownHosts(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	host, _, _ := net.SplitHostPort(s.addr)
	_, port, _ := net.SplitHostPort(s.addr)
	// format: [host]:port keytype base64
	raw := ssh.MarshalAuthorizedKey(s.hostKey.PublicKey())
	// raw is "keytype base64 comment\n" — drop trailing newline
	line := "[" + host + "]:" + port + " " + string(raw)
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	return path
}

func genED25519(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer from key: %v", err)
	}
	return signer
}

// writeED25519Key writes an unencrypted ed25519 PEM to tmp and returns
// the path + the ssh.PublicKey the server should accept.
func writeED25519Key(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return path, signer.PublicKey()
}

// writeEncryptedED25519Key writes a passphrase-protected ed25519 key.
func writeEncryptedED25519Key(t *testing.T, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	pemBlock, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519_enc")
	if err := os.WriteFile(path, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return path, signer.PublicKey()
}
