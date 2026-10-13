package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/devbox/steps"
)

// newEnv describes this machine and the target user for the steps; file
// steps read from share's conf directory.
func newEnv(username, share string, log io.Writer) (*steps.Env, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return nil, fmt.Errorf("user %q: %w", username, err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	group := username
	if g, err := user.LookupGroupId(u.Gid); err == nil {
		group = g.Name
	}
	return &steps.Env{
		User: username, Group: group, UID: uid, GID: gid, Home: u.HomeDir,
		Arch: debArch(), Codename: codename(), Kernel: kernel(),
		Exec:     steps.OSExec{User: username},
		Files:    os.DirFS(filepath.Join(share, "conf")),
		Log:      log,
		Chown:    os.Geteuid() == 0,
		Download: download,
	}, nil
}

func debArch() string {
	if out, err := exec.Command("dpkg", "--print-architecture").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "amd64"
}

func kernel() string {
	out, _ := exec.Command("uname", "-r").Output()
	return strings.TrimSpace(string(out))
}

func codename() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "noble"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "VERSION_CODENAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return "noble"
}

func download(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}
