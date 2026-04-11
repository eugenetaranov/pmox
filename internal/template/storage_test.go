package template

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

type storageFake struct {
	t               *testing.T
	pools           []pveclient.Storage
	updateContentOK bool
	lastPut         atomic.Value // string body
}

func (f *storageFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/storage") && !strings.HasPrefix(r.URL.Path, "/storage/") {
			// /nodes/<node>/storage
			var items []string
			for _, s := range f.pools {
				items = append(items, `{"storage":"`+s.Storage+`","type":"`+s.Type+`","content":"`+s.Content+`","active":1,"enabled":1}`)
			}
			_, _ = w.Write([]byte(`{"data":[` + strings.Join(items, ",") + `]}`))
			return
		}
		if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/storage/") {
			b, _ := io.ReadAll(r.Body)
			f.lastPut.Store(string(b))
			if !f.updateContentOK {
				w.WriteHeader(500)
				return
			}
			_, _ = w.Write([]byte(`{"data":null}`))
			return
		}
		http.NotFound(w, r)
	}
}

func newStorageFake(t *testing.T, pools []pveclient.Storage) (*storageFake, *pveclient.Client) {
	t.Helper()
	f := &storageFake{t: t, pools: pools, updateContentOK: true}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := pveclient.New(srv.URL, "tok@pam!x", "secret", false)
	c.HTTPClient = srv.Client()
	return f, c
}

func TestEnsureSnippetsStorage_AlreadyEnabled(t *testing.T) {
	var called int
	confirm := func(string) bool { called++; return true }
	_, c := newStorageFake(t, []pveclient.Storage{
		{Storage: "local", Type: "dir", Content: "iso,vztmpl,snippets", Active: 1, Enabled: 1},
		{Storage: "zfs1", Type: "zfspool", Content: "images", Active: 1, Enabled: 1},
	})
	name, err := ensureSnippetsStorage(context.Background(), c, "pve", confirm)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if name != "local" {
		t.Errorf("name = %q", name)
	}
	if called != 0 {
		t.Errorf("confirm called %d times, want 0", called)
	}
}

func TestEnsureSnippetsStorage_PromptAccept(t *testing.T) {
	confirm := func(string) bool { return true }
	f, c := newStorageFake(t, []pveclient.Storage{
		{Storage: "local", Type: "dir", Content: "iso,vztmpl,backup", Active: 1, Enabled: 1},
	})
	name, err := ensureSnippetsStorage(context.Background(), c, "pve", confirm)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if name != "local" {
		t.Errorf("name = %q", name)
	}
	body, _ := f.lastPut.Load().(string)
	form, _ := url.ParseQuery(body)
	got := form.Get("content")
	if !strings.Contains(got, "snippets") || !strings.Contains(got, "iso") || !strings.Contains(got, "backup") {
		t.Errorf("content = %q, want iso,vztmpl,backup,snippets", got)
	}
}

func TestEnsureSnippetsStorage_PromptReject(t *testing.T) {
	confirm := func(string) bool { return false }
	_, c := newStorageFake(t, []pveclient.Storage{
		{Storage: "local", Type: "dir", Content: "iso", Active: 1, Enabled: 1},
	})
	_, err := ensureSnippetsStorage(context.Background(), c, "pve", confirm)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "snippets storage required") {
		t.Errorf("err = %v", err)
	}
}

func TestEnsureSnippetsStorage_NoDirCapable(t *testing.T) {
	var called int
	confirm := func(string) bool { called++; return true }
	_, c := newStorageFake(t, []pveclient.Storage{
		{Storage: "local-lvm", Type: "lvmthin", Content: "images", Active: 1, Enabled: 1},
		{Storage: "zfs1", Type: "zfspool", Content: "images", Active: 1, Enabled: 1},
	})
	_, err := ensureSnippetsStorage(context.Background(), c, "pve", confirm)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "create a directory-type storage") {
		t.Errorf("err = %v", err)
	}
	if called != 0 {
		t.Errorf("confirm called %d times", called)
	}
}
