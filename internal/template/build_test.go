package template

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

type fakePVE struct {
	t           *testing.T
	srv         *httptest.Server
	client      *pveclient.Client
	catalogue   *httptest.Server
	mu          sync.Mutex
	hits        []string
	statusHits  int32
	alwaysRun   bool
	failDownload bool
	failConvert bool
	pveVersion  string
}

func init() {
	// Speed up waitStopped polling in tests.
	pollInterval = 50 * time.Millisecond
}

func newFakePVE(t *testing.T) *fakePVE {
	t.Helper()
	data, err := os.ReadFile("testdata/simplestreams.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	f := &fakePVE{t: t, pveVersion: "8.2.4"}
	f.catalogue = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	f.client = pveclient.New(f.srv.URL, "tok@pam!x", "secret", false)
	f.client.HTTPClient = f.srv.Client()
	t.Cleanup(func() { f.srv.Close(); f.catalogue.Close() })
	return f
}

func (f *fakePVE) record(method, path string) {
	f.mu.Lock()
	f.hits = append(f.hits, method+" "+path)
	f.mu.Unlock()
}

func (f *fakePVE) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.hits))
	copy(out, f.hits)
	return out
}

func (f *fakePVE) serve(w http.ResponseWriter, r *http.Request) {
	_, _ = io.ReadAll(r.Body)
	p := r.URL.Path
	f.record(r.Method, p)
	switch {
	case r.Method == "GET" && p == "/version":
		fmt.Fprintf(w, `{"data":{"version":"%s","release":"","repoid":""}}`, f.pveVersion)
	case r.Method == "GET" && p == "/nodes/pve/storage":
		fmt.Fprint(w, `{"data":[{"storage":"local","type":"dir","content":"iso,vztmpl,images","active":1,"enabled":1}]}`)
	case r.Method == "GET" && strings.HasPrefix(p, "/storage/"):
		fmt.Fprint(w, `{"data":{"path":"/var/lib/vz"}}`)
	case r.Method == "GET" && p == "/cluster/resources":
		fmt.Fprint(w, `{"data":[]}`)
	case r.Method == "POST" && strings.HasSuffix(p, "/download-url"):
		if f.failDownload {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"errors":{"url":"fetch failed"}}`)
			return
		}
		fmt.Fprint(w, `{"data":"UPID:pve:download"}`)
	case r.Method == "GET" && strings.Contains(p, "/tasks/") && strings.HasSuffix(p, "/status"):
		if strings.Contains(p, "UPID:pve:download") && f.failDownload {
			fmt.Fprint(w, `{"data":{"status":"stopped","exitstatus":"fetch failed"}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"status":"stopped","exitstatus":"OK"}}`)
	case r.Method == "POST" && p == "/nodes/pve/qemu":
		fmt.Fprint(w, `{"data":"UPID:pve:create"}`)
	case r.Method == "POST" && strings.HasSuffix(p, "/status/start"):
		fmt.Fprint(w, `{"data":"UPID:pve:start"}`)
	case r.Method == "GET" && strings.HasSuffix(p, "/status/current"):
		n := atomic.AddInt32(&f.statusHits, 1)
		if f.alwaysRun || n < 3 {
			fmt.Fprint(w, `{"data":{"status":"running","vmid":9000}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"status":"stopped","vmid":9000}}`)
	case r.Method == "POST" && strings.HasSuffix(p, "/config"):
		fmt.Fprint(w, `{"data":null}`)
	case r.Method == "POST" && strings.HasSuffix(p, "/template"):
		if f.failConvert {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"errors":{"vmid":"convert failed"}}`)
			return
		}
		fmt.Fprint(w, `{"data":null}`)
	default:
		http.NotFound(w, r)
	}
}

func baseOpts(f *fakePVE) Options {
	return Options{
		Client:              f.client,
		Node:                "pve",
		Bridge:              "vmbr0",
		Wait:                5 * time.Second,
		CatalogueURL:        f.catalogue.URL,
		PickImage:           func([]ImageEntry) int { return 0 },
		PickTargetStorage:   func([]pveclient.Storage) int { return 0 },
		PickSnippetsStorage: func([]pveclient.Storage) int { return 0 },
		UploadSnippet: func(ctx context.Context, storagePath, filename string, content []byte) error {
			return nil
		},
	}
}

func TestRun_HappyPath(t *testing.T) {
	f := newFakePVE(t)
	r, err := Run(context.Background(), baseOpts(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.VMID != 9000 {
		t.Errorf("VMID = %d, want 9000", r.VMID)
	}
	// Endpoint sequence: version, target-storage list, snippets-storage list,
	// then GetStoragePath for the snippets pool.
	expectedPrefix := []string{
		"GET /version",
		"GET /nodes/pve/storage",
		"GET /nodes/pve/storage",
		"GET /storage/local",
	}
	got := f.paths()
	for i, want := range expectedPrefix {
		if i >= len(got) || got[i] != want {
			t.Errorf("hit[%d] = %q, want %q", i, got[i], want)
		}
	}
	// Must include these in order later on.
	want := []string{
		"GET /cluster/resources",
		"POST /nodes/pve/storage/local/download-url",
		"POST /nodes/pve/qemu",
		"POST /nodes/pve/qemu/9000/status/start",
		"POST /nodes/pve/qemu/9000/config",
		"POST /nodes/pve/qemu/9000/template",
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %q in %v", w, got)
		}
	}
}

func TestRun_UploadReceivesResolvedStoragePath(t *testing.T) {
	f := newFakePVE(t)
	var gotPath, gotFile string
	var gotContent []byte
	opts := baseOpts(f)
	opts.UploadSnippet = func(ctx context.Context, storagePath, filename string, content []byte) error {
		gotPath = storagePath
		gotFile = filename
		gotContent = content
		return nil
	}
	_, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotPath != "/var/lib/vz" {
		t.Errorf("storagePath = %q, want /var/lib/vz", gotPath)
	}
	if gotFile != bakeSnippetFilename {
		t.Errorf("filename = %q", gotFile)
	}
	if len(gotContent) == 0 {
		t.Error("content empty")
	}
}

func TestRun_PVE7xRejected(t *testing.T) {
	f := newFakePVE(t)
	f.pveVersion = "7.4"
	_, err := Run(context.Background(), baseOpts(f))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "PVE 8.0 or later required") {
		t.Errorf("err = %v", err)
	}
}

func TestRun_DownloadFailurePre_VMCreated(t *testing.T) {
	f := newFakePVE(t)
	f.failDownload = true
	_, err := Run(context.Background(), baseOpts(f))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, p := range f.paths() {
		if p == "POST /nodes/pve/qemu" {
			t.Errorf("CreateVM called despite download failure")
		}
	}
}

func TestRun_WaitStoppedTimeout(t *testing.T) {
	f := newFakePVE(t)
	f.alwaysRun = true
	opts := baseOpts(f)
	opts.Wait = 2 * time.Second
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, pveclient.ErrTimeout) {
		t.Errorf("err = %v, want ErrTimeout", err)
	}
	if !strings.Contains(err.Error(), "9000") {
		t.Errorf("err = %v, want vmid mention", err)
	}
}

func TestRun_ConvertFailureLeavesVM(t *testing.T) {
	f := newFakePVE(t)
	f.failConvert = true
	_, err := Run(context.Background(), baseOpts(f))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "9000") {
		t.Errorf("err = %v, want vmid mention", err)
	}
	if !strings.Contains(err.Error(), "pmox delete") {
		t.Errorf("err = %v, want cleanup hint", err)
	}
	for _, p := range f.paths() {
		if strings.HasPrefix(p, "DELETE ") {
			t.Errorf("unexpected DELETE: %q", p)
		}
	}
}
