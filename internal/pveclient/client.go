// Package pveclient is a minimal HTTP client for the Proxmox VE API.
// This slice ships the endpoints configure needs; pveclient-core extends it.
package pveclient

import (
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

// Client is a minimal Proxmox VE API client.
type Client struct {
	BaseURL    string
	TokenID    string
	Secret     string
	Insecure   bool
	HTTPClient *http.Client
}

// New constructs a Client with the given credentials and TLS mode.
func New(baseURL, tokenID, secret string, insecure bool) *Client {
	return &Client{
		BaseURL:  baseURL,
		TokenID:  tokenID,
		Secret:   secret,
		Insecure: insecure,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // homelab fallback per D4
			},
		},
	}
}

// request performs an authenticated API request and returns the response body.
func (c *Client) request(ctx context.Context, method, path string, query url.Values) ([]byte, error) {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", c.TokenID, c.Secret))
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if isTLSError(err) {
			return nil, fmt.Errorf("%w: %w", ErrTLSVerificationFailed, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrNetwork, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: %s", ErrUnauthorized, resp.Status)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, resp.Status)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("%w: %s: %s", ErrAPIError, resp.Status, summarizeBody(body))
	}
	return body, nil
}

// requestForm performs an authenticated API request whose body is a
// URL-encoded form. Used for POST/PUT/DELETE write-path endpoints.
// Content-Type is only set when the form is non-empty so that body-less
// calls don't send a misleading header.
func (c *Client) requestForm(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	var body io.Reader
	hasBody := len(form) > 0
	if hasBody {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", c.TokenID, c.Secret))
	req.Header.Set("Accept", "application/json")
	if hasBody {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if isTLSError(err) {
			return nil, fmt.Errorf("%w: %w", ErrTLSVerificationFailed, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrNetwork, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: %s", ErrUnauthorized, resp.Status)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, resp.Status)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("%w: %s: %s", ErrAPIError, resp.Status, summarizeBody(respBody))
	}
	return respBody, nil
}

// summarizeBody extracts a short human-readable message from a PVE
// error response body. PVE returns a JSON envelope like
// {"data":null,"errors":{"param":"reason"}} on 4xx — surfacing the
// errors map is much more useful than the bare HTTP status.
func summarizeBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "<empty response body>"
	}
	var envelope struct {
		Errors  map[string]string `json:"errors"`
		Message string            `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
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
