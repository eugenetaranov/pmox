package template

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

func newListStorageFake(t *testing.T, pools []pveclient.Storage) *pveclient.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/storage") {
			var items []string
			for _, s := range pools {
				items = append(items, `{"storage":"`+s.Storage+`","type":"`+s.Type+`","content":"`+s.Content+`","active":1,"enabled":1}`)
			}
			_, _ = w.Write([]byte(`{"data":[` + strings.Join(items, ",") + `]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	c := pveclient.New(srv.URL, "tok@pam!x", "secret", false)
	c.HTTPClient = srv.Client()
	return c
}

func TestPickSnippetsStorage_FiltersDirCapable(t *testing.T) {
	c := newListStorageFake(t, []pveclient.Storage{
		{Storage: "local-lvm", Type: "lvmthin", Content: "images"},
		{Storage: "local", Type: "dir", Content: "iso,vztmpl"},
		{Storage: "nfs1", Type: "nfs", Content: "iso"},
	})
	var offered []pveclient.Storage
	opts := Options{
		Client: c,
		Node:   "pve",
		PickSnippetsStorage: func(pools []pveclient.Storage) int {
			offered = pools
			return 0
		},
	}
	got, err := pickSnippetsStorage(context.Background(), opts)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(offered) != 2 {
		t.Fatalf("offered %d pools, want 2: %+v", len(offered), offered)
	}
	if offered[0].Storage != "local" || offered[1].Storage != "nfs1" {
		t.Errorf("filter order wrong: %+v", offered)
	}
	if got != "local" {
		t.Errorf("got %q, want local", got)
	}
}

func TestPickSnippetsStorage_NoDirCapable(t *testing.T) {
	c := newListStorageFake(t, []pveclient.Storage{
		{Storage: "local-lvm", Type: "lvmthin", Content: "images"},
		{Storage: "zfs1", Type: "zfspool", Content: "images"},
	})
	opts := Options{
		Client:              c,
		Node:                "pve",
		PickSnippetsStorage: func([]pveclient.Storage) int { return 0 },
	}
	_, err := pickSnippetsStorage(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no dir-capable storage") {
		t.Errorf("err = %v", err)
	}
}
