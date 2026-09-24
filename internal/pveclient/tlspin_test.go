package pveclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchCertFingerprint(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(srv.Close)

	want := func() string {
		sum := sha256.Sum256(srv.Certificate().Raw)
		return hex.EncodeToString(sum[:])
	}()

	got, err := FetchCertFingerprint(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchCertFingerprint: %v", err)
	}
	if got != want {
		t.Fatalf("fingerprint = %s, want %s", got, want)
	}
}

func TestFetchCertFingerprint_UnreachableIsNetworkErr(t *testing.T) {
	// Port 1 is not listening; the dial must fail as a network error.
	_, err := FetchCertFingerprint(context.Background(), "https://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected an error dialing an unreachable host")
	}
}

func TestNormalizePin(t *testing.T) {
	const want = "aabbcc"
	for _, in := range []string{"aabbcc", "AABBCC", "sha256:aabbcc", "SHA256:AA:BB:CC", " aa:bb:cc \n"} {
		if got := NormalizePin(in); got != want {
			t.Errorf("NormalizePin(%q) = %q, want %q", in, got, want)
		}
	}
}
