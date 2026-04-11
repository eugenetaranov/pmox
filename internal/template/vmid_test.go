package template

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

func newVMIDStub(t *testing.T, vmids []int) *pveclient.Client {
	t.Helper()
	parts := make([]string, 0, len(vmids))
	for _, v := range vmids {
		parts = append(parts, fmt.Sprintf(`{"vmid":%d,"type":"qemu","node":"pve","status":"stopped"}`, v))
	}
	body := `{"data":[` + strings.Join(parts, ",") + `]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := pveclient.New(srv.URL, "tok@pam!x", "secret", false)
	c.HTTPClient = srv.Client()
	return c
}

func TestReserveVMID_EmptyRange(t *testing.T) {
	c := newVMIDStub(t, []int{100, 101})
	got, err := reserveVMID(context.Background(), c, "pve")
	if err != nil {
		t.Fatalf("reserveVMID: %v", err)
	}
	if got != 9000 {
		t.Errorf("got %d, want 9000", got)
	}
}

func TestReserveVMID_LowestGap(t *testing.T) {
	c := newVMIDStub(t, []int{9000, 9001, 9003})
	got, err := reserveVMID(context.Background(), c, "pve")
	if err != nil {
		t.Fatalf("reserveVMID: %v", err)
	}
	if got != 9002 {
		t.Errorf("got %d, want 9002", got)
	}
}

func TestReserveVMID_Full(t *testing.T) {
	full := make([]int, 0, 100)
	for i := 9000; i <= 9099; i++ {
		full = append(full, i)
	}
	c := newVMIDStub(t, full)
	_, err := reserveVMID(context.Background(), c, "pve")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "9000-9099 is full") {
		t.Errorf("err = %v", err)
	}
}
