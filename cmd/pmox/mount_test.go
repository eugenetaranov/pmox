package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMountRsyncArgs(t *testing.T) {
	target := &sshTarget{IP: "10.0.0.1", User: "pmox", Key: "/home/user/.ssh/id"}

	tests := []struct {
		name         string
		localPath    string
		remotePath   string
		noGitignore  bool
		noDelete     bool
		excludes     []string
		extra        []string
		wantContains []string
		wantAbsent   []string
		wantSuffix   []string
	}{
		{
			name:       "defaults",
			localPath:  "./src",
			remotePath: "/opt/app",
			excludes:   defaultMountExcludes,
			wantContains: []string{
				"-az", "--partial", "--delete",
				"--filter=:- .gitignore",
				"--exclude=.git",
				"--exclude=node_modules",
				"--exclude=__pycache__",
			},
			wantSuffix: []string{"./src/", "pmox@10.0.0.1:/opt/app"},
		},
		{
			name:        "no-gitignore",
			localPath:   "./src",
			remotePath:  "/opt/app",
			noGitignore: true,
			excludes:    defaultMountExcludes,
			wantContains: []string{
				"--delete",
			},
			wantAbsent: []string{
				"--filter",
			},
		},
		{
			name:       "no-delete",
			localPath:  "./src",
			remotePath: "/opt/app",
			noDelete:   true,
			excludes:   defaultMountExcludes,
			wantContains: []string{
				"--filter=:- .gitignore",
			},
			wantAbsent: []string{
				"--delete",
			},
		},
		{
			name:       "custom excludes replace defaults",
			localPath:  "./src",
			remotePath: "/opt/app",
			excludes:   []string{".git", "*.log"},
			wantContains: []string{
				"--exclude=.git",
				"--exclude=*.log",
			},
			wantAbsent: []string{
				"--exclude=node_modules",
				"--exclude=.venv",
			},
		},
		{
			name:       "extra args via --",
			localPath:  "./src",
			remotePath: "/opt/app",
			excludes:   defaultMountExcludes,
			extra:      []string{"--bwlimit=1000"},
			wantContains: []string{
				"--bwlimit=1000",
			},
		},
		{
			name:       "trailing slash added to local path",
			localPath:  "./src",
			remotePath: "/opt/app/",
			excludes:   []string{".git"},
			wantSuffix: []string{"./src/", "pmox@10.0.0.1:/opt/app/"},
		},
		{
			name:       "already has trailing slash",
			localPath:  "./src/",
			remotePath: "/opt/app",
			excludes:   []string{".git"},
			wantSuffix: []string{"./src/", "pmox@10.0.0.1:/opt/app"},
		},
		{
			name:        "no-gitignore and no-delete combined",
			localPath:   "./src",
			remotePath:  "/opt/app",
			noGitignore: true,
			noDelete:    true,
			excludes:    []string{".git"},
			wantContains: []string{
				"-az", "--partial", "--exclude=.git",
			},
			wantAbsent: []string{
				"--delete", "--filter",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildMountRsyncArgs("/usr/bin/rsync", target, tt.localPath, tt.remotePath,
				tt.noGitignore, tt.noDelete, tt.excludes, tt.extra)
			joined := strings.Join(args, " ")

			assert.Equal(t, "/usr/bin/rsync", args[0])

			for _, s := range tt.wantContains {
				assert.Contains(t, joined, s, "should contain %q", s)
			}
			for _, s := range tt.wantAbsent {
				assert.NotContains(t, joined, s, "should not contain %q", s)
			}
			if len(tt.wantSuffix) > 0 {
				got := args[len(args)-len(tt.wantSuffix):]
				assert.Equal(t, tt.wantSuffix, got)
			}
		})
	}
}

func TestBuildMountRsyncArgs_SSHOptions(t *testing.T) {
	target := &sshTarget{IP: "10.0.0.5", User: "ubuntu", Key: "/tmp/key"}
	args := buildMountRsyncArgs("/usr/bin/rsync", target, "./src", "/opt/app",
		false, false, []string{".git"}, nil)

	assert.Equal(t, "-e", args[1])
	assert.Contains(t, args[2], "ssh")
	assert.Contains(t, args[2], "-i /tmp/key")
	assert.Contains(t, args[2], "StrictHostKeyChecking=no")
}

func TestResolveExcludes(t *testing.T) {
	t.Run("flag excludes take precedence", func(t *testing.T) {
		got := resolveExcludes([]string{".git", "*.log"})
		assert.Equal(t, []string{".git", "*.log"}, got)
	})

	t.Run("falls back to defaults when no flags and no config", func(t *testing.T) {
		old := configLoadFn
		configLoadFn = func() (*mountConfig, error) {
			return &mountConfig{}, nil
		}
		defer func() { configLoadFn = old }()

		got := resolveExcludes(nil)
		assert.Equal(t, defaultMountExcludes, got)
	})

	t.Run("config excludes replace defaults", func(t *testing.T) {
		old := configLoadFn
		configLoadFn = func() (*mountConfig, error) {
			return &mountConfig{MountExcludes: []string{".git", "vendor/"}}, nil
		}
		defer func() { configLoadFn = old }()

		got := resolveExcludes(nil)
		assert.Equal(t, []string{".git", "vendor/"}, got)
	})
}

func TestPidFilePath(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		p1 := pidFilePath("web1", "/home/user/src", "/opt/app")
		p2 := pidFilePath("web1", "/home/user/src", "/opt/app")
		assert.Equal(t, p1, p2)
	})

	t.Run("different paths produce different hashes", func(t *testing.T) {
		p1 := pidFilePath("web1", "/home/user/src", "/opt/app")
		p2 := pidFilePath("web1", "/home/user/other", "/opt/app")
		assert.NotEqual(t, p1, p2)
	})

	t.Run("different VMs produce different filenames", func(t *testing.T) {
		p1 := pidFilePath("web1", "/home/user/src", "/opt/app")
		p2 := pidFilePath("web2", "/home/user/src", "/opt/app")
		assert.NotEqual(t, p1, p2)
	})

	t.Run("filename contains vm name prefix", func(t *testing.T) {
		p := pidFilePath("myvm", "/src", "/dst")
		base := filepath.Base(p)
		assert.True(t, strings.HasPrefix(base, "myvm-"))
		assert.True(t, strings.HasSuffix(base, ".pid"))
	})
}

func TestMountArgValidation(t *testing.T) {
	tests := []struct {
		name     string
		arg      string
		wantRef  string
		wantPath string
		wantOk   bool
	}{
		{"valid remote", "web1:/opt/app", "web1", "/opt/app", true},
		{"vmid remote", "100:/tmp/", "100", "/tmp/", true},
		{"local path", "./src", "", "", false},
		{"absolute local", "/home/user/src", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, path, isRemote := parseRemoteArg(tt.arg)
			assert.Equal(t, tt.wantRef, ref)
			assert.Equal(t, tt.wantPath, path)
			assert.Equal(t, tt.wantOk, isRemote)
		})
	}
}

func TestMountSourceValidation(t *testing.T) {
	t.Run("nonexistent source", func(t *testing.T) {
		cmd := newMountCmd()
		cmd.SetArgs([]string{"/nonexistent/path/that/does/not/exist", "web1:/opt/app"})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("source is a file", func(t *testing.T) {
		tmp := t.TempDir()
		f := filepath.Join(tmp, "testfile")
		require.NoError(t, os.WriteFile(f, []byte("test"), 0o644))

		cmd := newMountCmd()
		cmd.SetArgs([]string{f, "web1:/opt/app"})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a directory")
	})
}

func TestDebounceDefault(t *testing.T) {
	cmd := newMountCmd()
	d, err := cmd.Flags().GetDuration("debounce")
	require.NoError(t, err)
	assert.Equal(t, 300*time.Millisecond, d)
}

func TestDefaultMountExcludes(t *testing.T) {
	expected := []string{
		".git", ".venv", ".terraform", ".terraform.*",
		"node_modules", "__pycache__", ".DS_Store",
		"*.swp", "*.swo", "*~",
	}
	assert.Equal(t, expected, defaultMountExcludes)
}
