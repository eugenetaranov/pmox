package launch

import "testing"

func TestBuildCustomKV(t *testing.T) {
	opts := Options{
		Name: "web1", CPU: 2, MemMB: 2048,
		Storage:        "vm-data",
		SnippetStorage: "local",
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
	if got, want := kv["cicustom"], "user=local:snippets/pmox-104-user-data.yaml"; got != want {
		t.Errorf("cicustom = %q, want %q", got, want)
	}
	if got := kv["ide2"]; got != "vm-data:cloudinit" {
		t.Errorf("ide2 = %q, want vm-data:cloudinit", got)
	}
	if got := kv["agent"]; got != "1" {
		t.Errorf("agent = %q, want 1", got)
	}
	if got := kv["ipconfig0"]; got != "ip=dhcp" {
		t.Errorf("ipconfig0 = %q, want ip=dhcp", got)
	}
	if _, ok := kv["net0"]; ok {
		t.Error("net0 must not appear; Run adds it only when Bridge is set")
	}
}

func TestSetNet0Bridge(t *testing.T) {
	tests := []struct {
		name, net0, bridge, want string
	}{
		{
			name:   "replace existing bridge keeps model/MAC and params",
			net0:   "virtio=BC:24:11:AA:BB:CC,bridge=vmbr0,firewall=1,tag=20",
			bridge: "vmbr1",
			want:   "virtio=BC:24:11:AA:BB:CC,bridge=vmbr1,firewall=1,tag=20",
		},
		{
			name:   "bridge not first after model",
			net0:   "e1000=BC:24:11:00:00:01,firewall=1,bridge=vmbr0",
			bridge: "vmbr9",
			want:   "e1000=BC:24:11:00:00:01,firewall=1,bridge=vmbr9",
		},
		{
			name:   "same bridge is a no-op",
			net0:   "virtio=BC:24:11:AA:BB:CC,bridge=vmbr0",
			bridge: "vmbr0",
			want:   "virtio=BC:24:11:AA:BB:CC,bridge=vmbr0",
		},
		{
			name:   "missing bridge inserted after model",
			net0:   "virtio=BC:24:11:AA:BB:CC,firewall=1",
			bridge: "vmbr2",
			want:   "virtio=BC:24:11:AA:BB:CC,bridge=vmbr2,firewall=1",
		},
		{
			name:   "model only",
			net0:   "virtio",
			bridge: "vmbr2",
			want:   "virtio,bridge=vmbr2",
		},
		{
			name:   "empty net0 creates virtio nic",
			net0:   "",
			bridge: "vmbr3",
			want:   "virtio,bridge=vmbr3",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := setNet0Bridge(tc.net0, tc.bridge); got != tc.want {
				t.Errorf("setNet0Bridge(%q, %q) = %q, want %q", tc.net0, tc.bridge, got, tc.want)
			}
		})
	}
}
