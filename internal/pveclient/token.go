package pveclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// ticketClient builds an unauthenticated Client (no API token) for the
// ticket-based endpoints, sharing the API-token client's transport.
func ticketClient(baseURL string, insecure bool) *Client {
	return &Client{BaseURL: baseURL, Insecure: insecure, HTTPClient: newHTTPClient(insecure, Options{})}
}

// Login authenticates with a username (user@realm) and password against
// POST /access/ticket and returns a Ticket. The password is used only for
// this request and is never stored by pveclient.
func Login(ctx context.Context, baseURL string, insecure bool, username, password string) (Ticket, error) {
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)

	body, err := ticketClient(baseURL, insecure).do(ctx, http.MethodPost, "/access/ticket", nil, form, nil)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return Ticket{}, fmt.Errorf("%w: login failed (check username, realm, and password)", ErrUnauthorized)
		}
		return Ticket{}, err
	}

	data, err := decodeData[struct {
		Ticket string `json:"ticket"`
		CSRF   string `json:"CSRFPreventionToken"`
	}](body, "ticket response")
	if err != nil {
		return Ticket{}, err
	}
	if data.Ticket == "" {
		return Ticket{}, fmt.Errorf("%w: ticket response missing ticket", ErrAPIError)
	}
	return Ticket{Cookie: data.Ticket, CSRF: data.CSRF}, nil
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
	headers := http.Header{}
	headers.Set("Cookie", "PVEAuthCookie="+t.Cookie)
	headers.Set("CSRFPreventionToken", t.CSRF)

	body, err := ticketClient(baseURL, insecure).do(ctx, http.MethodPost, path, nil, form, headers)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && strings.Contains(strings.ToLower(string(apiErr.body)), "already exists") {
			return "", "", fmt.Errorf("%w: %s", ErrTokenExists, name)
		}
		return "", "", err
	}

	data, err := decodeData[struct {
		FullTokenID string `json:"full-tokenid"`
		Value       string `json:"value"`
	}](body, "token response")
	if err != nil {
		return "", "", err
	}
	if data.Value == "" || data.FullTokenID == "" {
		return "", "", fmt.Errorf("%w: token response missing value/full-tokenid", ErrAPIError)
	}
	return data.FullTokenID, data.Value, nil
}

// APIToken is one entry from GET /access/users/{userid}/token — an API
// token's bare name (not the full user@realm!name id) plus its comment.
type APIToken struct {
	TokenID string `json:"tokenid"`
	Comment string `json:"comment"`
}

// ListTokens fetches the API tokens configured for userid (user@realm),
// authenticating with the client's own API token.
func (c *Client) ListTokens(ctx context.Context, userid string) ([]APIToken, error) {
	path := fmt.Sprintf("/access/users/%s/token", url.PathEscape(userid))
	return getData[[]APIToken](ctx, c, path, nil, "token list response")
}

// DeleteToken removes the named API token from userid.
func (c *Client) DeleteToken(ctx context.Context, userid, name string) error {
	path := fmt.Sprintf("/access/users/%s/token/%s", url.PathEscape(userid), url.PathEscape(name))
	_, err := c.request(ctx, "DELETE", path, nil)
	return err
}
