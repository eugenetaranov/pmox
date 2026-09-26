// Package tackroles fetches the list of community roles published in
// github.com/tackhq/tack-roles, so 'pmox apply --init' can offer a
// picker instead of scaffolding a single hardcoded example role. There
// is no API for this — the list lives only as a Markdown table in the
// repo's README — so this package fetches and parses that table.
package tackroles

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// DefaultReadmeURL is the raw README listing tack-roles' available
// roles under its "## Available roles" heading, as a
// "| `name` link | description |" Markdown table.
const DefaultReadmeURL = "https://raw.githubusercontent.com/tackhq/tack-roles/main/README.md"

// Role is one row of the "Available roles" table.
type Role struct {
	Name        string
	Description string
}

// Fetch GETs readmeURL and parses its "## Available roles" table.
// Returns an error if the fetch fails or the table can't be found —
// callers should treat this as best-effort and fall back to a static
// default rather than blocking 'pmox apply --init' on it.
func Fetch(ctx context.Context, readmeURL string) ([]Role, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readmeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", readmeURL, err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", readmeURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch %s: http %d", readmeURL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", readmeURL, err)
	}
	roles := parseTable(string(body))
	if len(roles) == 0 {
		return nil, fmt.Errorf("no roles found in %s (its table format may have changed)", readmeURL)
	}
	return roles, nil
}

// roleNameRe extracts the backtick-quoted role name from a table
// cell like "[`docker`](roles/docker)".
var roleNameRe = regexp.MustCompile("`([a-zA-Z0-9_-]+)`")

// parseTable extracts (name, description) pairs from the Markdown
// table under a "## Available roles" heading:
//
//	## Available roles
//
//	| Role | Description |
//	| --- | --- |
//	| [`docker`](roles/docker) | Installs Docker Engine ... |
//
// Best-effort: any row whose first cell has no backtick-quoted name
// (the header row, a malformed row) is skipped rather than failing
// the whole parse. Parsing stops at the section's next "##" heading
// or the end of the table, whichever comes first.
func parseTable(md string) []Role {
	const heading = "## available roles"
	var roles []Role
	inSection := false
	inTable := false
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if inSection {
				break // left the "Available roles" section
			}
			inSection = strings.HasPrefix(strings.ToLower(trimmed), heading)
			continue
		}
		if !inSection {
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			if inTable {
				break // the table ended; don't keep scanning the rest of the section
			}
			continue
		}
		cols := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cols) < 2 {
			continue
		}
		first := strings.TrimSpace(cols[0])
		if strings.Trim(first, "- ") == "" {
			inTable = true // the "| --- | --- |" separator row
			continue
		}
		m := roleNameRe.FindStringSubmatch(first)
		if m == nil {
			continue // the "| Role | Description |" header row, or malformed
		}
		inTable = true
		roles = append(roles, Role{Name: m[1], Description: strings.TrimSpace(cols[1])})
	}
	return roles
}
