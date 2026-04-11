// Package exitcode defines the typed process exit codes used by pmox
// and maps typed errors to the right code via From.
package exitcode

import (
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
)

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
	switch {
	case errors.Is(err, pveclient.ErrUnauthorized):
		return ExitUnauthorized
	case errors.Is(err, pveclient.ErrNotFound):
		return ExitNotFound
	case errors.Is(err, credstore.ErrNotFound):
		return ExitNotFound
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
