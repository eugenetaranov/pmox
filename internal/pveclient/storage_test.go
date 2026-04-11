package pveclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// --- CreateVM ---

func TestCreateVM_HappyPath(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"data":"UPID:pve1:00001234:00005678:680ABCD0:qmcreate:9000:root@pam:"}`))
	})
	upid, err := c.CreateVM(context.Background(), "pve1", 9000, map[string]string{
		"name":   "test",
		"memory": "2048",
		"scsi0":  "local-lvm:0,importfrom=local:iso/noble.img",
	})
	if err != nil {
		t.Fatalf("CreateVM: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q", gotMethod)
	}
	if gotPath != "/nodes/pve1/qemu" {
		t.Errorf("path = %q", gotPath)
	}
	form, _ := url.ParseQuery(gotBody)
	if form.Get("vmid") != "9000" {
		t.Errorf("vmid = %q, want 9000", form.Get("vmid"))
	}
	if form.Get("name") != "test" || form.Get("memory") != "2048" {
		t.Errorf("form fields missing: %v", form)
	}
	// The importfrom parameter must pass through unchanged in the
	// scsi0 field value — this is the core PVE 8.0+ primitive the
	// create-template slice depends on.
	if got := form.Get("scsi0"); got != "local-lvm:0,importfrom=local:iso/noble.img" {
		t.Errorf("scsi0 = %q, want importfrom verbatim", got)
	}
	if !strings.HasPrefix(upid, "UPID:") {
		t.Errorf("upid = %q", upid)
	}
}

func TestCreateVM_ServerError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.CreateVM(context.Background(), "pve1", 9000, nil)
	if !errors.Is(err, ErrAPIError) {
		t.Errorf("err = %v, want ErrAPIError", err)
	}
}

func TestCreateVM_BadRequestSurfacesMessage(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":{"scsi0":"unknown parameter"}}`))
	})
	_, err := c.CreateVM(context.Background(), "pve1", 9000, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown parameter") {
		t.Errorf("err = %v, want pve text", err)
	}
}

// --- ConvertToTemplate ---

func TestConvertToTemplate_HappyPath(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	if err := c.ConvertToTemplate(context.Background(), "pve1", 9000); err != nil {
		t.Fatalf("ConvertToTemplate: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/nodes/pve1/qemu/9000/template" {
		t.Errorf("method/path = %q %q", gotMethod, gotPath)
	}
	if gotBody != "" {
		t.Errorf("body = %q, want empty", gotBody)
	}
}

func TestConvertToTemplate_RunningVM(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":{"vmid":"VM is running"}}`))
	})
	err := c.ConvertToTemplate(context.Background(), "pve1", 9000)
	if !errors.Is(err, ErrAPIError) {
		t.Errorf("err = %v, want ErrAPIError", err)
	}
}

// --- DownloadURL ---

func TestDownloadURL_HappyPath(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"data":"UPID:pve1:download:"}`))
	})
	upid, err := c.DownloadURL(context.Background(), "pve1", "local", map[string]string{
		"url":                "https://example.com/noble.img",
		"content":            "iso",
		"filename":           "noble.img",
		"checksum":           "deadbeef",
		"checksum-algorithm": "sha256",
	})
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q", gotMethod)
	}
	if gotPath != "/nodes/pve1/storage/local/download-url" {
		t.Errorf("path = %q", gotPath)
	}
	form, _ := url.ParseQuery(gotBody)
	for _, k := range []string{"url", "content", "filename", "checksum", "checksum-algorithm"} {
		if form.Get(k) == "" {
			t.Errorf("form missing %q: %v", k, form)
		}
	}
	if form.Get("content") != "iso" {
		t.Errorf("content = %q", form.Get("content"))
	}
	if form.Get("checksum-algorithm") != "sha256" {
		t.Errorf("checksum-algorithm = %q", form.Get("checksum-algorithm"))
	}
	if form.Get("url") != "https://example.com/noble.img" {
		t.Errorf("url = %q", form.Get("url"))
	}
	if !strings.HasPrefix(upid, "UPID:") {
		t.Errorf("upid = %q", upid)
	}
}

func TestDownloadURL_Unauthorized(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := c.DownloadURL(context.Background(), "pve1", "local", map[string]string{"url": "x"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

