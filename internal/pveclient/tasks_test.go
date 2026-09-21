package pveclient

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// A transient 5xx during polling must NOT abort the wait: the PVE task
// keeps running server-side, so WaitTask should keep polling and succeed
// once the task reports OK.
func TestWaitTask_TransientErrorThenOK(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			// One gateway blip.
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK"}}`))
	})
	if err := c.WaitTask(context.Background(), "pve1", "UPID:x:", 5*time.Second); err != nil {
		t.Fatalf("WaitTask should have retried past the transient error, got: %v", err)
	}
	if atomic.LoadInt32(&hits) < 2 {
		t.Errorf("expected at least 2 polls (retry after transient), got %d", hits)
	}
}

// A fatal error (401) can never resolve by waiting, so WaitTask must
// abort immediately rather than poll until the deadline.
func TestWaitTask_FatalErrorAbortsImmediately(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	start := time.Now()
	err := c.WaitTask(context.Background(), "pve1", "UPID:x:", 5*time.Second)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("fatal error should abort immediately, took %v", elapsed)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("expected exactly 1 poll before abort, got %d", n)
	}
}

func TestIsFatalPollError(t *testing.T) {
	fatal := []error{ErrUnauthorized, ErrTLSVerificationFailed, ErrNotFound}
	for _, e := range fatal {
		if !IsFatalPollError(e) {
			t.Errorf("%v should be fatal", e)
		}
	}
	transient := []error{ErrNetwork, ErrAPIError, ErrTimeout}
	for _, e := range transient {
		if IsFatalPollError(e) {
			t.Errorf("%v should be transient", e)
		}
	}
}
