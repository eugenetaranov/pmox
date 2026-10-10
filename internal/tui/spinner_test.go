package tui

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a bytes.Buffer safe for the spinner goroutine and the test.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func withDelay(t *testing.T, d time.Duration) {
	t.Helper()
	orig := SpinnerDelay
	SpinnerDelay = d
	t.Cleanup(func() { SpinnerDelay = orig })
}

func TestSpinnerFastOperationWritesNothing(t *testing.T) {
	withDelay(t, 200*time.Millisecond)
	var w syncBuf
	s := NewSpinner(&w, "Loading VMs…")
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	s.Stop() // idempotent
	if got := w.String(); got != "" {
		t.Fatalf("fast operation wrote %q, want nothing", got)
	}
}

func TestSpinnerSlowOperationDrawsAndClears(t *testing.T) {
	withDelay(t, 10*time.Millisecond)
	var w syncBuf
	s := NewSpinner(&w, "Starting web1…")
	time.Sleep(60 * time.Millisecond)
	s.Set("Waiting for web1 to get an IP…")
	time.Sleep(200 * time.Millisecond)
	s.Stop()
	got := w.String()
	if !strings.Contains(got, "Starting web1…") || !strings.Contains(got, "Waiting for web1 to get an IP…") {
		t.Fatalf("labels not drawn: %q", got)
	}
	if !strings.HasSuffix(got, "\r\033[K") {
		t.Fatalf("line not cleared at the end: %q", got)
	}
}

func TestSpinnerSucceedLeavesResult(t *testing.T) {
	withDelay(t, time.Hour)
	var w syncBuf
	NewSpinner(&w, "Starting web1…").Succeed("web1 started")
	if got := w.String(); got != "✓ web1 started\n" {
		t.Fatalf("got %q", got)
	}
}

func TestNilSpinnerIsNoop(t *testing.T) {
	var s *Spinner
	s.Set("x")
	s.Stop()
	s.Succeed("x")
}

func TestStartSpinnerNestedReturnsNil(t *testing.T) {
	orig := StderrIsTerminal
	StderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { StderrIsTerminal = orig })
	withDelay(t, time.Hour)
	outer := StartSpinner("outer")
	if outer == nil {
		t.Fatal("outer spinner not started")
	}
	if inner := StartSpinner("inner"); inner != nil {
		inner.Stop()
		t.Fatal("nested spinner started while another runs")
	}
	outer.Stop()
	again := StartSpinner("after")
	if again == nil {
		t.Fatal("spinner refused after the outer one stopped")
	}
	again.Stop()
}
