package pveclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// IsFatalPollError reports whether an error from a status poll is
// permanent and should abort a wait loop immediately, versus a
// transient error that should be retried until the deadline.
//
// Fatal: unauthorized (bad/expired token), TLS verification failure
// (cert won't start verifying mid-poll), and resource-not-found (the
// task/VM genuinely does not exist). Everything else — network blips
// (ErrNetwork) and 5xx/gateway errors (ErrAPIError) — is transient:
// the underlying PVE task keeps running server-side, so a dropped
// connection must not be reported as a hard failure.
func IsFatalPollError(err error) bool {
	return errors.Is(err, ErrUnauthorized) ||
		errors.Is(err, ErrTLSVerificationFailed) ||
		errors.Is(err, ErrNotFound)
}

const taskPollInterval = 500 * time.Millisecond

// TaskStatus is the parsed /tasks/{upid}/status response.
type TaskStatus struct {
	Status     string `json:"status"`     // "running" | "stopped"
	ExitStatus string `json:"exitstatus"` // "OK" on success, error text on failure
}

// GetTaskStatus issues GET /nodes/{node}/tasks/{upid}/status and
// returns the parsed TaskStatus block.
func (c *Client) GetTaskStatus(ctx context.Context, node, upid string) (*TaskStatus, error) {
	path := fmt.Sprintf("/nodes/%s/tasks/%s/status", node, upid)
	body, err := c.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data TaskStatus `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse task status response: %w", err)
	}
	return &payload.Data, nil
}

// WaitTask polls GetTaskStatus until the task completes, the context
// is cancelled, or the timeout elapses. Returns nil if the task
// finished with exit status "OK", an error wrapping ErrAPIError if
// the task stopped with any other exit status, an error wrapping
// ErrTimeout on timeout, or ctx.Err() on cancellation.
func (c *Client) WaitTask(ctx context.Context, node, upid string, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	var lastTransient error
	for {
		status, err := c.GetTaskStatus(ctx, node, upid)
		if err != nil {
			// A fatal error (auth/TLS/not-found) can't be waited out —
			// abort now. A transient error (network blip, 5xx) must not
			// abort the wait: the task is still running server-side, so
			// remember it and keep polling until the deadline.
			if IsFatalPollError(err) {
				return err
			}
			lastTransient = err
		} else if status.Status == "stopped" {
			if status.ExitStatus == "OK" {
				return nil
			}
			return fmt.Errorf("%w: pve task %s: %s", ErrAPIError, upid, status.ExitStatus)
		}
		if time.Now().After(deadline) {
			if lastTransient != nil {
				return fmt.Errorf("%w: waiting for pve task %s (last poll error: %w)", ErrTimeout, upid, lastTransient)
			}
			return fmt.Errorf("%w: waiting for pve task %s", ErrTimeout, upid)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(taskPollInterval):
		}
	}
}
