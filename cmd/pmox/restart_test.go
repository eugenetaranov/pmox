package main

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/pvetest"
)

// newRestartFake is a cluster with one VM whose status follows the
// stop/shutdown/start calls made on it. ignoreShutdown makes the guest
// ignore a graceful shutdown (the task fails).
func newRestartFake(t *testing.T, status string, ignoreShutdown bool) *pvetest.Server {
	t.Helper()
	var mu sync.Mutex
	set := func(s string) { mu.Lock(); status = s; mu.Unlock() }
	f := pvetest.New(t)
	f.Handle("GET", "/cluster/resources", pvetest.JSON(oneVMResources))
	f.Handle("GET", "/status/current", func(w http.ResponseWriter, _ *http.Request, _ string) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write([]byte(`{"data":{"status":"` + status + `"}}`))
	})
	f.Handle("POST", "/status/shutdown", func(w http.ResponseWriter, _ *http.Request, _ string) {
		if ignoreShutdown {
			http.Error(w, `{"errors":"VM quit/powerdown failed - got timeout"}`, http.StatusInternalServerError)
			return
		}
		set("stopped")
		_, _ = w.Write([]byte(`{"data":"UPID:pve1:shutdown:"}`))
	})
	f.Handle("POST", "/status/stop", func(w http.ResponseWriter, _ *http.Request, _ string) {
		set("stopped")
		_, _ = w.Write([]byte(`{"data":"UPID:pve1:stop:"}`))
	})
	f.Handle("POST", "/status/start", func(w http.ResponseWriter, _ *http.Request, _ string) {
		set("running")
		_, _ = w.Write([]byte(`{"data":"UPID:pve1:start:"}`))
	})
	f.Handle("GET", "/tasks/", pvetest.TaskOK)
	f.Handle("GET", "/agent/network-get-interfaces", pvetest.JSON(web1AgentNet))
	return f
}

func TestRestart(t *testing.T) {
	wait := &restartFlags{wait: 5 * time.Second}

	t.Run("a running VM is shut down and started", func(t *testing.T) {
		f := newRestartFake(t, "running", false)
		cmd, out, _ := newTestInfoCmd()
		if err := executeRestart(cmd.Context(), cmd, f.Client(), "web1", wait); err != nil {
			t.Fatal(err)
		}
		if f.Count("POST", "/status/shutdown") != 1 || f.Count("POST", "/status/stop") != 0 || f.Count("POST", "/status/start") != 1 {
			t.Errorf("shutdown %d, stop %d, start %d", f.Count("POST", "/status/shutdown"), f.Count("POST", "/status/stop"), f.Count("POST", "/status/start"))
		}
		if !strings.Contains(out.String(), "web1 stopped") || !strings.Contains(out.String(), "web1 started") || !strings.Contains(out.String(), "192.168.1.43") {
			t.Errorf("stdout = %q", out.String())
		}
	})

	t.Run("--hard powers off", func(t *testing.T) {
		f := newRestartFake(t, "running", false)
		cmd, _, _ := newTestInfoCmd()
		if err := executeRestart(cmd.Context(), cmd, f.Client(), "web1", &restartFlags{hard: true, wait: 5 * time.Second}); err != nil {
			t.Fatal(err)
		}
		if f.Count("POST", "/status/stop") != 1 || f.Count("POST", "/status/shutdown") != 0 || f.Count("POST", "/status/start") != 1 {
			t.Errorf("stop %d, shutdown %d, start %d", f.Count("POST", "/status/stop"), f.Count("POST", "/status/shutdown"), f.Count("POST", "/status/start"))
		}
	})

	t.Run("a stopped VM is just started", func(t *testing.T) {
		f := newRestartFake(t, "stopped", false)
		cmd, _, _ := newTestInfoCmd()
		if err := executeRestart(cmd.Context(), cmd, f.Client(), "web1", wait); err != nil {
			t.Fatal(err)
		}
		if f.Count("POST", "/status/shutdown")+f.Count("POST", "/status/stop") != 0 || f.Count("POST", "/status/start") != 1 {
			t.Errorf("a stopped VM should only be started")
		}
	})

	t.Run("a shutdown that fails leaves the VM alone and suggests --hard", func(t *testing.T) {
		f := newRestartFake(t, "running", true)
		cmd, _, _ := newTestInfoCmd()
		err := executeRestart(cmd.Context(), cmd, f.Client(), "web1", wait)
		if err == nil || !strings.Contains(err.Error(), "pmox restart --hard web1") {
			t.Fatalf("err = %v, want a pointer at --hard", err)
		}
		if f.Count("POST", "/status/start") != 0 || f.Count("POST", "/status/stop") != 0 {
			t.Error("the VM must be left as it is")
		}
	})

	t.Run("--no-wait skips the IP poll", func(t *testing.T) {
		f := newRestartFake(t, "running", false)
		cmd, _, _ := newTestInfoCmd()
		if err := executeRestart(cmd.Context(), cmd, f.Client(), "web1", &restartFlags{noWait: true}); err != nil {
			t.Fatal(err)
		}
		if f.Count("GET", "/agent/network-get-interfaces") != 0 {
			t.Error("--no-wait should not poll the agent")
		}
	})
}

func TestRestartIsAShortcutWithoutPick(t *testing.T) {
	c := findPath(t, "restart")
	if c.Annotations[canonicalAnnotation] != "vm restart" || c.GroupID != groupCommon {
		t.Errorf("restart: canonical %q, group %q", c.Annotations[canonicalAnnotation], c.GroupID)
	}
	if c.Flags().Lookup("hard") == nil || c.Flags().Lookup("force") != nil || c.Flags().Lookup("pick") != nil {
		t.Error("restart takes --hard, and neither --force nor --pick")
	}
	if !strings.Contains(c.Example, "pmox restart web1") {
		t.Error("restart --help should show an example")
	}
}
