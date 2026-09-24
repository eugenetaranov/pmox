// Package pveclient is a minimal HTTP client for the Proxmox VE API.
// This slice ships the endpoints configure needs; pveclient-core extends it.
package pveclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	// defaultTimeout bounds a single API request when Options.Timeout
	// is unset.
	defaultTimeout = 10 * time.Second
	// maxResponseBody caps how much of a response body is read into
	// memory; PVE responses are small, so this only guards against a
	// misbehaving server or proxy.
	maxResponseBody = 32 << 20
)

// Client is a minimal Proxmox VE API client.
type Client struct {
	BaseURL    string
	TokenID    string
	Secret     string
	Insecure   bool
	HTTPClient *http.Client
}

// Options holds optional Client settings for NewWithOptions.
type Options struct {
	// PinSHA256 is the expected SHA-256 fingerprint of the server's leaf
	// certificate, in the format FetchCertFingerprint returns (lowercase
	// hex; an optional "sha256:" prefix, colons and upper case are
	// tolerated). When set, every TLS handshake on the API connection
	// is checked against it and a mismatch fails with an error wrapping
	// ErrTLSVerificationFailed — so a pinned insecure server is
	// authenticated on the same connection that carries the token.
	PinSHA256 string
	// Timeout bounds each request. Zero means 10s.
	Timeout time.Duration
}

// New constructs a Client with the given credentials and TLS mode.
func New(baseURL, tokenID, secret string, insecure bool) *Client {
	return NewWithOptions(baseURL, tokenID, secret, insecure, Options{})
}

// NewWithOptions constructs a Client like New, applying opts (TLS pin,
// request timeout).
func NewWithOptions(baseURL, tokenID, secret string, insecure bool, opts Options) *Client {
	return &Client{
		BaseURL:    baseURL,
		TokenID:    tokenID,
		Secret:     secret,
		Insecure:   insecure,
		HTTPClient: newHTTPClient(insecure, opts),
	}
}

func newHTTPClient(insecure bool, opts Options) *http.Client {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	return &http.Client{Timeout: timeout, Transport: newTransport(insecure, opts.PinSHA256)}
}

// newTransport builds the HTTP transport shared by every pveclient
// entry point. insecure skips chain verification (homelab self-signed
// certs); pinSHA256, when non-empty, additionally requires the leaf
// certificate to match the pin on every handshake.
func newTransport(insecure bool, pinSHA256 string) *http.Transport {
	cfg := &tls.Config{InsecureSkipVerify: insecure} //nolint:gosec // homelab fallback per D4; pinned when PinSHA256 is set
	if pinSHA256 != "" {
		cfg.VerifyConnection = VerifyPin(pinSHA256)
	}
	return &http.Transport{TLSClientConfig: cfg}
}

// request performs an authenticated API request and returns the response body.
func (c *Client) request(ctx context.Context, method, path string, query url.Values) ([]byte, error) {
	return c.do(ctx, method, path, query, nil, nil)
}

// requestForm performs an authenticated API request whose body is a
// URL-encoded form. Used for POST/PUT/DELETE write-path endpoints.
func (c *Client) requestForm(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	return c.do(ctx, method, path, nil, form, nil)
}

// do is the single request core: it builds the URL, attaches the API
// token (when the client has one), sends an optional URL-encoded form
// body, and maps the outcome onto the package's error taxonomy —
// transport failures to ErrTLSVerificationFailed/ErrNetwork, and any
// status >= 400 to an *APIError. Content-Type is only set when the form
// is non-empty so that body-less calls don't send a misleading header.
// Extra headers (e.g. ticket cookie + CSRF token) are applied last.
func (c *Client) do(ctx context.Context, method, path string, query, form url.Values, headers http.Header) ([]byte, error) {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var body io.Reader
	if len(form) > 0 {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.TokenID != "" {
		req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", c.TokenID, c.Secret))
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, respBody, err := send(c.HTTPClient, req) //nolint:bodyclose // send reads and closes the body
	if err != nil {
		return nil, classifyTransportError(err)
	}
	if resp.StatusCode >= 400 {
		return nil, newAPIError(resp, respBody)
	}
	return respBody, nil
}

// send executes req and returns the response with its body fully read
// (bounded by maxResponseBody) and closed. Errors are returned raw so
// callers can classify them.
func send(hc *http.Client, req *http.Request) (*http.Response, []byte, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, nil, fmt.Errorf("read response body: %w", err)
	}
	return resp, body, nil
}

// classifyTransportError wraps a transport-level failure with
// ErrTLSVerificationFailed or ErrNetwork.
func classifyTransportError(err error) error {
	if errors.Is(err, ErrTLSVerificationFailed) {
		// Already classified (pin mismatch from VerifyPin).
		return err
	}
	if isTLSError(err) {
		return fmt.Errorf("%w: %w", ErrTLSVerificationFailed, err)
	}
	return fmt.Errorf("%w: %w", ErrNetwork, err)
}

// getData GETs path and decodes the {"data": ...} envelope into T.
// what names the response in parse errors ("parse <what>: ...").
func getData[T any](ctx context.Context, c *Client, path string, query url.Values, what string) (T, error) {
	body, err := c.request(ctx, "GET", path, query)
	if err != nil {
		var zero T
		return zero, err
	}
	return decodeData[T](body, what)
}

// decodeData decodes the {"data": ...} envelope of body into T.
func decodeData[T any](body []byte, what string) (T, error) {
	var env struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		var zero T
		return zero, fmt.Errorf("parse %s: %w", what, err)
	}
	return env.Data, nil
}

// errorEnvelope is the PVE error response shape:
// {"data":null,"errors":{"param":"reason"},"message":"..."}.
type errorEnvelope struct {
	Errors  map[string]string `json:"errors"`
	Message string            `json:"message"`
}

func parseErrorEnvelope(body []byte) (errorEnvelope, bool) {
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return errorEnvelope{}, false
	}
	return env, true
}

// summarizeBody extracts a short human-readable message from a PVE
// error response body. PVE returns a JSON envelope like
// {"data":null,"errors":{"param":"reason"}} on 4xx — surfacing the
// errors map is much more useful than the bare HTTP status.
func summarizeBody(body []byte) string {
	s := string(bytes.TrimSpace(body))
	if s == "" {
		return "<empty response body>"
	}
	if envelope, ok := parseErrorEnvelope(body); ok {
		if len(envelope.Errors) > 0 {
			parts := make([]string, 0, len(envelope.Errors))
			for k, v := range envelope.Errors {
				parts = append(parts, fmt.Sprintf("%s: %s", k, v))
			}
			sort.Strings(parts)
			if envelope.Message != "" {
				return envelope.Message + " (" + strings.Join(parts, "; ") + ")"
			}
			return strings.Join(parts, "; ")
		}
		if envelope.Message != "" {
			return envelope.Message
		}
	}
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	return s
}

func isTLSError(err error) bool {
	if errors.Is(err, ErrTLSVerificationFailed) {
		return true
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return true
	}
	var unknownAuth x509.UnknownAuthorityError
	if errors.As(err, &unknownAuth) {
		return true
	}
	var hostErr x509.HostnameError
	return errors.As(err, &hostErr)
}
