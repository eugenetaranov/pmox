package pvessh

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

var errSentinel = errors.New("sentinel")

func TestScrub_RedactsAndPreservesChain(t *testing.T) {
	cfg := Config{Password: "hunter2"}
	orig := fmt.Errorf("auth failed for password hunter2: %w", errSentinel)

	got := scrub(orig, cfg)
	if strings.Contains(got.Error(), "hunter2") {
		t.Errorf("password leaked: %q", got.Error())
	}
	if !strings.Contains(got.Error(), "[REDACTED]") {
		t.Errorf("missing redaction marker: %q", got.Error())
	}
	if !errors.Is(got, errSentinel) {
		t.Errorf("errors.Is(scrubbed, sentinel) = false; chain dropped")
	}
	if wrapped := fmt.Errorf("ssh dial: %w", got); !errors.Is(wrapped, errSentinel) || strings.Contains(wrapped.Error(), "hunter2") {
		t.Errorf("re-wrapped scrubbed error: %q (Is=%v)", wrapped.Error(), errors.Is(wrapped, errSentinel))
	}
}

func TestScrub_Passthrough(t *testing.T) {
	orig := fmt.Errorf("connection refused: %w", errSentinel)
	if got := scrub(orig, Config{Password: "hunter2"}); got != orig { //nolint:errorlint // identity check: unchanged error must be returned as-is
		t.Errorf("error without password should be returned unchanged, got %v", got)
	}
	if got := scrub(orig, Config{}); got != orig { //nolint:errorlint // identity check: unchanged error must be returned as-is
		t.Errorf("no password configured should return err unchanged, got %v", got)
	}
	if scrub(nil, Config{Password: "x"}) != nil {
		t.Error("scrub(nil) != nil")
	}
}
