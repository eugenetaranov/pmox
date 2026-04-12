package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRemoteArg(t *testing.T) {
	tests := []struct {
		name       string
		arg        string
		wantRef    string
		wantPath   string
		wantRemote bool
	}{
		{"local path", "./file.txt", "", "", false},
		{"absolute local", "/tmp/file", "", "", false},
		{"remote with path", "web1:/tmp/file", "web1", "/tmp/file", true},
		{"remote root", "web1:/", "web1", "/", true},
		{"vmid remote", "100:/tmp/", "100", "/tmp/", true},
		{"empty remote path", "web1:", "web1", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, path, isRemote := parseRemoteArg(tt.arg)
			assert.Equal(t, tt.wantRef, ref)
			assert.Equal(t, tt.wantPath, path)
			assert.Equal(t, tt.wantRemote, isRemote)
		})
	}
}

func TestResolveTransferArgs(t *testing.T) {
	tests := []struct {
		name          string
		args          [2]string
		wantLocal     string
		wantVMRef     string
		wantRemote    string
		wantLocalSrc  bool
		wantErr       string
	}{
		{
			name:         "local to VM",
			args:         [2]string{"./file.txt", "web1:/tmp/"},
			wantLocal:    "./file.txt",
			wantVMRef:    "web1",
			wantRemote:   "/tmp/",
			wantLocalSrc: true,
		},
		{
			name:         "VM to local",
			args:         [2]string{"web1:/var/log/syslog", "./logs/"},
			wantLocal:    "./logs/",
			wantVMRef:    "web1",
			wantRemote:   "/var/log/syslog",
			wantLocalSrc: false,
		},
		{
			name:    "both local",
			args:    [2]string{"./a", "./b"},
			wantErr: "exactly one argument must reference a VM",
		},
		{
			name:    "both remote",
			args:    [2]string{"vm1:/a", "vm2:/b"},
			wantErr: "VM-to-VM transfer is not supported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local, remote, localIsSrc, err := resolveTransferArgs(tt.args[:])
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantLocal, local)
			assert.Equal(t, tt.wantVMRef, remote.vmRef)
			assert.Equal(t, tt.wantRemote, remote.remotePath)
			assert.Equal(t, tt.wantLocalSrc, localIsSrc)
		})
	}
}

func TestBuildScpArgs(t *testing.T) {
	tests := []struct {
		name         string
		target       *sshTarget
		localPath    string
		remotePath   string
		localIsSrc   bool
		recursive    bool
		extra        []string
		wantContains []string
		wantSuffix   []string
	}{
		{
			name:       "local to VM with key",
			target:     &sshTarget{IP: "10.0.0.1", User: "pmox", Key: "/home/user/.ssh/id"},
			localPath:  "./app.tar.gz",
			remotePath: "/tmp/",
			localIsSrc: true,
			wantContains: []string{
				"-o", "StrictHostKeyChecking=no",
				"-i", "/home/user/.ssh/id",
			},
			wantSuffix: []string{"./app.tar.gz", "pmox@10.0.0.1:/tmp/"},
		},
		{
			name:       "VM to local without key",
			target:     &sshTarget{IP: "10.0.0.1", User: "ubuntu", Key: ""},
			localPath:  "./logs/",
			remotePath: "/var/log/syslog",
			localIsSrc: false,
			wantSuffix: []string{"ubuntu@10.0.0.1:/var/log/syslog", "./logs/"},
		},
		{
			name:       "recursive flag",
			target:     &sshTarget{IP: "10.0.0.1", User: "pmox", Key: ""},
			localPath:  "./dir/",
			remotePath: "/opt/",
			localIsSrc: true,
			recursive:  true,
			wantContains: []string{"-r"},
		},
		{
			name:       "extra flags",
			target:     &sshTarget{IP: "10.0.0.1", User: "pmox", Key: ""},
			localPath:  "./big.tar",
			remotePath: "/tmp/",
			localIsSrc: true,
			extra:      []string{"-l", "1000"},
			wantContains: []string{"-l", "1000"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildScpArgs("/usr/bin/scp", tt.target, tt.localPath, tt.remotePath, tt.localIsSrc, tt.recursive, tt.extra)
			joined := strings.Join(args, " ")
			for _, s := range tt.wantContains {
				assert.Contains(t, joined, s)
			}
			if len(tt.wantSuffix) > 0 {
				got := args[len(args)-len(tt.wantSuffix):]
				assert.Equal(t, tt.wantSuffix, got)
			}
			assert.Equal(t, "/usr/bin/scp", args[0])
			assert.NotContains(t, joined, "-i -o", "no -i when key is empty")
		})
	}
}

func TestBuildScpArgs_NoKeyFlag(t *testing.T) {
	args := buildScpArgs("/usr/bin/scp", &sshTarget{IP: "10.0.0.1", User: "pmox", Key: ""}, "./f", "/tmp/", true, false, nil)
	for _, a := range args {
		if a == "-i" {
			t.Error("-i should not appear when key is empty")
		}
	}
}

func TestBuildRsyncArgs(t *testing.T) {
	tests := []struct {
		name         string
		target       *sshTarget
		localPath    string
		remotePath   string
		localIsSrc   bool
		extra        []string
		wantE        string
		wantSuffix   []string
	}{
		{
			name:       "local to VM with key",
			target:     &sshTarget{IP: "10.0.0.1", User: "pmox", Key: "/home/user/.ssh/id"},
			localPath:  "./src/",
			remotePath: "/opt/app/",
			localIsSrc: true,
			wantE:      "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i /home/user/.ssh/id",
			wantSuffix: []string{"./src/", "pmox@10.0.0.1:/opt/app/"},
		},
		{
			name:       "VM to local without key",
			target:     &sshTarget{IP: "10.0.0.1", User: "ubuntu", Key: ""},
			localPath:  "./logs/",
			remotePath: "/var/log/",
			localIsSrc: false,
			wantE:      "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
			wantSuffix: []string{"ubuntu@10.0.0.1:/var/log/", "./logs/"},
		},
		{
			name:       "extra flags",
			target:     &sshTarget{IP: "10.0.0.1", User: "pmox", Key: ""},
			localPath:  "./src/",
			remotePath: "/opt/",
			localIsSrc: true,
			extra:      []string{"--delete", "--exclude", ".git"},
			wantE:      "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildRsyncArgs("/usr/bin/rsync", tt.target, tt.localPath, tt.remotePath, tt.localIsSrc, tt.extra)
			assert.Equal(t, "/usr/bin/rsync", args[0])
			assert.Equal(t, "-e", args[1])
			assert.Equal(t, tt.wantE, args[2])
			if len(tt.wantSuffix) > 0 {
				got := args[len(args)-len(tt.wantSuffix):]
				assert.Equal(t, tt.wantSuffix, got)
			}
			if len(tt.extra) > 0 {
				joined := strings.Join(args, " ")
				for _, e := range tt.extra {
					assert.Contains(t, joined, e)
				}
			}
		})
	}
}

func TestBuildRsyncArgs_NoKeyInE(t *testing.T) {
	args := buildRsyncArgs("/usr/bin/rsync", &sshTarget{IP: "10.0.0.1", User: "pmox", Key: ""}, "./f", "/tmp/", true, nil)
	assert.NotContains(t, args[2], "-i")
}
