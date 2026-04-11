package pveclient

import (
	"context"
	"net/http"
	"os"
	"testing"
)

func TestClusterResources_ParsesFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/cluster_resources.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var gotPath, gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write(data)
	})
	got, err := c.ClusterResources(context.Background(), "vm")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	if gotPath != "/cluster/resources" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "type=vm" {
		t.Errorf("query = %q, want type=vm", gotQuery)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Tagged VM parses all fields.
	if got[0].VMID != 100 || got[0].Name != "web1" || got[0].Node != "pve1" || got[0].Status != "running" || got[0].Tags != "pmox" {
		t.Errorf("row 0 = %+v", got[0])
	}
	// Untagged VM: missing tags field parses as empty string.
	if got[1].VMID != 200 || got[1].Tags != "" {
		t.Errorf("row 1 tags = %q, want empty", got[1].Tags)
	}
}

func TestClusterResources_NoFilter(t *testing.T) {
	var gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	if _, err := c.ClusterResources(context.Background(), ""); err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want empty", gotQuery)
	}
}
