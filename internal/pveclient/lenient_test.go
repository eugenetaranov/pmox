package pveclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestFlexIntDecoding(t *testing.T) {
	cases := map[string]int64{
		`1`: 1, `"1"`: 1, `" 42 "`: 42, `true`: 1, `false`: 0,
		`null`: 0, `""`: 0, `1.0`: 1, `"2e3"`: 2000, `1048576`: 1048576,
	}
	for in, want := range cases {
		var f flexInt
		if err := json.Unmarshal([]byte(in), &f); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if int64(f) != want {
			t.Errorf("%s = %d, want %d", in, f, want)
		}
	}
	var f flexInt
	if err := json.Unmarshal([]byte(`"abc"`), &f); err == nil {
		t.Error("non-numeric string should fail")
	}
}

func TestResourceLenientDecoding(t *testing.T) {
	var rs []Resource
	in := `[{"vmid":"100","name":"a","status":"running","tags":"pmox","uptime":5,"template":true},
		{"vmid":101,"name":"b","template":"0"}]`
	if err := json.Unmarshal([]byte(in), &rs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rs[0].VMID != 100 || rs[0].Template != 1 || rs[0].Uptime != 5 || rs[0].Name != "a" || rs[0].Tags != "pmox" {
		t.Errorf("rs[0] = %+v", rs[0])
	}
	if rs[1].VMID != 101 || rs[1].Template != 0 {
		t.Errorf("rs[1] = %+v", rs[1])
	}
}

func TestStorageAndStatusLenientDecoding(t *testing.T) {
	var s Storage
	if err := json.Unmarshal([]byte(`{"storage":"local","content":"iso,images","active":true,"enabled":"1","avail":"107374182400","total":536870912000}`), &s); err != nil {
		t.Fatalf("storage: %v", err)
	}
	if s.Active != 1 || s.Enabled != 1 || s.Storage != "local" {
		t.Errorf("storage = %+v", s)
	}
	if s.Avail != 107374182400 || s.Total != 536870912000 {
		t.Errorf("storage capacity = avail:%d total:%d, want avail:107374182400 total:536870912000", s.Avail, s.Total)
	}

	var inactive Storage
	if err := json.Unmarshal([]byte(`{"storage":"nfs-share","active":false,"enabled":1}`), &inactive); err != nil {
		t.Fatalf("inactive storage: %v", err)
	}
	if inactive.Avail != 0 || inactive.Total != 0 {
		t.Errorf("inactive storage capacity = avail:%d total:%d, want both 0 (PVE omits them)", inactive.Avail, inactive.Total)
	}
	var st VMStatus
	if err := json.Unmarshal([]byte(`{"status":"running","vmid":"104","cpus":2,"mem":"1024","maxmem":2048,"uptime":"7"}`), &st); err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.VMID != 104 || st.CPUs != 2 || st.Mem != 1024 || st.MaxMem != 2048 || st.Uptime != 7 || !st.IsRunning() {
		t.Errorf("status = %+v", st)
	}
}

func TestGetConfigKeepsIntegerLiterals(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"memory":1048576,"cores":4,"name":"vm","template":1,"balloon":0}}`))
	})
	cfg, err := c.GetConfig(context.Background(), "pve", 100)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	want := map[string]string{"memory": "1048576", "cores": "4", "name": "vm", "template": "1", "balloon": "0"}
	for k, v := range want {
		if cfg[k] != v {
			t.Errorf("cfg[%s] = %q, want %q", k, cfg[k], v)
		}
	}
}

func TestResourceHelpers(t *testing.T) {
	r := Resource{Status: "running", Tags: " web ; PMOX,db ", Template: 0}
	if !r.IsRunning() || r.State() != StateRunning || r.IsTemplate() {
		t.Errorf("state helpers wrong for %+v", r)
	}
	for _, tag := range []string{"pmox", "web", "DB"} {
		if !r.HasTag(tag) {
			t.Errorf("HasTag(%q) = false", tag)
		}
	}
	if r.HasTag("pm") || (Resource{}).HasTag("pmox") {
		t.Error("HasTag false positive")
	}
	if got := r.TagList(); len(got) != 3 || got[0] != "web" || got[1] != "PMOX" || got[2] != "db" {
		t.Errorf("TagList = %q", got)
	}
	if (Resource{Status: "stopped"}).IsRunning() {
		t.Error("stopped VM reported running")
	}
}

func TestStorageHasContent(t *testing.T) {
	s := Storage{Content: "iso, images,snippets"}
	for _, k := range []string{"iso", "images", "snippets"} {
		if !s.HasContent(k) {
			t.Errorf("HasContent(%q) = false", k)
		}
	}
	if s.HasContent("image") || s.HasContent("vztmpl") {
		t.Error("HasContent false positive")
	}
	if !s.SupportsVMDisks() {
		t.Error("SupportsVMDisks = false")
	}
}

func TestWaitTaskFailureIsTaskError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"clone failed"}}`))
	})
	err := c.WaitTask(context.Background(), "pve", "UPID:pve:1", time.Second)
	var te *TaskError
	if !errors.As(err, &te) || te.UPID != "UPID:pve:1" || te.ExitStatus != "clone failed" {
		t.Fatalf("err = %v, want *TaskError", err)
	}
	if !errors.Is(err, ErrTaskFailed) || !errors.Is(err, ErrAPIError) {
		t.Errorf("TaskError should match ErrTaskFailed and ErrAPIError")
	}
	if want := "api error: pve task UPID:pve:1: clone failed"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
	if !IsFatalPollError(err) {
		t.Error("a failed task must be fatal, not transient")
	}
}

func TestSentinelWrapping(t *testing.T) {
	if !errors.Is(ErrForbidden, ErrUnauthorized) {
		t.Error("ErrForbidden should wrap ErrUnauthorized")
	}
	if !errors.Is(ErrTaskFailed, ErrAPIError) {
		t.Error("ErrTaskFailed should wrap ErrAPIError")
	}
}
