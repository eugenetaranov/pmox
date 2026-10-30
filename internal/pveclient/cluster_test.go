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

func TestQEMUResources_DropsContainers(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[
		  {"vmid":104,"name":"payzoff","type":"qemu"},
		  {"vmid":110,"name":"pmox-router","type":"lxc"},
		  {"vmid":9000,"name":"tmpl"}
		]}`))
	})
	got, err := c.QEMUResources(context.Background())
	if err != nil {
		t.Fatalf("QEMUResources: %v", err)
	}
	var ids []int
	for _, r := range got {
		ids = append(ids, r.VMID)
	}
	if len(ids) != 2 || ids[0] != 104 || ids[1] != 9000 {
		t.Errorf("vmids = %v, want [104 9000] (a missing type counts as qemu)", ids)
	}
}
