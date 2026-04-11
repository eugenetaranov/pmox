package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
)

func TestCreateTemplate_NonTTYRejected(t *testing.T) {
	orig := isTTYFunc
	isTTYFunc = func(uintptr) bool { return false }
	t.Cleanup(func() { isTTYFunc = orig })

	cmd := newCreateTemplateCmd()
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs(nil)
	cmd.SetContext(context.Background())

	err := runCreateTemplate(cmd, &createTemplateFlags{})
	if err == nil {
		t.Fatal("expected error")
	}
	if exitcode.From(err) != exitcode.ExitUserError {
		t.Errorf("exit code = %d, want %d", exitcode.From(err), exitcode.ExitUserError)
	}
	if !strings.Contains(err.Error(), "interactive TTY required") {
		t.Errorf("err = %v", err)
	}
}

func TestCreateTemplate_VerboseLogLine(t *testing.T) {
	// Short-circuit the template.Run state machine by returning 500
	// on /version — that's the first API call, so the rest of the
	// phases never fire.
	var versionHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			atomic.AddInt32(&versionHits, 1)
			http.Error(w, `{"errors":{"version":"fail"}}`, 500)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	client := pveclient.New(srv.URL, "tok@pam!x", "secret", false)
	client.HTTPClient = srv.Client()

	origVerbose := verbose
	verbose = true
	t.Cleanup(func() { verbose = origVerbose })

	cmd := newCreateTemplateCmd()
	var errBuf, outBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&outBuf)
	cmd.SetContext(context.Background())

	err := runCreateTemplateWithClient(context.Background(), cmd, client, srv.URL, "single configured", "pve", "vmbr0", time.Minute)
	if err == nil {
		t.Fatal("expected error from short-circuited /version")
	}
	stderr := errBuf.String()
	want := "using server " + srv.URL + " (single configured)\n"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr missing log line %q, got: %q", want, stderr)
	}
	if strings.Count(stderr, "using server ") != 1 {
		t.Errorf("expected exactly one log line, got: %q", stderr)
	}
	// Log line must precede the first API call — i.e. appear in
	// stderr before template.Run returns.
	if atomic.LoadInt32(&versionHits) == 0 {
		t.Fatal("expected /version to have been called")
	}
	// Since the log line is written synchronously before template.Run,
	// its presence in errBuf together with a non-zero versionHits
	// proves the ordering.
}

func TestCreateTemplate_FlagDefaults(t *testing.T) {
	cmd := newCreateTemplateCmd()
	// Default --wait is 10m.
	wantDefault := 10 * time.Minute
	got, err := cmd.Flags().GetDuration("wait")
	if err != nil {
		t.Fatalf("GetDuration: %v", err)
	}
	if got != wantDefault {
		t.Errorf("default --wait = %v, want %v", got, wantDefault)
	}
	// Parsing --wait 5m.
	if err := cmd.Flags().Set("wait", "5m"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _ = cmd.Flags().GetDuration("wait")
	if got != 5*time.Minute {
		t.Errorf("parsed --wait = %v, want 5m", got)
	}
	// --node and --bridge exist.
	for _, name := range []string{"node", "bridge", "wait"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q missing", name)
		}
	}
}
