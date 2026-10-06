package pveclient

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func agentServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL, "t@pam!x", "s", false)
}

func TestAgentFileRead(t *testing.T) {
	for _, tc := range []struct {
		truncated string
		want      bool
	}{{`0`, false}, {`1`, true}, {`false`, false}, {`true`, true}, {`null`, false}} {
		c := agentServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/nodes/pve/qemu/101/agent/file-read" || r.URL.Query().Get("file") != "/etc/passwd" {
				t.Errorf("got %s?%s", r.URL.Path, r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":{"content":"root:x:0:0::/root:/bin/bash\n","bytes-read":28,"truncated":` + tc.truncated + `}}`))
		})
		got, truncated, err := c.AgentFileRead(context.Background(), "pve", 101, "/etc/passwd")
		if err != nil || string(got) != "root:x:0:0::/root:/bin/bash\n" || truncated != tc.want {
			t.Errorf("truncated=%s: got %q %v %v", tc.truncated, got, truncated, err)
		}
	}
}

func TestAgentFileWriteSendsPlainContent(t *testing.T) {
	c := agentServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Method != http.MethodPost || r.URL.Path != "/nodes/pve/qemu/101/agent/file-write" ||
			r.PostForm.Get("file") != "/home/ubuntu/.ssh/authorized_keys" || r.PostForm.Get("encode") != "0" {
			t.Errorf("got %s %s %v", r.Method, r.URL.Path, r.PostForm)
		}
		if got, err := base64.StdEncoding.DecodeString(r.PostForm.Get("content")); err != nil || string(got) != "ssh-ed25519 AAAA x jürgen@ws1\n" {
			t.Errorf("got %s %s %v", r.Method, r.URL.Path, r.PostForm)
		}
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	// Non-ASCII content (a UTF-8 key comment) must survive: it is
	// base64-encoded client-side and sent with encode=0.
	if err := c.AgentFileWrite(context.Background(), "pve", 101, "/home/ubuntu/.ssh/authorized_keys", []byte("ssh-ed25519 AAAA x jürgen@ws1\n")); err != nil {
		t.Fatal(err)
	}
}

func TestAgentErrorsClassified(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   error
	}{
		{500, `{"message":"VM 101 is not running\n"}`, ErrVMNotRunning},
		{500, `{"message":"QEMU guest agent is not running\n"}`, ErrAgentNotRunning},
		{403, `{"message":"Permission check failed (/vms/101, VM.GuestAgent.FileRead)\n"}`, ErrForbidden},
	} {
		c := agentServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		_, _, err := c.AgentFileRead(context.Background(), "pve", 101, "/x")
		if !errors.Is(err, tc.want) {
			t.Errorf("%d %s: err = %v, want %v", tc.status, tc.body, err, tc.want)
		}
		if errors.Is(tc.want, ErrForbidden) && (errors.Is(err, ErrAgentNotRunning) || errors.Is(err, ErrVMNotRunning)) {
			t.Errorf("403 misclassified: %v", err)
		}
	}
}
