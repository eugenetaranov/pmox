package pveclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   ReachStatus
	}{
		{"401 is reachable pve", http.StatusUnauthorized, "", Reachable},
		{"200 version envelope is reachable", http.StatusOK, `{"data":{"version":"8.2.2"}}`, Reachable},
		{"200 non-pve json is not pve", http.StatusOK, `{"hello":"world"}`, ReachNotPVE},
		{"200 html is not pve", http.StatusOK, "<html>hi</html>", ReachNotPVE},
		{"404 is not pve", http.StatusNotFound, "not found", ReachNotPVE},
		{"500 is not pve", http.StatusInternalServerError, "boom", ReachNotPVE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/version" {
					t.Errorf("probe hit %q, want /version", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "" {
					t.Errorf("probe sent Authorization header; must be unauthenticated")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			got, _ := Probe(context.Background(), srv.URL, false)
			if got != tc.want {
				t.Errorf("Probe status = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProbeUnreachable(t *testing.T) {
	// A server that is closed immediately → connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	got, err := Probe(context.Background(), url, false)
	if got != ReachUnreachable {
		t.Errorf("Probe status = %v, want ReachUnreachable (err=%v)", got, err)
	}
}

func TestProbeTLSUntrusted(t *testing.T) {
	// httptest TLS server uses a self-signed cert → verification fails when
	// insecure=false.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	got, err := Probe(context.Background(), srv.URL, false)
	if got != ReachTLSUntrusted {
		t.Errorf("Probe status = %v, want ReachTLSUntrusted (err=%v)", got, err)
	}

	// With insecure=true the same server is reachable (401 → Reachable).
	got, _ = Probe(context.Background(), srv.URL, true)
	if got != Reachable {
		t.Errorf("Probe(insecure) status = %v, want Reachable", got)
	}
}
