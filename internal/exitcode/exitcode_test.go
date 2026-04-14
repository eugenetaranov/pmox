package exitcode

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/pveclient"
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
		{"hook error", &fakeHookError{}, ExitHook},
		{"hook error wrapped", fmt.Errorf("launch: %w", &fakeHookError{}), ExitHook},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := From(tc.err); got != tc.want {
				t.Errorf("From(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// fakeHookError is a minimal stand-in for *launch.HookError that lets
// exitcode's test suite verify the ExitHook mapping without importing
// internal/launch (which would create a cycle).
type fakeHookError struct{}

func (e *fakeHookError) Error() string { return "fake hook failed" }
func (e *fakeHookError) IsHookError()  {}
