package pveclient

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// FetchCertFingerprint dials the server's TLS port and returns the
// SHA-256 fingerprint (lowercase hex) of its leaf certificate. It does
// NOT verify the certificate chain — it is used to pin a self-signed
// homelab cert on first use and to detect later changes. The dial
// respects ctx's deadline.
func FetchCertFingerprint(ctx context.Context, baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse server url: %w", err)
	}
	host := u.Host
	if u.Port() == "" {
		host = u.Hostname() + ":8006"
	}
	dialer := &tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // fingerprint probe; we pin, not trust
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return "", fmt.Errorf("%w: tls dial %s: %w", ErrNetwork, host, err)
	}
	defer func() { _ = conn.Close() }()

	tconn, ok := conn.(*tls.Conn)
	if !ok {
		return "", fmt.Errorf("tls dial %s: unexpected connection type", host)
	}
	certs := tconn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("tls dial %s: server presented no certificate", host)
	}
	return CertFingerprint(certs[0].Raw), nil
}

// CertFingerprint returns the SHA-256 fingerprint of a DER-encoded
// certificate as lowercase hex — the format stored as a TLS pin.
func CertFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// normalizePin canonicalizes a pin to lowercase hex, tolerating a
// "sha256:" prefix and colon-separated byte pairs.
func normalizePin(pin string) string {
	pin = strings.ToLower(strings.TrimSpace(pin))
	pin = strings.TrimPrefix(pin, "sha256:")
	return strings.ReplaceAll(pin, ":", "")
}

// VerifyPin returns a tls.Config.VerifyConnection callback that accepts
// the connection only if the peer's leaf certificate matches pin (see
// CertFingerprint). A mismatch returns an error wrapping
// ErrTLSVerificationFailed. It runs on every handshake, including
// resumed sessions, so the pin guards the connection that actually
// carries credentials.
func VerifyPin(pin string) func(tls.ConnectionState) error {
	want := normalizePin(pin)
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return fmt.Errorf("%w: server presented no certificate", ErrTLSVerificationFailed)
		}
		got := CertFingerprint(cs.PeerCertificates[0].Raw)
		if got != want {
			return fmt.Errorf("%w: certificate sha256:%s does not match pinned sha256:%s", ErrTLSVerificationFailed, got, want)
		}
		return nil
	}
}
