package launch

import "testing"

func TestBuildBuiltinKV(t *testing.T) {
	tests := []struct {
		name       string
		opts       Options
		wantUser   string
		wantSSHKey string
	}{
		{
			name: "default pmox user",
			opts: Options{
				Name: "web1", CPU: 2, MemMB: 2048,
				User: "pmox", SSHPubKey: "ssh-ed25519 AAAA test@host",
				Storage: "local-lvm",
			},
			wantUser:   "pmox",
			wantSSHKey: "ssh-ed25519 AAAA test@host",
		},
		{
			name: "custom user via opts.User",
			opts: Options{
				Name: "db1", CPU: 4, MemMB: 4096,
				User: "ubuntu", SSHPubKey: "ssh-rsa XYZ me@box",
				Storage: "local-lvm",
			},
			wantUser:   "ubuntu",
			wantSSHKey: "ssh-rsa XYZ me@box",
		},
		{
			name: "multi-line pubkey gets trimmed",
			opts: Options{
				Name: "trim", CPU: 1, MemMB: 512,
				User: "pmox", SSHPubKey: "ssh-ed25519 AAAA test\n",
				Storage: "local-lvm",
			},
			wantUser:   "pmox",
			wantSSHKey: "ssh-ed25519 AAAA test",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kv := BuildBuiltinKV(tc.opts, 101)
			if got := kv["ciuser"]; got != tc.wantUser {
				t.Errorf("ciuser = %q, want %q", got, tc.wantUser)
			}
			if got := kv["sshkeys"]; got != tc.wantSSHKey {
				t.Errorf("sshkeys = %q, want %q", got, tc.wantSSHKey)
			}
			if kv["agent"] != "1" {
				t.Errorf("agent = %q, want 1", kv["agent"])
			}
			if kv["ipconfig0"] != "ip=dhcp" {
				t.Errorf("ipconfig0 = %q, want ip=dhcp", kv["ipconfig0"])
			}
			if kv["name"] != tc.opts.Name {
				t.Errorf("name = %q, want %q", kv["name"], tc.opts.Name)
			}
			// Required keys.
			for _, k := range []string{"name", "memory", "cores", "agent", "ciuser", "sshkeys", "ipconfig0", "ide2"} {
				if _, ok := kv[k]; !ok {
					t.Errorf("missing required key %q", k)
				}
			}
			if got := kv["ide2"]; got != "local-lvm:cloudinit" {
				t.Errorf("ide2 = %q, want local-lvm:cloudinit", got)
			}
		})
	}
}

func TestBuildBuiltinKV_NoCicustom(t *testing.T) {
	kv := BuildBuiltinKV(Options{Name: "x", User: "pmox", SSHPubKey: "k"}, 101)
	if _, ok := kv["cicustom"]; ok {
		t.Error("cicustom key must not appear in built-in mode (slice 7 territory)")
	}
}
