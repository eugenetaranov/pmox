package pveclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrTokenExists is returned by CreateToken when a token of the requested
// name already exists (its secret cannot be re-fetched, so the caller must
// pick a different name).
var ErrTokenExists = errors.New("api token already exists")

// Ticket holds the session credentials returned by /access/ticket: the
// auth cookie value and the CSRF token required for writes.
type Ticket struct {
	Cookie string
	CSRF   string
}

// ticketHTTPClient builds an HTTP client honoring the insecure TLS flag,
// matching the API-token client's transport.
func ticketHTTPClient(insecure bool) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // homelab fallback per D4
		},
	}
}

// Login authenticates with a username (user@realm) and password against
// POST /access/ticket and returns a Ticket. The password is used only for
// this request and is never stored by pveclient.
func Login(ctx context.Context, baseURL string, insecure bool, username, password string) (Ticket, error) {
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/access/ticket", strings.NewReader(form.Encode()))
	if err != nil {
		return Ticket{}, fmt.Errorf("build ticket request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := ticketHTTPClient(insecure).Do(req)
	if err != nil {
		if isTLSError(err) {
			return Ticket{}, fmt.Errorf("%w: %w", ErrTLSVerificationFailed, err)
		}
		return Ticket{}, fmt.Errorf("%w: %w", ErrNetwork, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Ticket{}, fmt.Errorf("%w: login failed (check username, realm, and password)", ErrUnauthorized)
	}
	if resp.StatusCode >= 400 {
		return Ticket{}, fmt.Errorf("%w: %s: %s", ErrAPIError, resp.Status, summarizeBody(body))
	}

	var env struct {
		Data struct {
			Ticket string `json:"ticket"`
			CSRF   string `json:"CSRFPreventionToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return Ticket{}, fmt.Errorf("parse ticket response: %w", err)
	}
	if env.Data.Ticket == "" {
		return Ticket{}, fmt.Errorf("%w: ticket response missing ticket", ErrAPIError)
	}
	return Ticket{Cookie: env.Data.Ticket, CSRF: env.Data.CSRF}, nil
}

// CreateToken creates an API token named `name` for `userid` (user@realm)
// with privilege separation disabled (privsep=0), so it inherits the
// user's privileges. It returns the full token id (e.g. "root@pam!pmox")
// and the secret value, which the PVE API returns exactly once. A
// name collision yields ErrTokenExists.
func CreateToken(ctx context.Context, baseURL string, insecure bool, t Ticket, userid, name string) (fullTokenID, secret string, err error) {
	path := fmt.Sprintf("/access/users/%s/token/%s", url.PathEscape(userid), url.PathEscape(name))
	form := url.Values{}
	form.Set("privsep", "0")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", "PVEAuthCookie="+t.Cookie)
	req.Header.Set("CSRFPreventionToken", t.CSRF)

	resp, err := ticketHTTPClient(insecure).Do(req)
	if err != nil {
		if isTLSError(err) {
			return "", "", fmt.Errorf("%w: %w", ErrTLSVerificationFailed, err)
		}
		return "", "", fmt.Errorf("%w: %w", ErrNetwork, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		if strings.Contains(strings.ToLower(string(body)), "already exists") {
			return "", "", fmt.Errorf("%w: %s", ErrTokenExists, name)
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return "", "", fmt.Errorf("%w: %s", ErrUnauthorized, resp.Status)
		}
		return "", "", fmt.Errorf("%w: %s: %s", ErrAPIError, resp.Status, summarizeBody(body))
	}

	var env struct {
		Data struct {
			FullTokenID string `json:"full-tokenid"`
			Value       string `json:"value"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return "", "", fmt.Errorf("parse token response: %w", err)
	}
	if env.Data.Value == "" || env.Data.FullTokenID == "" {
		return "", "", fmt.Errorf("%w: token response missing value/full-tokenid", ErrAPIError)
	}
	return env.Data.FullTokenID, env.Data.Value, nil
}
