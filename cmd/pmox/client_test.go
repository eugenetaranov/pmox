package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWarnInsecureTLS(t *testing.T) {
	t.Run("secure server prints nothing", func(t *testing.T) {
		insecureTLSWarned = false
		var buf bytes.Buffer
		warnInsecureTLS(&buf, "https://host:8006/api2/json", false)
		if buf.Len() != 0 {
			t.Fatalf("expected no output for secure server, got %q", buf.String())
		}
	})

	t.Run("insecure server warns once per process", func(t *testing.T) {
		insecureTLSWarned = false
		var buf bytes.Buffer
		warnInsecureTLS(&buf, "https://host:8006/api2/json", true)
		warnInsecureTLS(&buf, "https://host:8006/api2/json", true)
		if n := strings.Count(buf.String(), "WARNING"); n != 1 {
			t.Fatalf("expected exactly one warning, got %d: %q", n, buf.String())
		}
		if !strings.Contains(buf.String(), "host:8006") {
			t.Fatalf("warning should name the server URL, got %q", buf.String())
		}
	})
}
