package exitcode

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

func TestFrom(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"generic", errors.New("boom"), ExitGeneric},
		{"unauthorized", pveclient.ErrUnauthorized, ExitUnauthorized},
		{"unauthorized wrapped", fmt.Errorf("validate: %w", pveclient.ErrUnauthorized), ExitUnauthorized},
		{"api error", pveclient.ErrAPIError, ExitAPIError},
		{"api error wrapped", fmt.Errorf("oops: %w", pveclient.ErrAPIError), ExitAPIError},
		{"network", pveclient.ErrNetwork, ExitNetworkError},
		{"tls", pveclient.ErrTLSVerificationFailed, ExitNetworkError},
		{"pveclient notfound", pveclient.ErrNotFound, ExitNotFound},
		{"credstore notfound", credstore.ErrNotFound, ExitNotFound},
		{"credstore notfound wrapped", fmt.Errorf("x: %w", credstore.ErrNotFound), ExitNotFound},
		{"user input", ErrUserInput, ExitUserError},
		{"user input wrapped", fmt.Errorf("resolve: %w", ErrUserInput), ExitUserError},
		{"exitcode notfound", ErrNotFound, ExitNotFound},
		{"exitcode notfound wrapped", fmt.Errorf("resolve: %w", ErrNotFound), ExitNotFound},
		{"pveclient timeout", pveclient.ErrTimeout, ExitTimeout},
		{"pveclient timeout wrapped", fmt.Errorf("x: %w", pveclient.ErrTimeout), ExitTimeout},
		{"context deadline exceeded", context.DeadlineExceeded, ExitTimeout},
		{"ambiguous vm", vm.ErrAmbiguous, ExitUserError},
		{"ambiguous vm wrapped", fmt.Errorf("resolve: %w", vm.ErrAmbiguous), ExitUserError},
		{"coder hook", &coderError{code: ExitHook}, ExitHook},
		{"coder wrapped", fmt.Errorf("x: %w", &coderError{code: ExitWarnings}), ExitWarnings},
		{"coder beats sentinel", &coderError{code: ExitHook, wrapped: ErrUserInput}, ExitHook},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := From(tc.err); got != tc.want {
				t.Errorf("From(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// coderError implements Coder, optionally wrapping another error.
type coderError struct {
	code    int
	wrapped error
}

func (e *coderError) Error() string { return "coder" }
func (e *coderError) ExitCode() int { return e.code }
func (e *coderError) Unwrap() error { return e.wrapped }

// TestCodeValues pins every exit code: they are a public contract for
// scripts wrapping pmox and must never change.
func TestCodeValues(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"ExitOK", ExitOK, 0},
		{"ExitGeneric", ExitGeneric, 1},
		{"ExitUserError", ExitUserError, 2},
		{"ExitNotFound", ExitNotFound, 3},
		{"ExitAPIError", ExitAPIError, 4},
		{"ExitNetworkError", ExitNetworkError, 5},
		{"ExitUnauthorized", ExitUnauthorized, 6},
		{"ExitTimeout", ExitTimeout, 7},
		{"ExitHook", ExitHook, 8},
		{"ExitWarnings", ExitWarnings, 9},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}
