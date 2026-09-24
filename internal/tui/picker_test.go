package tui

import (
	"errors"
	"fmt"
	"testing"

	"github.com/charmbracelet/huh"
)

func TestAbortErr(t *testing.T) {
	other := errors.New("boom")
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"user abort", huh.ErrUserAborted, ErrAborted},
		{"wrapped user abort", fmt.Errorf("form: %w", huh.ErrUserAborted), ErrAborted},
		{"other", other, other},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AbortErr(tc.in); !errors.Is(got, tc.want) || (tc.want == nil && got != nil) {
				t.Errorf("AbortErr(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSelectOne_TrivialOptionsNeverPrompt(t *testing.T) {
	got, err := SelectOne("t", nil, "fb")
	if err != nil || got != "fb" {
		t.Errorf("empty opts: got (%q, %v), want (fb, nil)", got, err)
	}
	got, err = SelectOne("t", []huh.Option[string]{huh.NewOption("only", "only")}, "fb")
	if err != nil || got != "only" {
		t.Errorf("single opt: got (%q, %v), want (only, nil)", got, err)
	}
}
