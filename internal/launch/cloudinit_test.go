package launch

import "testing"

func TestBuildCustomKV(t *testing.T) {
	opts := Options{
		Name: "web1", CPU: 2, MemMB: 2048,
		Storage: "local-lvm",
	}
	kv := BuildCustomKV(opts, 104)
	if _, ok := kv["ciuser"]; ok {
		t.Error("ciuser must not appear")
	}
	if _, ok := kv["sshkeys"]; ok {
		t.Error("sshkeys must not appear")
	}
	if _, ok := kv["cipassword"]; ok {
		t.Error("cipassword must not appear")
	}
	for _, k := range []string{"name", "memory", "cores", "agent", "ipconfig0", "cicustom", "ide2"} {
		if _, ok := kv[k]; !ok {
			t.Errorf("missing required key %q", k)
		}
	}
	if got, want := kv["cicustom"], "user=local-lvm:snippets/pmox-104-user-data.yaml"; got != want {
		t.Errorf("cicustom = %q, want %q", got, want)
	}
	if got := kv["ide2"]; got != "local-lvm:cloudinit" {
		t.Errorf("ide2 = %q, want local-lvm:cloudinit", got)
	}
	if got := kv["agent"]; got != "1" {
		t.Errorf("agent = %q, want 1", got)
	}
	if got := kv["ipconfig0"]; got != "ip=dhcp" {
		t.Errorf("ipconfig0 = %q, want ip=dhcp", got)
	}
}
