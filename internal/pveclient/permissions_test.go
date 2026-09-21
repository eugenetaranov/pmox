package pveclient

import (
	"context"
	"net/http"
	"testing"
)

func TestGetPermissionsAndHasPriv(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{
			"/":{"Sys.Audit":1},
			"/vms":{"VM.Audit":1,"VM.Clone":0},
			"/storage/local":{"Datastore.AllocateSpace":1}
		}}`))
	})
	perms, err := c.GetPermissions(context.Background())
	if err != nil {
		t.Fatalf("GetPermissions: %v", err)
	}

	// Direct grants.
	if !perms.HasPriv("/", "Sys.Audit") {
		t.Error("Sys.Audit on / should be granted")
	}
	if !perms.HasPriv("/vms", "VM.Audit") {
		t.Error("VM.Audit on /vms should be granted")
	}
	// Value 0 means not granted.
	if perms.HasPriv("/vms", "VM.Clone") {
		t.Error("VM.Clone is 0, must not be granted")
	}
	// Inheritance: Sys.Audit granted on / covers a descendant path.
	if !perms.HasPriv("/vms", "Sys.Audit") {
		t.Error("Sys.Audit on / should inherit to /vms")
	}
	// Deep path inherits from a mid ancestor and root.
	if !perms.HasPriv("/storage/local", "Datastore.AllocateSpace") {
		t.Error("Datastore.AllocateSpace on /storage/local should be granted")
	}
	if !perms.HasPriv("/storage/local/sub", "Sys.Audit") {
		t.Error("Sys.Audit should inherit from / down to a deep path")
	}
	// Absent privilege.
	if perms.HasPriv("/vms", "VM.PowerMgmt") {
		t.Error("VM.PowerMgmt was never granted")
	}
}

func TestAncestorPaths(t *testing.T) {
	got := ancestorPaths("/storage/local")
	want := []string{"/storage/local", "/storage", "/"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if r := ancestorPaths("/"); len(r) != 1 || r[0] != "/" {
		t.Errorf("ancestorPaths(/) = %v, want [/]", r)
	}
}
