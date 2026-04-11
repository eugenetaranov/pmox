package pveclient

import "errors"

var (
	ErrUnauthorized          = errors.New("unauthorized")
	ErrNotFound              = errors.New("resource not found")
	ErrAPIError              = errors.New("api error")
	ErrTLSVerificationFailed = errors.New("tls verification failed")
	ErrNetwork               = errors.New("network error")
	ErrTimeout               = errors.New("operation timed out")
)
