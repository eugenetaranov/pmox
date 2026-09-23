package pveclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// ReachStatus classifies the result of an unauthenticated reachability
// probe against a PVE API base URL.
type ReachStatus int

const (
	// ReachUnknown is the zero value (probe not run or request build failed).
	ReachUnknown ReachStatus = iota
	// Reachable means a live PVE API answered (HTTP 200 version envelope, or
	// 401 which proves the endpoint is a PVE API requiring auth).
	Reachable
	// ReachTLSUntrusted means the host answered but its TLS cert failed
	// verification — a trust decision, not a connectivity failure.
	ReachTLSUntrusted
	// ReachUnreachable means no usable response: connection refused,
	// timeout, no route, or DNS failure.
	ReachUnreachable
	// ReachNotPVE means the host answered but the response is not a PVE API.
	ReachNotPVE
)

// probeTimeout bounds a single reachability probe.
const probeTimeout = 5 * time.Second

// Probe performs an unauthenticated GET on baseURL's /version endpoint and
// classifies the outcome. It never sends credentials, so it is safe to run
// before the user has provided an API token. The returned error is the
// underlying transport/HTTP error (nil on Reachable) for logging; callers
// switch on the ReachStatus.
func Probe(ctx context.Context, baseURL string, insecure bool) (ReachStatus, error) {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // homelab fallback per D4
		},
	}
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, baseURL+"/version", nil)
	if err != nil {
		return ReachUnknown, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		if isTLSError(err) {
			return ReachTLSUntrusted, err
		}
		return ReachUnreachable, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		// 401 from /api2/json/version proves a live PVE API that requires auth.
		return Reachable, nil
	case http.StatusOK:
		if looksLikePVEVersion(body) {
			return Reachable, nil
		}
		return ReachNotPVE, nil
	default:
		// Any other status (404, HTML error page, 5xx) is not the PVE API.
		return ReachNotPVE, nil
	}
}

// looksLikePVEVersion reports whether body is a PVE version envelope
// ({"data":{"version":"..."}}). Used only for the rare 200-without-auth
// case (e.g. a permissive reverse proxy).
func looksLikePVEVersion(body []byte) bool {
	var v versionResponse
	if err := json.Unmarshal(body, &v); err != nil {
		return false
	}
	return v.Data.Version != ""
}
