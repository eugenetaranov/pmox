package template

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestFetchCatalogue_FromFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/simplestreams.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	entries, err := fetchCatalogue(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchCatalogue: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3 (arm64 filtered out)", len(entries))
	}
	// Sorted newest-first: 20240423 (noble), 20240315 (jammy), 20240210 (mantic).
	if entries[0].VersionDate != "20240423" {
		t.Errorf("entries[0].VersionDate = %q, want 20240423", entries[0].VersionDate)
	}
	if entries[1].VersionDate != "20240315" {
		t.Errorf("entries[1].VersionDate = %q, want 20240315", entries[1].VersionDate)
	}
	if entries[2].VersionDate != "20240210" {
		t.Errorf("entries[2].VersionDate = %q, want 20240210", entries[2].VersionDate)
	}
	if !entries[0].IsLTS || !entries[1].IsLTS {
		t.Errorf("noble and jammy should be LTS, got %+v", entries[:2])
	}
	if entries[2].IsLTS {
		t.Errorf("mantic should not be LTS: %+v", entries[2])
	}
	// SHA256 roundtrip.
	wantSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if entries[0].SHA256 != wantSHA {
		t.Errorf("noble sha256 = %q, want %q", entries[0].SHA256, wantSHA)
	}
	// URL composed from mirror base + path.
	if !strings.HasPrefix(entries[0].URL, "https://cloud-images.ubuntu.com/") {
		t.Errorf("url = %q", entries[0].URL)
	}
	if !strings.Contains(entries[0].URL, "releases/noble/release-20240423") {
		t.Errorf("url = %q, want noble release path", entries[0].URL)
	}
}

func TestFetchCatalogue_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{`))
	}))
	defer srv.Close()
	_, err := fetchCatalogue(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("err = %v, want 'parse'", err)
	}
	if !strings.Contains(err.Error(), srv.URL) {
		t.Errorf("err = %v, want URL mention", err)
	}
}

func TestFetchCatalogue_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := fetchCatalogue(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want status code mention", err)
	}
}

func TestDefaultImageIndex(t *testing.T) {
	cases := []struct {
		name    string
		entries []ImageEntry
		want    int
	}{
		{
			name: "latest LTS cursor",
			entries: []ImageEntry{
				{VersionDate: "20240423", IsLTS: false}, // interim first
				{VersionDate: "20240315", IsLTS: true},
				{VersionDate: "20240101", IsLTS: true},
			},
			want: 1,
		},
		{
			name: "no LTS fallback to 0",
			entries: []ImageEntry{
				{VersionDate: "20240423", IsLTS: false},
				{VersionDate: "20240315", IsLTS: false},
			},
			want: 0,
		},
		{
			name:    "empty list returns 0",
			entries: nil,
			want:    0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultImageIndex(tc.entries); got != tc.want {
				t.Errorf("defaultImageIndex = %d, want %d", got, tc.want)
			}
		})
	}
}
