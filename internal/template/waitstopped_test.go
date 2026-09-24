package template

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// statusServer answers GET status/current with the (code, body) the
// handler returns for the n-th hit (1-based).
func statusServer(t *testing.T, handler func(hit int) (int, string)) (*pveclient.Client, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		code, body := handler(int(atomic.AddInt32(&hits, 1)))
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	c := pveclient.New(srv.URL, "tok@pam!x", "secret", false)
	c.HTTPClient = srv.Client()
	return c, &hits
}

func TestWaitStopped_ToleratesTransientErrors(t *testing.T) {
	c, _ := statusServer(t, func(hit int) (int, string) {
		switch {
		case hit <= 2:
			return 502, `{"data":null}`
		case hit == 3:
			return 200, `{"data":{"status":"running"}}`
		}
		return 200, `{"data":{"status":"stopped"}}`
	})
	if err := waitStopped(context.Background(), c, "pve", 9000, 5*time.Second); err != nil {
		t.Fatalf("waitStopped err = %v, want nil after transient 502s", err)
	}
}

func TestWaitStopped_FatalErrorFailsFast(t *testing.T) {
	c, hits := statusServer(t, func(int) (int, string) { return 401, `{"data":null}` })
	err := waitStopped(context.Background(), c, "pve", 9000, 5*time.Second)
	if !errors.Is(err, pveclient.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Errorf("polls = %d, want 1 before abort", n)
	}
}

func TestWaitStopped_TimeoutReportsLastTransient(t *testing.T) {
	c, _ := statusServer(t, func(int) (int, string) { return 502, `{"data":null}` })
	err := waitStopped(context.Background(), c, "pve", 9000, 150*time.Millisecond)
	if !errors.Is(err, pveclient.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if !errors.Is(err, pveclient.ErrAPIError) {
		t.Errorf("err = %v, want last transient ErrAPIError wrapped", err)
	}
}
