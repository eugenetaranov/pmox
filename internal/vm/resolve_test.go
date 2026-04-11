package vm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

func TestHasPMOXTag(t *testing.T) {
	cases := map[string]bool{
		"":           false,
		"pmox":       true,
		"foo;pmox":   true,
		"pmox;bar":   true,
		"foo,pmox":   true,
		"pmox,bar":   true,
		"PMOX":       true,
		" pmox ":     true,
		"notpmox":    false,
		"pmoxish":    false,
		"foo;bar":    false,
		"foo,bar":    false,
		"prod;pmox;": true,
	}
	for in, want := range cases {
		if got := HasPMOXTag(in); got != want {
			t.Errorf("HasPMOXTag(%q) = %v, want %v", in, got, want)
		}
	}
}

// clusterServer spins up a test server that returns a canned
// cluster-resources payload on /api2/json/cluster/resources. The
// body is controlled per test.
func clusterServer(t *testing.T, body string) *pveclient.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/cluster/resources") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &pveclient.Client{
		BaseURL:    srv.URL,
		TokenID:    "t",
		Secret:     "s",
		HTTPClient: srv.Client(),
	}
}

const twoVMsFixture = `{"data":[
  {"vmid":100,"name":"web1","node":"pve1","status":"running","tags":"pmox"},
  {"vmid":200,"name":"db","node":"pve2","status":"stopped","tags":""}
]}`

const dupeNameFixture = `{"data":[
  {"vmid":107,"name":"web1","node":"pve2","status":"stopped","tags":"pmox"},
  {"vmid":104,"name":"web1","node":"pve1","status":"running","tags":"pmox"}
]}`

func TestResolve_NumericSingleMatch(t *testing.T) {
	c := clusterServer(t, twoVMsFixture)
	ref, err := Resolve(context.Background(), c, "200")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ref.VMID != 200 || ref.Name != "db" || ref.Node != "pve2" {
		t.Errorf("ref = %+v", ref)
	}
}

func TestResolve_NameSingleMatch(t *testing.T) {
	c := clusterServer(t, twoVMsFixture)
	ref, err := Resolve(context.Background(), c, "web1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ref.VMID != 100 || ref.Node != "pve1" || ref.Tags != "pmox" {
		t.Errorf("ref = %+v", ref)
	}
}

func TestResolve_NameAmbiguous(t *testing.T) {
	c := clusterServer(t, dupeNameFixture)
	_, err := Resolve(context.Background(), c, "web1")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `multiple VMs named "web1"`) {
		t.Errorf("missing prefix: %v", err)
	}
	// VMIDs must be listed in ascending order: 104, 107.
	if !strings.Contains(msg, "[104 107]") {
		t.Errorf("vmids not sorted: %v", err)
	}
}

func TestResolve_NameNotFound(t *testing.T) {
	c := clusterServer(t, twoVMsFixture)
	_, err := Resolve(context.Background(), c, "ghost")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `VM "ghost" not found`) {
		t.Errorf("err = %v", err)
	}
}

func TestResolve_VMIDNotFound(t *testing.T) {
	c := clusterServer(t, twoVMsFixture)
	_, err := Resolve(context.Background(), c, "999")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "VM 999 not found") {
		t.Errorf("err = %v", err)
	}
}
