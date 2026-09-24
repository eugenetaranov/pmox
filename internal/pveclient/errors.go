package pveclient

import (
	"errors"
	"fmt"
	"net/http"
)

var (
	ErrUnauthorized          = errors.New("unauthorized")
	ErrNotFound              = errors.New("resource not found")
	ErrAPIError              = errors.New("api error")
	ErrTLSVerificationFailed = errors.New("tls verification failed")
	ErrNetwork               = errors.New("network error")
	ErrTimeout               = errors.New("operation timed out")

	// ErrForbidden marks an HTTP 403 (e.g. an API token missing a
	// required privilege). It wraps ErrUnauthorized so existing
	// errors.Is(err, ErrUnauthorized) checks and exit codes still match.
	ErrForbidden = fmt.Errorf("%w: forbidden", ErrUnauthorized)

	// ErrTaskFailed marks an asynchronous PVE task that finished with a
	// non-OK exit status. It wraps ErrAPIError for compatibility with
	// callers that classify task failures as API errors.
	ErrTaskFailed = fmt.Errorf("%w: task failed", ErrAPIError)
)

// APIError is returned for any HTTP response with status >= 400. It
// carries the parsed PVE error envelope and matches the legacy
// sentinels via Is: 401 → ErrUnauthorized, 403 → ErrForbidden (and
// thus ErrUnauthorized), 404 → ErrNotFound, anything else → ErrAPIError.
type APIError struct {
	StatusCode int
	Status     string            // e.g. "500 Internal Server Error"
	Message    string            // envelope "message", if any
	Errors     map[string]string // envelope per-parameter "errors", if any

	body []byte // raw (bounded) response body, for summaries and matching
}

func newAPIError(resp *http.Response, body []byte) *APIError {
	e := &APIError{StatusCode: resp.StatusCode, Status: resp.Status, body: body}
	if env, ok := parseErrorEnvelope(body); ok {
		e.Message = env.Message
		e.Errors = env.Errors
	}
	return e
}

func (e *APIError) Error() string {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		// A 403 is a permission problem that never resolves by waiting,
		// so it reads (and classifies) as unauthorized, not transient.
		return fmt.Sprintf("%v: %s", ErrUnauthorized, e.Status)
	case http.StatusNotFound:
		return fmt.Sprintf("%v: %s", ErrNotFound, e.Status)
	default:
		return fmt.Sprintf("%v: %s: %s", ErrAPIError, e.Status, summarizeBody(e.body))
	}
}

// Is maps the status code onto the package sentinels.
func (e *APIError) Is(target error) bool {
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return target == ErrUnauthorized
	case http.StatusForbidden:
		return target == ErrForbidden || target == ErrUnauthorized
	case http.StatusNotFound:
		return target == ErrNotFound
	default:
		return target == ErrAPIError
	}
}

// TaskError is returned by WaitTask when a task stops with an exit
// status other than "OK". It matches ErrTaskFailed and ErrAPIError.
type TaskError struct {
	UPID       string
	ExitStatus string
}

func (e *TaskError) Error() string {
	return fmt.Sprintf("%v: pve task %s: %s", ErrAPIError, e.UPID, e.ExitStatus)
}

// Is reports whether target is ErrTaskFailed or ErrAPIError.
func (e *TaskError) Is(target error) bool {
	return target == ErrTaskFailed || target == ErrAPIError
}
