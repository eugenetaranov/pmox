package vm

import (
	"context"
	"errors"
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

// oneTaggedOneUntaggedFixture has two VMs sharing a name: one carries
// the pmox tag (and so is what `pmox list`'s default view shows), the
// other doesn't.
const oneTaggedOneUntaggedFixture = `{"data":[
  {"vmid":105,"name":"alice","node":"p0","status":"stopped","tags":"pmox"},
  {"vmid":107,"name":"alice","node":"p0","status":"stopped","tags":""}
]}`

const twoUntaggedSameNameFixture = `{"data":[
  {"vmid":105,"name":"alice","node":"p0","status":"stopped","tags":""},
  {"vmid":107,"name":"alice","node":"p0","status":"stopped","tags":""}
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
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("err = %v, want errors.Is ErrAmbiguous", err)
	}
}

// TestResolve_NamePrefersTaggedOverUntagged guards against the
// inconsistency `pmox list` vs. name resolution used to have: `pmox
// list`'s default view only shows pmox-tagged VMs, so an untagged VM
// sharing a name with a tagged one was invisible there but still made
// Resolve report the name as ambiguous. Resolve must prefer the tagged
// VM instead, matching what the user can actually see.
func TestResolve_NamePrefersTaggedOverUntagged(t *testing.T) {
	c := clusterServer(t, oneTaggedOneUntaggedFixture)
	ref, err := Resolve(context.Background(), c, "alice")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ref.VMID != 105 || !HasPMOXTag(ref.Tags) {
		t.Errorf("ref = %+v, want the tagged VM 105", ref)
	}
}

// TestResolve_NameAmbiguousAmongUntagged checks that a name shared by
// two untagged VMs still reports ambiguity (there is no tagged VM to
// prefer), and that the untagged VMID 107 is reachable by passing it
// directly.
func TestResolve_NameAmbiguousAmongUntagged(t *testing.T) {
	c := clusterServer(t, twoUntaggedSameNameFixture)
	_, err := Resolve(context.Background(), c, "alice")
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("err = %v, want errors.Is ErrAmbiguous", err)
	}
	ref, err := Resolve(context.Background(), c, "107")
	if err != nil || ref.VMID != 107 {
		t.Errorf("Resolve(107) = %+v, %v", ref, err)
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
	if !errors.Is(err, pveclient.ErrNotFound) {
		t.Errorf("err = %v, want errors.Is pveclient.ErrNotFound", err)
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
	if !errors.Is(err, pveclient.ErrNotFound) {
		t.Errorf("err = %v, want errors.Is pveclient.ErrNotFound", err)
	}
}

func TestRefRequirePMOXTag(t *testing.T) {
	tagged := &Ref{VMID: 1, Name: "a", Tags: "pmox"}
	untagged := &Ref{VMID: 2, Name: "b", Tags: "prod"}
	if err := tagged.RequirePMOXTag("delete", false); err != nil {
		t.Errorf("tagged: %v", err)
	}
	if err := untagged.RequirePMOXTag("delete", true); err != nil {
		t.Errorf("untagged+force: %v", err)
	}
	err := untagged.RequirePMOXTag("delete", false)
	if err == nil || !strings.Contains(err.Error(), `refusing to delete VM "b" (vmid 2)`) {
		t.Errorf("untagged: err = %v", err)
	}
}
