package pvessh

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// HostFromURL derives the node SSH address (hostname:22) from a PVE API
// URL such as https://pve.lan:8006/api2/json.
func HostFromURL(apiURL string) (string, error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("parse server url: %w", err)
	}
	h := u.Hostname()
	if h == "" {
		return "", fmt.Errorf("server url has no host: %s", apiURL)
	}
	return h + ":22", nil
}

// KnownHostsHas reports whether the known_hosts file at path has an entry
// whose host list names host, with or without its default :22 suffix. A
// missing file has no entries. Hashed (|1|...) entries never match.
func KnownHostsHas(path, host string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	bare := strings.TrimSuffix(host, ":22")
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// known_hosts lines start with "host[,host2] type base64".
		fields := strings.Fields(line)
		for _, n := range strings.Split(fields[0], ",") {
			if n == host || n == bare {
				return true, nil
			}
		}
	}
	return false, nil
}

// KnownHostToken returns the host field of a known_hosts line: the first
// entry of a comma-separated host list, with any [host]:port bracketing
// and a trailing numeric :port stripped. A bare (unbracketed) IPv6
// literal is not distinguished from host:port and loses its last group.
// Blank lines yield "". Hashed (|1|...) entries are returned verbatim.
func KnownHostToken(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	h := fields[0]
	// Take the first host if it's a comma-list, and strip [..]:port form.
	if i := strings.IndexByte(h, ','); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimPrefix(h, "[")
	h = strings.ReplaceAll(h, "]", "")
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i+1:], ":") {
		// strip a trailing :port (but not part of an IPv6 literal)
		if _, err := strconv.Atoi(h[i+1:]); err == nil {
			h = h[:i]
		}
	}
	return h
}

// KnownHostsPrune rewrites the known_hosts file at path, dropping every
// entry whose KnownHostToken is rejected by keep. Comment lines are kept,
// blank lines are dropped, and the file is rewritten with mode 0600. It
// returns the number of entries removed; a missing file is an error.
func KnownHostsPrune(path string, keep func(host string) bool) (removed int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			kept = append(kept, line)
			continue
		}
		if host := KnownHostToken(trimmed); host != "" && !keep(host) {
			removed++
			continue // stale — drop
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	if out != "" {
		out += "\n"
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return 0, err
	}
	return removed, nil
}
