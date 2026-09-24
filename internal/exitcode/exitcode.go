// Package exitcode defines the typed process exit codes used by pmox
// and maps typed errors to the right code via From.
package exitcode

import (
	"context"
	"errors"

	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/pveclient"
)

const (
	ExitOK           = 0
	ExitGeneric      = 1
	ExitUserError    = 2
	ExitNotFound     = 3
	ExitAPIError     = 4
	ExitNetworkError = 5
	ExitUnauthorized = 6
	ExitTimeout      = 7
	ExitHook         = 8
	ExitWarnings     = 9 // doctor --strict: all checks passed but warnings present
)

// Coder is implemented by errors that already know their exact process
// exit code (e.g. `pmox doctor`, which picks the code of the worst
// failing check, or a hook failure returning ExitHook). From honors it,
// anywhere in the wrap chain, before any sentinel matching. Packages
// that cannot import exitcode can satisfy it structurally.
type Coder interface {
	ExitCode() int
}

// hookErrMarker is the legacy marker implemented by *launch.HookError.
// New error types should implement Coder instead; this remains so hook
// errors that only carry the marker still map to ExitHook.
type hookErrMarker interface {
	IsHookError()
}

// ErrUserInput is a sentinel for interactive-prompt input errors
// (invalid entries, too many attempts).
var ErrUserInput = errors.New("user input error")

// ErrNotFound is a sentinel for "requested entity is not configured"
// errors raised outside the PVE client / keychain — e.g. the server
// resolver reporting that no servers are configured.
var ErrNotFound = errors.New("not found")

// From maps a top-level command error to the corresponding exit code.
func From(err error) int {
	if err == nil {
		return ExitOK
	}
	var carrier Coder
	if errors.As(err, &carrier) {
		return carrier.ExitCode()
	}
	var hookErr hookErrMarker
	if errors.As(err, &hookErr) {
		return ExitHook
	}
	switch {
	case errors.Is(err, pveclient.ErrUnauthorized):
		return ExitUnauthorized
	case errors.Is(err, pveclient.ErrNotFound):
		return ExitNotFound
	case errors.Is(err, credstore.ErrNotFound):
		return ExitNotFound
	case errors.Is(err, pveclient.ErrTimeout):
		return ExitTimeout
	case errors.Is(err, context.DeadlineExceeded):
		return ExitTimeout
	case errors.Is(err, pveclient.ErrAPIError):
		return ExitAPIError
	case errors.Is(err, pveclient.ErrNetwork):
		return ExitNetworkError
	case errors.Is(err, pveclient.ErrTLSVerificationFailed):
		return ExitNetworkError
	case errors.Is(err, ErrUserInput):
		return ExitUserError
	case errors.Is(err, ErrNotFound):
		return ExitNotFound
	}
	return ExitGeneric
}
