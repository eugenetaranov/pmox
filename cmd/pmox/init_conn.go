package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/setup"
	"github.com/eugenetaranov/pmox/internal/tui"
)

var tokenIDRegex = regexp.MustCompile(`^[^@!]+@[^@!]+![^@!]+$`)

// Injectable for tests.
var (
	probeEndpoint = pveclient.Probe
	interactiveFn = tui.Interactive
)

// promptReachableURL prompts for the API URL, normalizes it, and probes
// the endpoint before returning. On a network failure (or a response that
// isn't a PVE API) in interactive mode it prints a specific error and
// re-asks; a blank line or Ctrl-C aborts cleanly. In non-interactive mode
// it fails fast on the first failure instead of looping. The returned
// bool is the TLS-insecure decision the probe settled on.
func promptReachableURL(ctx context.Context, p prompter) (string, bool, error) {
	interactive := interactiveFn()
	for {
		raw, err := p.Prompt("Proxmox API URL: ")
		if err != nil {
			return "", false, err
		}
		if strings.TrimSpace(raw) == "" {
			return "", false, fmt.Errorf("%w: no URL entered", exitcode.ErrUserInput)
		}
		canonical, upgraded, cerr := config.CanonicalizeURLVerbose(raw)
		if cerr != nil {
			p.Errf("%v\n", cerr)
			if !interactive {
				return "", false, cerr
			}
			continue
		}
		if upgraded {
			p.Errf("note: using https (upgraded from http): %s\n", canonical)
		}

		insecure, ok := probeURL(ctx, p, canonical)
		if ok {
			return canonical, insecure, nil
		}
		if !interactive {
			return "", false, fmt.Errorf("%w: %s is not reachable", exitcode.ErrUserInput, canonical)
		}
		// Loop and re-ask.
	}
}

// probeURL classifies the endpoint and prints a specific message on
// failure. It returns (insecure, ok): ok is true when the endpoint is a
// reachable PVE API, with insecure indicating whether TLS verification
// had to be skipped.
func probeURL(ctx context.Context, p prompter, canonical string) (insecure bool, ok bool) {
	r := setup.ProbeTLS(ctx, probeEndpoint, canonical)
	switch r.Status {
	case pveclient.Reachable:
		if r.Insecure {
			warnTLSFallback(p, canonical)
		}
		return r.Insecure, true
	case pveclient.ReachTLSUntrusted:
		p.Errf("cannot reach %s: %v\n", canonical, r.Err)
		return false, false
	case pveclient.ReachNotPVE:
		p.Errf("%s responded but does not look like a Proxmox VE API — check the address\n", canonical)
		return false, false
	default: // ReachUnreachable / ReachUnknown
		p.Errf("nothing responding at %s — check the address and that Proxmox is running\n", hostPort(canonical))
		return false, false
	}
}

// warnTLSFallback tells the user TLS verification failed for baseURL and
// that pmox fell back to (and will record) insecure mode.
func warnTLSFallback(p prompter, baseURL string) {
	p.Errf("WARNING: TLS verification failed for %s\n", baseURL)
	p.Errf("         falling back to insecure mode; the certificate will not be verified.\n")
	p.Errf("         to re-enable, set 'insecure: false' in ~/.config/pmox/config.yaml.\n")
}

// hostPort extracts host:port from a canonical URL for error messages,
// falling back to the full URL if it cannot be parsed.
func hostPort(canonical string) string {
	if u, err := url.Parse(canonical); err == nil && u.Host != "" {
		return u.Host
	}
	return canonical
}

// selectTokenSourceFn presents the token-source choice as an up/down
// selector. It is a seam so tests can drive the choice without a TTY.
var selectTokenSourceFn = func() (string, error) {
	return tui.SelectOne("API token", []huh.Option[string]{
		huh.NewOption("Generate a new token (log in)", "generate"),
		huh.NewOption("Paste an existing token", "paste"),
	}, "generate")
}

// acquireToken obtains an API token id + secret. Interactively it offers
// a choice between pasting an existing token and logging in to generate
// one; non-interactively (and on the paste choice) it prompts for the
// token id and secret directly.
func acquireToken(ctx context.Context, p prompter, baseURL string, insecure bool) (tokenID, secret string, err error) {
	if interactiveFn() {
		choice, cerr := selectTokenSourceFn()
		if cerr != nil {
			return "", "", cerr
		}
		if choice == "generate" {
			tokenID, secret, err = generateToken(ctx, p, baseURL, insecure)
			if err == nil {
				return tokenID, secret, nil
			}
			if errors.Is(err, pveclient.ErrUnauthorized) {
				p.Errf("login failed; falling back to pasting an existing token\n")
				// fall through to the paste path
			} else {
				return "", "", err
			}
		}
	}
	tokenID, err = promptTokenID(p)
	if err != nil {
		return "", "", err
	}
	secret, err = promptSecret(p)
	if err != nil {
		return "", "", err
	}
	return tokenID, secret, nil
}

// generateToken logs in with a username/password and creates a new API
// token (privsep=0). The password is used only for the login ticket and
// is never stored. On a name collision it re-prompts for a new name.
func generateToken(ctx context.Context, p prompter, baseURL string, insecure bool) (tokenID, secret string, err error) {
	user, err := promptLoginUser(p)
	if err != nil {
		return "", "", err
	}
	password, err := p.PromptSecret(fmt.Sprintf("Password for %s: ", user))
	if err != nil {
		return "", "", err
	}
	issuer, err := setup.Login(ctx, baseURL, insecure, user, password)
	if err != nil {
		return "", "", err
	}
	for {
		name, nerr := promptTokenName(p)
		if nerr != nil {
			return "", "", nerr
		}
		full, value, cerr := issuer.Create(ctx, name)
		if cerr == nil {
			p.Printf("created API token %s (privilege separation off)\n", full)
			return full, value, nil
		}
		if errors.Is(cerr, pveclient.ErrTokenExists) {
			p.Errf("a token named %q already exists; choose another name\n", name)
			continue
		}
		return "", "", cerr
	}
}

// promptLoginUser prompts for a PVE login in user@realm form.
func promptLoginUser(p prompter) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		s, err := p.Prompt("PVE login (user@realm) [root@pam]: ")
		if err != nil {
			return "", err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			s = "root@pam"
		}
		if strings.Contains(s, "@") && !strings.Contains(s, "!") {
			return s, nil
		}
		p.Errf("login must be in the form 'user@realm' (e.g. root@pam)\n")
	}
	return "", fmt.Errorf("%w: too many invalid login attempts", exitcode.ErrUserInput)
}

// promptTokenName prompts for a new API token name (the segment after '!').
func promptTokenName(p prompter) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		s, err := p.Prompt("New API token name [pmox]: ")
		if err != nil {
			return "", err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			s = "pmox"
		}
		if !strings.ContainsAny(s, " \t@!") {
			return s, nil
		}
		p.Errf("token name may not contain spaces, '@', or '!'\n")
	}
	return "", fmt.Errorf("%w: too many invalid token name attempts", exitcode.ErrUserInput)
}

func promptTokenID(p prompter) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		s, err := p.Prompt("API token ID: ")
		if err != nil {
			return "", err
		}
		s = strings.TrimSpace(s)
		if tokenIDRegex.MatchString(s) {
			return s, nil
		}
		p.Errf("token ID must be in the form 'user@realm!tokenname' (got: '%s')\n", s)
	}
	return "", fmt.Errorf("%w: too many invalid token ID attempts", exitcode.ErrUserInput)
}

func promptSecret(p prompter) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		s, err := p.PromptSecret("API token secret: ")
		if err != nil {
			return "", err
		}
		if s != "" {
			return s, nil
		}
		p.Errf("token secret cannot be empty\n")
	}
	return "", fmt.Errorf("%w: too many empty secret attempts", exitcode.ErrUserInput)
}

// validateCredentials runs GetVersion to confirm the token works. When
// knownInsecure is true the reachability probe already settled on (and
// warned about) insecure TLS, so it connects insecurely directly without
// re-warning. Otherwise it tries strict TLS first and falls back to
// insecure on a TLS error. Returns the final insecure flag used.
func validateCredentials(ctx context.Context, p prompter, baseURL, tokenID, secret string, knownInsecure bool) (bool, error) {
	insecure, err := setup.VerifyToken(ctx, baseURL, tokenID, secret, knownInsecure)
	if err != nil {
		return false, err
	}
	if insecure && !knownInsecure {
		warnTLSFallback(p, baseURL)
	}
	return insecure, nil
}
