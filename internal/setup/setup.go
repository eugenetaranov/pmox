// Package setup holds the non-interactive business logic behind
// `pmox init`: persisting a configured server and its secrets, removing
// one, and deciding the TLS mode for a new endpoint. It never prompts or
// prints; the CLI layer turns its results into messages.
package setup

import (
	"context"
	"errors"
	"fmt"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// Secrets are the per-server values init stores in the secret store
// rather than in config.yaml. Empty node-SSH fields clear any stale
// entry left by an earlier configuration.
type Secrets struct {
	Token                string // API token secret (required)
	NodeSSHPassword      string
	NodeSSHKeyPassphrase string
}

// SaveServer adds srv to cfg under canonicalURL, saves the config, and
// stores sec. If the token secret can't be stored, the server is removed
// from cfg again and the config re-saved (best effort) so no server is
// left configured without its credentials.
func SaveServer(cfg *config.Config, canonicalURL string, srv *config.Server, sec Secrets) error {
	cfg.AddServer(canonicalURL, srv)
	if err := cfg.Save(); err != nil {
		return err
	}
	if err := credstore.Set(canonicalURL, sec.Token); err != nil {
		// Best-effort revert: delete the server we just added and re-save.
		cfg.RemoveServer(canonicalURL)
		_ = cfg.Save()
		return fmt.Errorf("save secret to keychain: %w", err)
	}
	if sec.NodeSSHPassword != "" {
		if err := credstore.SetNodeSSHPassword(canonicalURL, sec.NodeSSHPassword); err != nil {
			return fmt.Errorf("save node ssh password to keychain: %w", err)
		}
	} else {
		_ = credstore.RemoveNodeSSHPassword(canonicalURL)
	}
	if sec.NodeSSHKeyPassphrase != "" {
		if err := credstore.SetNodeSSHKeyPassphrase(canonicalURL, sec.NodeSSHKeyPassphrase); err != nil {
			return fmt.Errorf("save node ssh key passphrase to keychain: %w", err)
		}
	} else {
		_ = credstore.RemoveNodeSSHKeyPassphrase(canonicalURL)
	}
	return nil
}

// RemoveServer canonicalizes rawURL, drops that server from the saved
// config (clearing the current context if it no longer resolves), and
// deletes all of its stored secrets. It returns the canonical URL, or an
// error wrapping credstore.ErrNotFound when the server isn't configured.
func RemoveServer(rawURL string) (string, error) {
	canonical, err := config.CanonicalizeURL(rawURL)
	if err != nil {
		return "", err
	}
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	if !cfg.RemoveServer(canonical) {
		return "", fmt.Errorf("%w: server %s is not configured", credstore.ErrNotFound, canonical)
	}
	// Clear the current context if it no longer resolves after removal.
	if cfg.CurrentContext != "" {
		if _, ok := cfg.ContextByName(cfg.CurrentContext); !ok {
			cfg.CurrentContext = ""
		}
	}
	if err := cfg.Save(); err != nil {
		return "", err
	}
	if err := credstore.RemoveAll(canonical); err != nil {
		return "", err
	}
	return canonical, nil
}

// ProbeFunc classifies an endpoint; pveclient.Probe in production.
type ProbeFunc func(ctx context.Context, baseURL string, insecure bool) (pveclient.ReachStatus, error)

// Reach is the outcome of ProbeTLS.
type Reach struct {
	// Status is pveclient.Reachable when the endpoint is a PVE API
	// reachable in the TLS mode given by Insecure.
	Status pveclient.ReachStatus
	// Insecure is true when the endpoint was only reachable with
	// certificate verification skipped. Callers should warn about it.
	Insecure bool
	// Err is the strict probe's error, for reporting a failure.
	Err error
}

// ProbeTLS probes canonicalURL with strict TLS and, if the certificate
// is untrusted, confirms the host is reachable when verification is
// skipped. An untrusted endpoint that also fails the insecure probe
// keeps Status ReachTLSUntrusted.
func ProbeTLS(ctx context.Context, probe ProbeFunc, canonicalURL string) Reach {
	status, err := probe(ctx, canonicalURL, false)
	if status == pveclient.ReachTLSUntrusted {
		// Confirm the host is actually reachable when we ignore the cert.
		if s2, _ := probe(ctx, canonicalURL, true); s2 == pveclient.Reachable {
			return Reach{Status: pveclient.Reachable, Insecure: true}
		}
	}
	return Reach{Status: status, Err: err}
}

// PinOptions returns the pveclient options for a connection in the given TLS
// mode. A stored pin only applies to insecure connections: a strictly
// verified certificate is already authenticated by its CA chain (and may
// legitimately differ from the self-signed cert that was pinned).
func PinOptions(insecure bool, pin string) pveclient.Options {
	if !insecure {
		return pveclient.Options{}
	}
	return pveclient.Options{PinSHA256: pin}
}

// VerifyToken confirms the API token works by calling GET /version and
// returns the TLS-insecure mode that succeeded. When knownInsecure is
// true (a probe already settled on insecure TLS) it connects insecurely
// directly. Otherwise it tries strict TLS first and falls back to
// insecure only on a TLS verification error; a true result with
// knownInsecure false means that fallback happened and should be warned
// about. pin, when non-empty (re-configuring a server whose certificate
// was pinned), is enforced on insecure connections so a swapped
// certificate fails the handshake before the token is sent.
func VerifyToken(ctx context.Context, baseURL, tokenID, secret string, knownInsecure bool, pin string) (insecure bool, err error) {
	getVersion := func(insecure bool) error {
		_, err := pveclient.NewWithOptions(baseURL, tokenID, secret, insecure, PinOptions(insecure, pin)).GetVersion(ctx)
		return err
	}
	if knownInsecure {
		if err := getVersion(true); err != nil {
			return false, err
		}
		return true, nil
	}
	err = getVersion(false)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		return false, err
	}
	// Retry insecure.
	if err2 := getVersion(true); err2 != nil {
		return false, err2
	}
	return true, nil
}

// TokenIssuer creates API tokens for one PVE user using a login ticket,
// so several names can be tried without logging in again.
type TokenIssuer struct {
	baseURL  string
	insecure bool
	opts     pveclient.Options
	user     string
	ticket   pveclient.Ticket
}

// Login authenticates user (user@realm) with password and returns an
// issuer bound to the resulting ticket. The password is not retained.
// pin, when non-empty, is enforced on insecure connections (see
// VerifyToken) for both the login and later token creation.
func Login(ctx context.Context, baseURL string, insecure bool, pin, user, password string) (*TokenIssuer, error) {
	opts := PinOptions(insecure, pin)
	ticket, err := pveclient.LoginWithOptions(ctx, baseURL, insecure, opts, user, password)
	if err != nil {
		return nil, err
	}
	return &TokenIssuer{baseURL: baseURL, insecure: insecure, opts: opts, user: user, ticket: ticket}, nil
}

// Create makes an API token named name with privilege separation off and
// returns its full id (user@realm!name) and secret. A name collision
// yields pveclient.ErrTokenExists.
func (t *TokenIssuer) Create(ctx context.Context, name string) (tokenID, secret string, err error) {
	return pveclient.CreateTokenWithOptions(ctx, t.baseURL, t.insecure, t.opts, t.ticket, t.user, name)
}
