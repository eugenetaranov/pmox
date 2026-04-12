package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestTTYConfirmer(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"y", "y\n", true},
		{"yes", "yes\n", true},
		{"Y", "Y\n", true},
		{"YES", "YES\n", true},
		{"Yes", "Yes\n", true},
		{"n", "n\n", false},
		{"empty", "\n", false},
		{"maybe", "maybe\n", false},
		{"multi-line", "no\nyes\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewTTYConfirmer(strings.NewReader(tc.in), io.Discard)
			got, err := c.Confirm(context.Background(), "Continue? [y/N]: ")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Confirm() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTTYConfirmer_ReaderError(t *testing.T) {
	errBroken := errors.New("broken pipe")
	c := NewTTYConfirmer(&failReader{err: errBroken}, io.Discard)
	got, err := c.Confirm(context.Background(), "prompt")
	if !errors.Is(err, errBroken) {
		t.Errorf("err = %v, want %v", err, errBroken)
	}
	if got {
		t.Error("expected false on error")
	}
}

func TestAlwaysConfirmer_NoIO(t *testing.T) {
	c := AlwaysConfirmer{}
	got, err := c.Confirm(context.Background(), "anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("AlwaysConfirmer should return true")
	}
}

func TestAlwaysConfirmer_NeverReadsOrWrites(t *testing.T) {
	_ = NewTTYConfirmer(&failReader{err: errors.New("should not read")}, &failWriter{err: errors.New("should not write")})
	c := AlwaysConfirmer{}
	got, err := c.Confirm(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected true")
	}
}

type failReader struct{ err error }

func (r *failReader) Read([]byte) (int, error) { return 0, r.err }

type failWriter struct{ err error }

func (w *failWriter) Write([]byte) (int, error) { return 0, w.err }
