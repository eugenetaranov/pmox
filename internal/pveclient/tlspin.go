package pveclient

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/url"
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
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}
