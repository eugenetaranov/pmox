package pvetest

import (
	"io"
	"net/http"
	"testing"
)

func get(t *testing.T, s *Server, method, path string) string {
	t.Helper()
	req, err := http.NewRequest(method, s.URL()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestHandleExactDoesNotMatchPrefix(t *testing.T) {
	s := New(t)
	s.HandleExact("GET", "/nodes/pve/qemu/100", JSON("exact"))
	s.SetFallback(JSON("fallback"))
	if got := get(t, s, "GET", "/nodes/pve/qemu/100"); got != "exact" {
		t.Errorf("exact path got %q", got)
	}
	if got := get(t, s, "GET", "/nodes/pve/qemu/1000"); got != "fallback" {
		t.Errorf("/qemu/1000 got %q, want fallback", got)
	}
	if got := get(t, s, "POST", "/nodes/pve/qemu/100"); got != "fallback" {
		t.Errorf("wrong method got %q, want fallback", got)
	}
}

func TestHandlePattern(t *testing.T) {
	s := New(t)
	s.HandlePattern("GET /nodes/{node}/qemu/100/config", JSON("cfg"))
	s.HandlePattern("/nodes/{node}/tasks/{rest...}", JSON("task"))
	s.SetFallback(JSON("fallback"))
	cases := []struct{ method, path, want string }{
		{"GET", "/nodes/pve/qemu/100/config", "cfg"},
		{"GET", "/nodes/pve/qemu/1000/config", "fallback"},
		{"POST", "/nodes/pve/qemu/100/config", "fallback"},
		{"GET", "/nodes//qemu/100/config", "fallback"},
		{"GET", "/nodes/pve/qemu/100/config/extra", "fallback"},
		{"DELETE", "/nodes/pve/tasks/UPID:x/status", "task"},
		{"GET", "/nodes/pve/tasks/", "task"},
	}
	for _, tc := range cases {
		if got := get(t, s, tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestHandleSubstringBackwardCompatible(t *testing.T) {
	s := New(t)
	s.Handle("", "/qemu/100", JSON("sub"))
	if got := get(t, s, "GET", "/nodes/pve/qemu/1000"); got != "sub" {
		t.Errorf("substring Handle got %q", got)
	}
	if n := s.Count("GET", "/qemu/"); n != 1 {
		t.Errorf("Count = %d", n)
	}
}
